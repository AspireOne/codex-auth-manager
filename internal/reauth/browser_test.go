package reauth

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	profilemgr "codex-manage/internal/profiles"
)

func TestBrowserDetectionOrder(t *testing.T) {
	var lookedUp []string
	browser := testBrowserLauncher(t)
	browser.lookPath = func(name string) (string, error) {
		lookedUp = append(lookedUp, name)
		if name == "google-chrome" {
			return "/browser/chrome", nil
		}
		return "", errors.New("missing")
	}

	selected, err := browser.resolveBrowser()
	if err != nil {
		t.Fatalf("resolveBrowser() error = %v", err)
	}
	if selected.name != "chrome" {
		t.Fatalf("browser = %q, want chrome", selected.name)
	}
	want := []string{"brave-browser", "brave", "chromium", "chromium-browser", "google-chrome"}
	if !reflect.DeepEqual(lookedUp, want) {
		t.Fatalf("lookup order = %v, want %v", lookedUp, want)
	}
}

func TestBrowserEnvironmentOverridePrecedence(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "custom-browser")
	if err := os.WriteFile(executable, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	browser := testBrowserLauncher(t)
	browser.getenv = func(name string) string {
		switch name {
		case browserExecutableEnv:
			return executable
		case legacyBraveEnv:
			return "/ignored/brave"
		default:
			return ""
		}
	}

	selected, err := browser.resolveBrowser()
	if err != nil {
		t.Fatalf("resolveBrowser() error = %v", err)
	}
	if selected.executable != executable {
		t.Fatalf("executable = %q, want %q", selected.executable, executable)
	}
}

func TestBrowserUsesLegacyProfileDirectoryWhenPresent(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "person@example.com")
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "brave")
	if err := os.WriteFile(executable, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	browser := testBrowserLauncher(t)
	browser.getenv = func(name string) string {
		switch name {
		case browserExecutableEnv:
			return executable
		case browserProfilesEnv:
			return root
		default:
			return ""
		}
	}
	browser.command = func(_ string, args ...string) *exec.Cmd {
		gotArgs = append([]string(nil), args...)
		return exec.Command(os.Args[0], "-test.run=TestDetachedBrowserHelper") // #nosec G204 -- test helper invocation.
	}

	err := browser.Open(profilemgr.ProfileSummary{Key: "chatgpt-stable", Label: "person@example.com"}, "https://chatgpt.com/oauth")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "--user-data-dir="+legacy || gotArgs[1] != "https://chatgpt.com/oauth" {
		t.Fatalf("browser args = %v", gotArgs)
	}
}

func TestManagedProfileRootDefaults(t *testing.T) {
	browser := testBrowserLauncher(t)
	browser.homeDir = func() (string, error) { return "/home/person", nil }
	browser.getenv = func(name string) string {
		if name == "XDG_DATA_HOME" {
			return "/data"
		}
		return ""
	}
	host, browserPath, err := browser.managedProfileRoot("brave")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/data", "codex-manage", "browser-profiles", "brave")
	if host != want || browserPath != want {
		t.Fatalf("roots = %q/%q, want %q", host, browserPath, want)
	}
}

func TestWSLBrowserReceivesWindowsProfilePath(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(t.TempDir(), "browser.exe")
	if err := os.WriteFile(executable, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	browser := testBrowserLauncher(t)
	browser.readFile = func(string) ([]byte, error) { return []byte("microsoft-standard-WSL2"), nil }
	browser.getenv = func(name string) string {
		switch name {
		case browserExecutableEnv:
			return executable
		case browserProfilesEnv:
			return root
		default:
			return ""
		}
	}
	browser.output = func(name string, args ...string) ([]byte, error) {
		if name == "wslpath" && len(args) == 2 && args[0] == "-w" {
			return []byte(`C:\Users\person\profile` + "\n"), nil
		}
		return nil, errors.New("unexpected conversion")
	}
	browser.command = func(_ string, args ...string) *exec.Cmd {
		gotArgs = append([]string(nil), args...)
		return exec.Command(os.Args[0], "-test.run=TestDetachedBrowserHelper") // #nosec G204 -- test helper invocation.
	}

	err := browser.Open(profilemgr.ProfileSummary{Key: "chatgpt-stable", Label: "person@example.com"}, "https://chatgpt.com/oauth")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if len(gotArgs) != 2 || gotArgs[0] != `--user-data-dir=C:\Users\person\profile` {
		t.Fatalf("browser args = %v, want converted Windows profile path", gotArgs)
	}
}

func TestSanitizeLegacyProfileName(t *testing.T) {
	tests := map[string]string{
		"../evil name.": "_evil_name",
		"CON":           "_CON",
		"person@x.test": "person@x.test",
	}
	for input, want := range tests {
		if got := sanitizeLegacyProfileName(input); got != want {
			t.Fatalf("sanitizeLegacyProfileName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDetachedBrowserHelper(t *testing.T) {
	if strings.Contains(strings.Join(os.Args, " "), "TestDetachedBrowserHelper") {
		return
	}
}

func testBrowserLauncher(t *testing.T) *browserLauncher {
	t.Helper()
	return &browserLauncher{
		goos: "linux", getenv: func(string) string { return "" },
		homeDir:  func() (string, error) { return t.TempDir(), nil },
		lookPath: func(string) (string, error) { return "", errors.New("missing") },
		stat:     os.Stat, command: exec.Command,
		output:   func(string, ...string) ([]byte, error) { return nil, errors.New("not WSL") },
		readFile: func(string) ([]byte, error) { return []byte("linux"), nil },
	}
}
