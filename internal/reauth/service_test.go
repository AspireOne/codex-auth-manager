package reauth

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	profilemgr "codex-manage/internal/profiles"
)

type fakeBrowser struct {
	opened chan string
	err    error
	onOpen func()
}

func (b fakeBrowser) Open(_ profilemgr.ProfileSummary, authURL string) error {
	if b.opened != nil {
		b.opened <- authURL
	}
	if b.onOpen != nil {
		b.onOpen()
	}
	return b.err
}

func TestServiceReauthenticateInstallsMatchingCredentials(t *testing.T) {
	service, manager, profile := newTestService(t, "success", "acct-work")

	if err := service.Reauthenticate(context.Background(), profile); err != nil {
		t.Fatalf("Reauthenticate() error = %v", err)
	}

	snapshot, err := manager.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.CurrentProfileKey != profile.Key {
		t.Fatalf("CurrentProfileKey = %q, want %q", snapshot.CurrentProfileKey, profile.Key)
	}
	data, err := os.ReadFile(manager.AuthFile) // #nosec G304 -- test path is under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(auth.json): %v", err)
	}
	if !strings.Contains(string(data), `"refreshed":true`) {
		t.Fatalf("auth.json = %s, want refreshed credentials", data)
	}
}

func TestServiceReauthenticateRejectsMismatchedAccountWithoutChanges(t *testing.T) {
	service, manager, profile := newTestService(t, "success", "acct-wrong")
	before, err := os.ReadFile(filepath.Join(manager.ProfileDir, profile.Key)) // #nosec G304 -- test path is under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}

	err = service.Reauthenticate(context.Background(), profile)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Reauthenticate() error = %v, want mismatch", err)
	}
	after, readErr := os.ReadFile(filepath.Join(manager.ProfileDir, profile.Key)) // #nosec G304 -- test path is under t.TempDir.
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(before) {
		t.Fatalf("saved profile changed on mismatch\ngot: %s\nwant: %s", after, before)
	}
	if _, statErr := os.Stat(manager.AuthFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("live auth exists after mismatch: %v", statErr)
	}
}

func TestServiceReauthenticateCancellationPreservesCredentials(t *testing.T) {
	service, manager, profile := newTestService(t, "wait", "acct-work")
	opened := make(chan string, 1)
	service.browser = fakeBrowser{opened: opened}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Reauthenticate(ctx, profile) }()

	select {
	case <-opened:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("browser was not opened")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Reauthenticate() error = %v, want context cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Reauthenticate() did not stop after cancellation")
	}
	if _, err := os.Stat(manager.AuthFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("live auth exists after cancellation: %v", err)
	}
}

func TestServiceCancellationAfterBrowserLaunchPreventsInstallation(t *testing.T) {
	service, manager, profile := newTestService(t, "success", "acct-work")
	ctx, cancel := context.WithCancel(context.Background())
	service.browser = fakeBrowser{onOpen: cancel}

	err := service.Reauthenticate(ctx, profile)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reauthenticate() error = %v, want context cancellation", err)
	}
	if _, statErr := os.Stat(manager.AuthFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("live auth exists after cancellation: %v", statErr)
	}
}

func TestServiceReauthenticateReportsProtocolAndOutputFailures(t *testing.T) {
	tests := []struct {
		scenario string
		want     string
	}{
		{"malformed", "malformed protocol data"},
		{"rejected", "unsupported login"},
		{"failed", "user denied login"},
		{"missing-auth", "did not write auth.json"},
	}
	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			service, _, profile := newTestService(t, test.scenario, "acct-work")
			err := service.Reauthenticate(context.Background(), profile)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Reauthenticate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestServiceRejectsBrowserLaunchFailure(t *testing.T) {
	service, _, profile := newTestService(t, "wait", "acct-work")
	service.browser = fakeBrowser{err: errors.New("browser unavailable")}
	err := service.Reauthenticate(context.Background(), profile)
	if err == nil || !strings.Contains(err.Error(), "browser unavailable") {
		t.Fatalf("Reauthenticate() error = %v, want browser error", err)
	}
}

func TestIsolatedEnvironmentRemovesCredentialOverrides(t *testing.T) {
	got := isolatedEnvironment([]string{
		"PATH=/bin", "CODEX_HOME=/real", "CODEX_SQLITE_HOME=/real/db",
		"CODEX_ACCESS_TOKEN=secret", "CODEX_API_KEY=secret", "OPENAI_API_KEY=secret",
		"HTTPS_PROXY=http://proxy", "CODEX_CA_CERTIFICATE=/ca.pem",
	}, "/temp/codex", "/temp/sqlite")
	joined := strings.Join(got, "\n")
	for _, forbidden := range []string{"/real", "secret"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("environment contains %q: %v", forbidden, got)
		}
	}
	for _, want := range []string{"CODEX_HOME=/temp/codex", "CODEX_SQLITE_HOME=/temp/sqlite", "HTTPS_PROXY=http://proxy", "CODEX_CA_CERTIFICATE=/ca.pem"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("environment missing %q: %v", want, got)
		}
	}
}

