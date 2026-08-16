package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	profilemgr "codex-manage/internal/profiles"
	"codex-manage/internal/reauth"
)

type fakeCodexObservation struct {
	Args                []string `json:"args"`
	CodexHome           string   `json:"codexHome"`
	SQLiteHome          string   `json:"sqliteHome"`
	Config              string   `json:"config"`
	HTTPSProxy          string   `json:"httpsProxy"`
	HasCodexAccessToken bool     `json:"hasCodexAccessToken"`
	HasCodexAPIKey      bool     `json:"hasCodexApiKey"`
	HasOpenAIAPIKey     bool     `json:"hasOpenaiApiKey"`
	ReceivedInitialize  bool     `json:"receivedInitialize"`
	ReceivedInitialized bool     `json:"receivedInitialized"`
	ReceivedLoginStart  bool     `json:"receivedLoginStart"`
}

func TestLoginProductionComposition(t *testing.T) {
	toolDir := t.TempDir()
	executableSuffix := ""
	if runtime.GOOS == "windows" {
		executableSuffix = ".exe"
	}
	fakeCodex := filepath.Join(toolDir, "codex"+executableSuffix)
	fakeBrowser := filepath.Join(toolDir, "fixture-browser"+executableSuffix)
	buildFakeTool(t, fakeCodex)
	buildFakeTool(t, fakeBrowser)

	originalAuthenticator := newAuthenticator
	newAuthenticator = func(manager profilemgr.Manager) reauth.Authenticator { return reauth.New(manager) }
	t.Cleanup(func() { newAuthenticator = originalAuthenticator })

	t.Run("successful login updates and activates only after the full protocol", func(t *testing.T) {
		environment := newLoginIntegrationEnvironment(t, toolDir, fakeBrowser, "success.jsonl", "work-refreshed.json")
		copyIntegrationFixture(t, "config.toml", filepath.Join(environment.codexDir, "config.toml"))
		copyIntegrationFixture(t, filepath.Join("auth", "work-old.json"), environment.profilePath)
		copyIntegrationFixture(t, filepath.Join("auth", "work-old.json"), environment.authPath)

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"--login", "work@example.com"}, &stdout, &stderr, func() (string, error) {
			return environment.home, nil
		}, failUIRun(t))
		if code != 0 {
			t.Fatalf("run(--login) code = %d, stderr=%q", code, stderr.String())
		}
		if stderr.String() != "" {
			t.Fatalf("stderr = %q, want empty", stderr.String())
		}
		if !strings.Contains(stdout.String(), "Authenticated and activated") {
			t.Fatalf("stdout = %q, want success state", stdout.String())
		}

		assertIntegrationFixture(t, filepath.Join("auth", "work-refreshed.json"), environment.profilePath)
		assertIntegrationFixture(t, filepath.Join("auth", "work-refreshed.json"), environment.authPath)
		assertIntegrationMarker(t, filepath.Join(environment.codexDir, "auth_manager", profilemgr.CurrentProfileMarkerName), "work")

		browserArgs := readJSONEventually[[]string](t, environment.browserLog)
		wantProfileDir := filepath.Join(environment.browserRoot, "work")
		if len(browserArgs) != 2 || browserArgs[0] != "--user-data-dir="+wantProfileDir || browserArgs[1] != "https://chatgpt.com/oauth/fixture" {
			t.Fatalf("browser args = %v, want isolated profile and fixture URL", browserArgs)
		}

		observed := readJSONEventually[fakeCodexObservation](t, environment.codexLog)
		if !observed.ReceivedInitialize || !observed.ReceivedInitialized || !observed.ReceivedLoginStart {
			t.Fatalf("protocol observation = %#v, want complete handshake", observed)
		}
		if got, want := observed.Args, []string{"-c", `cli_auth_credentials_store="file"`, "app-server", "--listen", "stdio://"}; !equalStrings(got, want) {
			t.Fatalf("codex args = %v, want %v", got, want)
		}
		config := readIntegrationFixture(t, "config.toml")
		if observed.Config != string(config) {
			t.Fatalf("isolated config = %q, want fixture %q", observed.Config, config)
		}
		if observed.CodexHome == environment.codexDir || !strings.Contains(observed.CodexHome, "codex-manage-login-") {
			t.Fatalf("CODEX_HOME = %q, want isolated temporary home", observed.CodexHome)
		}
		if observed.SQLiteHome == "" || observed.SQLiteHome == observed.CodexHome {
			t.Fatalf("CODEX_SQLITE_HOME = %q, want separate isolated state", observed.SQLiteHome)
		}
		if observed.HasCodexAccessToken || observed.HasCodexAPIKey || observed.HasOpenAIAPIKey {
			t.Fatalf("credential overrides leaked into app-server: %#v", observed)
		}
		if observed.HTTPSProxy != "http://fixture-proxy.invalid" {
			t.Fatalf("HTTPS_PROXY = %q, want preserved proxy", observed.HTTPSProxy)
		}
		if _, err := os.Stat(filepath.Dir(observed.CodexHome)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary login root still exists after completion: %v", err)
		}
	})

	t.Run("mismatched account leaves saved and active credentials byte-identical", func(t *testing.T) {
		environment := newLoginIntegrationEnvironment(t, toolDir, fakeBrowser, "success.jsonl", "wrong-account.json")
		copyIntegrationFixture(t, filepath.Join("auth", "work-old.json"), environment.profilePath)
		copyIntegrationFixture(t, filepath.Join("auth", "work-old.json"), environment.authPath)

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"--login", "work@example.com"}, &stdout, &stderr, func() (string, error) {
			return environment.home, nil
		}, failUIRun(t))
		if code != 1 || !strings.Contains(stderr.String(), "does not match") {
			t.Fatalf("code/stderr = %d/%q, want mismatch failure", code, stderr.String())
		}
		assertIntegrationFixture(t, filepath.Join("auth", "work-old.json"), environment.profilePath)
		assertIntegrationFixture(t, filepath.Join("auth", "work-old.json"), environment.authPath)
	})

	t.Run("protocol rejection never launches a browser or changes credentials", func(t *testing.T) {
		environment := newLoginIntegrationEnvironment(t, toolDir, fakeBrowser, "rejected.jsonl", "")
		copyIntegrationFixture(t, filepath.Join("auth", "work-old.json"), environment.profilePath)
		copyIntegrationFixture(t, filepath.Join("auth", "work-old.json"), environment.authPath)

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := run([]string{"--login", "work@example.com"}, &stdout, &stderr, func() (string, error) {
			return environment.home, nil
		}, failUIRun(t))
		if code != 1 || !strings.Contains(stderr.String(), "fixture login method unsupported") {
			t.Fatalf("code/stderr = %d/%q, want protocol rejection", code, stderr.String())
		}
		assertIntegrationFixture(t, filepath.Join("auth", "work-old.json"), environment.profilePath)
		assertIntegrationFixture(t, filepath.Join("auth", "work-old.json"), environment.authPath)
		if _, err := os.Stat(environment.browserLog); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("browser log exists after rejected login: %v", err)
		}
	})
}

