package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsolatedEnvironmentRemovesCredentialOverrides(t *testing.T) {
	got := strings.Join(IsolatedEnvironment([]string{
		"PATH=/bin", "CODEX_HOME=/real", "CODEX_SQLITE_HOME=/real/db",
		"CODEX_ACCESS_TOKEN=secret", "CODEX_API_KEY=secret", "OPENAI_API_KEY=secret",
		"HTTPS_PROXY=http://proxy",
	}, "/isolated/codex", "/isolated/sqlite"), "\n")
	for _, forbidden := range []string{"/real", "secret"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("environment contains %q: %s", forbidden, got)
		}
	}
	for _, want := range []string{"CODEX_HOME=/isolated/codex", "CODEX_SQLITE_HOME=/isolated/sqlite", "HTTPS_PROXY=http://proxy"} {
		if !strings.Contains(got, want) {
			t.Fatalf("environment missing %q: %s", want, got)
		}
	}
}

func TestTailBufferKeepsBoundedDiagnosticTail(t *testing.T) {
	buffer := &tailBuffer{limit: 8}
	_, _ = buffer.Write([]byte("old diagnostics:"))
	_, _ = buffer.Write([]byte(" useful"))
	if got, want := buffer.String(), ": useful"; got != want {
		t.Fatalf("tail = %q, want %q", got, want)
	}
}

func TestLauncherRunsProtocolAndCleansIsolatedHome(t *testing.T) {
	var root string
	launcher := testLauncher("success")
	launcher.TempDir = func(_, pattern string) (string, error) {
		path, err := os.MkdirTemp(t.TempDir(), pattern)
		root = path
		return path, err
	}
	session, err := launcher.Start(context.Background(), os.Args[0], Seed{
		Prefix: "client-test-*", AuthJSON: []byte(`{"token":"seed"}`),
		ConfigTOML: []byte("model = \"test\"\n"), InstallationID: "00000000-0000-4000-8000-000000000001",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"auth.json", "config.toml", "installation_id"} {
		info, err := os.Stat(filepath.Join(session.Home, name))
		if err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s permissions = %o, want 600", name, got)
		}
	}
	var result struct {
		Value string `json:"value"`
	}
	if err := session.Request(context.Background(), "test/read", map[string]any{}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != "ok" {
		t.Fatalf("result = %#v", result)
	}
	session.Close()
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated root still exists after Close: %v", err)
	}
}

func TestLauncherTimeoutStopsProcessAndCleansDirectory(t *testing.T) {
	var root string
	launcher := testLauncher("wait-initialize")
	launcher.TempDir = func(_, pattern string) (string, error) {
		path, err := os.MkdirTemp(t.TempDir(), pattern)
		root = path
		return path, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := launcher.Start(ctx, os.Args[0], Seed{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start() error = %v, want deadline exceeded", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated root still exists after timeout: %v", err)
	}
}

func TestLauncherReportsMalformedProtocolAndSanitizesDiagnostics(t *testing.T) {
	_, err := testLauncher("malformed").Start(context.Background(), os.Args[0], Seed{})
	if err == nil {
		t.Fatal("Start() error = nil")
	}
	message := err.Error()
	for _, secret := range []string{"super-secret", "eyJabcdefghijk.abcdefghijklmnop.signature", "sk-abcdefghijk"} {
		if strings.Contains(message, secret) {
			t.Fatalf("error leaked %q: %s", secret, message)
		}
	}
	if !strings.Contains(message, "malformed protocol data") {
		t.Fatalf("error missing malformed-protocol context: %s", message)
	}
}

func TestSanitizeDiagnosticsRedactsCredentialShapes(t *testing.T) {
	got := sanitizeDiagnostics("OPENAI_API_KEY=super-secret bearer bearer-secret eyJabcdefghijk.abcdefghijklmnop.signature sk-abcdefghijk\n" +
		`{"access_token":"json-secret"}` + "\nAuthorization: Basic authorization-secret")
	for _, secret := range []string{"super-secret", "bearer-secret", "eyJabcdefghijk.abcdefghijklmnop.signature", "sk-abcdefghijk", "json-secret", "authorization-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitized diagnostics leaked %q: %s", secret, got)
		}
	}
	for _, want := range []string{"[redacted]", "[redacted-jwt]", "[redacted-api-key]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized diagnostics missing %q: %s", want, got)
		}
	}
}

func testLauncher(scenario string) Launcher {
	launcher := DefaultLauncher()
	launcher.Command = func(string, ...string) *exec.Cmd {
		return exec.Command(os.Args[0], "-test.run=TestClientHelperProcess", "--", "--client-helper", scenario) // #nosec G204 -- test helper invocation.
	}
	return launcher
}

func TestClientHelperProcess(t *testing.T) {
	scenario := helperScenario(os.Args, "--client-helper")
	if scenario == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			switch scenario {
			case "wait-initialize":
				time.Sleep(time.Hour)
			case "malformed":
				_, _ = fmt.Fprintln(os.Stderr, `OPENAI_API_KEY=super-secret bearer super-secret eyJabcdefghijk.abcdefghijklmnop.signature sk-abcdefghijk`)
				fmt.Println("{not-json")
			default:
				fmt.Println(`{"method":"unrelated/notification","params":{}}`)
				fmt.Printf(`{"id":%d,"result":{}}`+"\n", request.ID)
			}
		case "test/read":
			fmt.Println(`{"method":"another/notification","params":{"ignored":true}}`)
			fmt.Printf(`{"id":%d,"result":{"value":"ok"}}`+"\n", request.ID)
		}
	}
	os.Exit(0)
}

func helperScenario(arguments []string, marker string) string {
	for index, argument := range arguments {
		if argument == marker && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	return ""
}
