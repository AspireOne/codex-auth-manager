package authsync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	profilemgr "codex-manage/internal/profiles"
)

func TestPiTargetSyncMergesCodexCredentialAndPreservesOtherProviders(t *testing.T) {
	root := t.TempDir()
	authPath := filepath.Join(root, "auth.json")
	original := []byte(`{"anthropic":{"type":"oauth","access":"anthropic-token"},"openai-codex":{"type":"oauth","legacy":true}}` + "\n")
	if err := os.WriteFile(authPath, original, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	expiresAt := time.Unix(1_900_000_000, 0).UTC()
	target := NewPiTargetAtPath(authPath)
	credentials := profilemgr.AuthCredentials{
		Kind:         profilemgr.AuthKindChatGPT,
		AccountID:    "acct-work",
		AccessToken:  "access-work",
		RefreshToken: "refresh-work",
		ExpiresAt:    expiresAt,
	}
	if err := target.Sync(credentials); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	entries := readPiEntries(t, authPath)
	var anthropic map[string]any
	if err := json.Unmarshal(entries["anthropic"], &anthropic); err != nil {
		t.Fatalf("Unmarshal(anthropic) error = %v", err)
	}
	if anthropic["type"] != "oauth" || anthropic["access"] != "anthropic-token" {
		t.Fatalf("anthropic entry changed: %#v", anthropic)
	}
	var codex map[string]any
	if err := json.Unmarshal(entries[piProviderKey], &codex); err != nil {
		t.Fatalf("Unmarshal(openai-codex) error = %v", err)
	}
	if codex["type"] != "oauth" || codex["access"] != "access-work" ||
		codex["refresh"] != "refresh-work" || codex["accountId"] != "acct-work" ||
		codex["legacy"] != true {
		t.Fatalf("openai-codex entry = %#v, want converted credential with legacy field", codex)
	}
	if got, ok := codex["expires"].(float64); !ok || int64(got) != expiresAt.UnixMilli() {
		t.Fatalf("expires = %#v, want %d", codex["expires"], expiresAt.UnixMilli())
	}
	if _, err := os.Stat(authPath + piAuthLockSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock path stat error = %v, want lock removed", err)
	}
}

func TestPiTargetClearRemovesOnlyCodexCredential(t *testing.T) {
	root := t.TempDir()
	authPath := filepath.Join(root, "auth.json")
	writePiAuthTestFile(t, authPath, `{"anthropic":{"type":"api_key","key":"ant"},"openai-codex":{"type":"oauth","access":"codex"}}`)

	if err := NewPiTargetAtPath(authPath).Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	entries := readPiEntries(t, authPath)
	if _, exists := entries[piProviderKey]; exists {
		t.Fatal("openai-codex entry still exists after Clear()")
	}
	var anthropic map[string]any
	if err := json.Unmarshal(entries["anthropic"], &anthropic); err != nil {
		t.Fatalf("Unmarshal(anthropic) error = %v", err)
	}
	if anthropic["type"] != "api_key" || anthropic["key"] != "ant" {
		t.Fatalf("anthropic entry changed: %#v", anthropic)
	}
}

func TestPiTargetSyncClearsCodexCredentialForAPIKeyState(t *testing.T) {
	root := t.TempDir()
	authPath := filepath.Join(root, "auth.json")
	writePiAuthTestFile(t, authPath, `{"openai-codex":{"type":"oauth","access":"codex"},"google":{"type":"api_key","key":"google"}}`)

	if err := NewPiTargetAtPath(authPath).Sync(profilemgr.AuthCredentials{
		Kind:   profilemgr.AuthKindAPIKey,
		APIKey: "sk-test",
	}); err != nil {
		t.Fatalf("Sync(APIKey) error = %v", err)
	}

	entries := readPiEntries(t, authPath)
	if _, exists := entries[piProviderKey]; exists {
		t.Fatal("openai-codex entry still exists after API-key sync")
	}
	if _, exists := entries["google"]; !exists {
		t.Fatal("unrelated provider was removed after API-key sync")
	}
}

func TestPiTargetRejectsInvalidAuthFileWithoutOverwritingIt(t *testing.T) {
	root := t.TempDir()
	authPath := filepath.Join(root, "auth.json")
	original := []byte("not-json\n")
	if err := os.WriteFile(authPath, original, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := NewPiTargetAtPath(authPath).Sync(profilemgr.AuthCredentials{
		Kind:         profilemgr.AuthKindChatGPT,
		AccountID:    "acct",
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    time.Unix(1_900_000_000, 0),
	})
	if err == nil {
		t.Fatal("Sync() error = nil, want invalid Pi auth error")
	}
	got, readErr := os.ReadFile(authPath) // #nosec G304 -- test path is created under t.TempDir().
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("invalid Pi auth file changed to %q", got)
	}
}

func TestPiTargetSkipsMissingPiDirectory(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "missing", "auth.json")
	err := NewPiTargetAtPath(authPath).Sync(profilemgr.AuthCredentials{
		Kind:         profilemgr.AuthKindChatGPT,
		AccountID:    "acct",
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    time.Unix(1_900_000_000, 0),
	})
	if err != nil {
		t.Fatalf("Sync() error = %v, want missing Pi directory to be ignored", err)
	}
	if _, err := os.Stat(authPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("auth path stat error = %v, want file absent", err)
	}
}

func TestNewPiTargetUsesConfiguredAgentDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("PI_CODING_AGENT_DIR", "~/.pi/agents")
	target := NewPiTarget(home)
	want := filepath.Join(home, ".pi", "agents", piAuthFileName)
	if target.authPath != want {
		t.Fatalf("Pi auth path = %q, want %q", target.authPath, want)
	}
}

func TestNewPiTargetRecognizesExistingPluralAgentDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agents"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", "")
	target := NewPiTarget(home)
	want := filepath.Join(home, ".pi", "agents", piAuthFileName)
	if target.authPath != want {
		t.Fatalf("Pi auth path = %q, want %q", target.authPath, want)
	}
}

func TestPiTargetRemovesStaleCompatibleLock(t *testing.T) {
	root := t.TempDir()
	authPath := filepath.Join(root, "auth.json")
	writePiAuthTestFile(t, authPath, `{"anthropic":{"type":"api_key","key":"ant"}}`)
	lockPath := authPath + piAuthLockSuffix
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("Mkdir(lock) error = %v", err)
	}
	stale := time.Now().Add(-2 * piLockStale)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatalf("Chtimes(lock) error = %v", err)
	}

	if err := NewPiTargetAtPath(authPath).Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock path stat error = %v, want stale lock removed", err)
	}
}

func TestPiTargetAuthFileUsesRestrictivePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission bits")
	}
	root := t.TempDir()
	authPath := filepath.Join(root, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{}`), 0o644); err != nil { // #nosec G306 -- intentionally insecure for the permission-hardening test.
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := NewPiTargetAtPath(authPath).Sync(profilemgr.AuthCredentials{
		Kind:         profilemgr.AuthKindChatGPT,
		AccountID:    "acct",
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    time.Unix(1_900_000_000, 0),
	}); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("permissions = %o, want 600", got)
	}
}

func TestPiTargetSecuresUnchangedAuthFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission bits")
	}
	root := t.TempDir()
	authPath := filepath.Join(root, "auth.json")
	content := `{"openai-codex":{"type":"oauth","access":"access","refresh":"refresh","expires":1900000000000,"accountId":"acct"}}` + "\n"
	if err := os.WriteFile(authPath, []byte(content), 0o644); err != nil { // #nosec G306 -- intentionally insecure for the permission-hardening test.
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := NewPiTargetAtPath(authPath).Sync(profilemgr.AuthCredentials{
		Kind:         profilemgr.AuthKindChatGPT,
		AccountID:    "acct",
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    time.UnixMilli(1900000000000),
	}); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("permissions = %o, want 600", got)
	}
}

func writePiAuthTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func readPiEntries(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test path is created under t.TempDir().
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", path, err)
	}
	return entries
}
