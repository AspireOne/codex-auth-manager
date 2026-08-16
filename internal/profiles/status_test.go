package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfileStatusCacheRoundTripTTLAndPermissions(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), ".codex"))
	if err := m.ensurePrivateStorage(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	percent := 38
	reset := now.Add(time.Hour)
	status := ProfileStatus{FetchedAt: now, AuthStatus: ProfileAuthAuthenticated, UsedPercent: &percent, ResetsAt: &reset}
	if err := m.SaveProfileStatus("work", status); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(m.StatusCacheFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("cache permissions = %o, want 600", got)
	}
	loaded, err := m.LoadProfileStatuses(map[string]AuthKind{"work": AuthKindChatGPT}, now.Add(29*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if loaded["work"].UsedPercent == nil || *loaded["work"].UsedPercent != percent {
		t.Fatalf("loaded status = %#v", loaded["work"])
	}
}

func TestProfileStatusCachePrunesFutureDeletedAndAPIKeyEntries(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), ".codex"))
	if err := m.ensurePrivateStorage(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for key, fetched := range map[string]time.Time{"future": now.Add(time.Minute), "deleted": now, "api": now} {
		status := ProfileStatus{FetchedAt: fetched, AuthStatus: ProfileAuthSignInRequired}
		if err := m.SaveProfileStatus(key, status); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := m.LoadProfileStatuses(map[string]AuthKind{"future": AuthKindChatGPT, "api": AuthKindAPIKey}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("loaded statuses = %#v, want empty", loaded)
	}
}

func TestReconcileStatusCredentialsRejectsConcurrentChange(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), ".codex"))
	path := filepath.Join(m.ProfileDir, "work")
	original := []byte(`{"auth_mode":"account","tokens":{"account_id":"acct"},"token":"old"}`)
	changed := []byte(`{"auth_mode":"account","tokens":{"account_id":"acct"},"token":"changed"}`)
	refreshed := []byte(`{"auth_mode":"account","tokens":{"account_id":"acct"},"token":"new"}`)
	if err := os.MkdirAll(m.ProfileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	err := m.ReconcileStatusCredentials("work", original, refreshed)
	if err == nil || !strings.Contains(err.Error(), "changed while status was loading") {
		t.Fatalf("ReconcileStatusCredentials() error = %v, want concurrent-change rejection", err)
	}
	got, readErr := os.ReadFile(path) // #nosec G304 -- test-controlled temporary path.
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(changed) {
		t.Fatalf("profile changed to %s", got)
	}
}