func TestValidateAuthURL(t *testing.T) {
	if err := validateAuthURL("https://chatgpt.com/oauth"); err != nil {
		t.Fatalf("validateAuthURL(valid) = %v", err)
	}
	for _, raw := range []string{"http://chatgpt.com/oauth", "https:///missing-host", "not a url"} {
		if err := validateAuthURL(raw); err == nil {
			t.Fatalf("validateAuthURL(%q) = nil, want error", raw)
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

func newTestService(t *testing.T, scenario, loginAccount string) (*Service, profilemgr.Manager, profilemgr.ProfileSummary) {
	t.Helper()
	codexDir := filepath.Join(t.TempDir(), ".codex")
	manager := profilemgr.NewManager(codexDir)
	profilePath := filepath.Join(manager.ProfileDir, "work")
	writeTestAuth(t, profilePath, "acct-work", false)
	snapshot, err := manager.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	service := New(manager)
	service.browser = fakeBrowser{}
	service.lookPath = func(string) (string, error) { return os.Args[0], nil }
	service.command = func(_ string, _ ...string) *exec.Cmd {
		return exec.Command(os.Args[0], "-test.run=TestReauthHelperProcess", "--", "--helper", scenario, loginAccount) // #nosec G204 -- test helper invocation.
	}
	return service, manager, snapshot.Profiles[0]
}

func TestReauthHelperProcess(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-3] != "--helper" {
		return
	}
	scenario := os.Args[len(os.Args)-2]
	accountID := os.Args[len(os.Args)-1]
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			fmt.Printf(`{"id":%d,"result":{}}`+"\n", request.ID)
		case "account/login/start":
			switch scenario {
			case "malformed":
				fmt.Println("{not-json")
			case "rejected":
				fmt.Printf(`{"id":%d,"error":{"code":-32601,"message":"unsupported login"}}`+"\n", request.ID)
			default:
				fmt.Printf(`{"id":%d,"result":{"type":"chatgpt","loginId":"login-1","authUrl":"https://chatgpt.com/oauth"}}`+"\n", request.ID)
				if scenario == "failed" {
					fmt.Println(`{"method":"account/login/completed","params":{"loginId":"login-1","success":false,"error":"user denied login"}}`)
				} else if scenario != "wait" {
					if scenario != "missing-auth" {
						writeHelperAuth(accountID)
					}
					fmt.Println(`{"method":"account/login/completed","params":{"loginId":"login-1","success":true,"error":null}}`)
				}
			}
		case "account/login/cancel":
			return
		}
	}
}

func writeHelperAuth(accountID string) {
	home := os.Getenv("CODEX_HOME")
	data := testAuthData(accountID, true)
	if err := os.WriteFile(filepath.Join(home, "auth.json"), data, 0o600); err != nil {
		os.Exit(3)
	}
}

func writeTestAuth(t *testing.T, path, accountID string, refreshed bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, testAuthData(accountID, refreshed), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testAuthData(accountID string, refreshed bool) []byte {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"work@example.com"}`))
	value := map[string]any{
		"auth_mode": "account", "refreshed": refreshed,
		"tokens": map[string]any{"account_id": accountID, "id_token": "x." + payload + ".x"},
	}
	data, _ := json.Marshal(value)
	return append(data, '\n')
}