type loginIntegrationEnvironment struct {
	home        string
	codexDir    string
	profilePath string
	authPath    string
	browserRoot string
	browserLog  string
	codexLog    string
}

func newLoginIntegrationEnvironment(t *testing.T, toolDir, fakeBrowser, scenario, authFixture string) loginIntegrationEnvironment {
	t.Helper()
	home := t.TempDir()
	logDir, err := os.MkdirTemp(toolDir, "run-*")
	if err != nil {
		t.Fatalf("MkdirTemp(integration logs): %v", err)
	}
	codexDir := filepath.Join(home, ".codex")
	environment := loginIntegrationEnvironment{
		home: home, codexDir: codexDir,
		profilePath: filepath.Join(codexDir, "auth_manager", "profiles", "work"),
		authPath:    filepath.Join(codexDir, "auth.json"),
		browserRoot: filepath.Join(home, "browser-profiles"),
		browserLog:  filepath.Join(logDir, "browser.json"),
		codexLog:    filepath.Join(logDir, "codex.json"),
	}
	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEX_MANAGE_BROWSER_EXECUTABLE", fakeBrowser)
	t.Setenv("CODEX_MANAGE_BROWSER_PROFILES_DIR", environment.browserRoot)
	t.Setenv("FAKE_BROWSER_LOG", environment.browserLog)
	t.Setenv("FAKE_CODEX_LOG", environment.codexLog)
	t.Setenv("FAKE_CODEX_SCENARIO_FILE", integrationFixturePath(filepath.Join("scenarios", scenario)))
	if authFixture == "" {
		t.Setenv("FAKE_CODEX_AUTH_FILE", "")
	} else {
		t.Setenv("FAKE_CODEX_AUTH_FILE", integrationFixturePath(filepath.Join("auth", authFixture)))
	}
	t.Setenv("CODEX_ACCESS_TOKEN", "must-not-leak")
	t.Setenv("CODEX_API_KEY", "must-not-leak")
	t.Setenv("OPENAI_API_KEY", "must-not-leak")
	t.Setenv("HTTPS_PROXY", "http://fixture-proxy.invalid")
	return environment
}

func buildFakeTool(t *testing.T, output string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", output, "./testdata/fake-tool") // #nosec G204 -- fixed checked-in test fixture package.
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building fake tool: %v\n%s", err, data)
	}
}

func integrationFixturePath(relative string) string {
	return filepath.Join("testdata", relative)
}

func readIntegrationFixture(t *testing.T, relative string) []byte {
	t.Helper()
	data, err := os.ReadFile(integrationFixturePath(relative)) // #nosec G304 -- path is a checked-in test fixture.
	if err != nil {
		t.Fatalf("ReadFile(fixture %q): %v", relative, err)
	}
	return data
}

func copyIntegrationFixture(t *testing.T, relative, destination string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(destination), err)
	}
	if err := os.WriteFile(destination, readIntegrationFixture(t, relative), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", destination, err)
	}
}

func assertIntegrationFixture(t *testing.T, relative, actual string) {
	t.Helper()
	want := readIntegrationFixture(t, relative)
	got, err := os.ReadFile(actual) // #nosec G304 -- actual is under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", actual, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %s, want fixture %s", actual, got, want)
	}
}

func assertIntegrationMarker(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- path is under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(marker): %v", err)
	}
	var marker struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("Unmarshal(marker): %v", err)
	}
	if marker.Name != want {
		t.Fatalf("marker name = %q, want %q", marker.Name, want)
	}
}

func readJSONEventually[T any](t *testing.T, path string) T {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, err := os.ReadFile(path) // #nosec G304 -- path is under t.TempDir.
		if err == nil {
			var result T
			if unmarshalErr := json.Unmarshal(data, &result); unmarshalErr == nil {
				return result
			} else if time.Now().After(deadline) {
				t.Fatalf("Unmarshal(%q): %v", path, unmarshalErr)
			}
		} else if !errors.Is(err, os.ErrNotExist) || time.Now().After(deadline) {
			t.Fatalf("ReadFile(%q): %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
