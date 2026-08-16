package profiles

import (
	"os"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS != windowsGOOS {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("cache permissions = %o, want 600", got)
		}
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

func TestProfileStatusCacheTreatsCorruptionUnknownSchemaAndReadFailureAsMissing(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, manager Manager)
	}{
		{name: "corrupt JSON", setup: func(t *testing.T, manager Manager) {
			writeStatusTestFile(t, manager.StatusCacheFile, []byte("{not-json"))
		}},
		{name: "poisoned version one cache", setup: func(t *testing.T, manager Manager) {
			writeStatusTestFile(t, manager.StatusCacheFile, []byte(`{"version":1,"profiles":{"work":{"fetched_at":"2026-08-16T12:00:00Z","auth_status":"sign_in_required"}}}`))
		}},
		{name: "unknown future schema", setup: func(t *testing.T, manager Manager) {
			writeStatusTestFile(t, manager.StatusCacheFile, []byte(`{"version":999,"profiles":{"work":{}}}`))
		}},
		{name: "read failure", setup: func(t *testing.T, manager Manager) {
			if err := os.MkdirAll(manager.StatusCacheFile, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(filepath.Join(t.TempDir(), ".codex"))
			if err := os.MkdirAll(filepath.Dir(manager.StatusCacheFile), 0o700); err != nil {
				t.Fatal(err)
			}
			test.setup(t, manager)
			loaded, err := manager.LoadProfileStatuses(map[string]AuthKind{"work": AuthKindChatGPT}, time.Now())
			if err != nil {
				t.Fatalf("LoadProfileStatuses() blocked startup: %v", err)
			}
			if len(loaded) != 0 {
				t.Fatalf("loaded = %#v, want cache miss", loaded)
			}
		})
	}
}

func TestProfileStatusCacheMergesInvalidatesAndRejectsInvalidEntries(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), ".codex"))
	if err := manager.ensurePrivateStorage(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, key := range []string{"one", "two"} {
		if err := manager.SaveProfileStatus(key, ProfileStatus{FetchedAt: now, AuthStatus: ProfileAuthSignInRequired}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.InvalidateProfileStatus("one"); err != nil {
		t.Fatal(err)
	}
	loaded, err := manager.LoadProfileStatuses(map[string]AuthKind{"one": AuthKindChatGPT, "two": AuthKindChatGPT}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded["two"].AuthStatus != ProfileAuthSignInRequired {
		t.Fatalf("loaded = %#v, want only two", loaded)
	}
	invalidPercent := 101
	for _, invalid := range []ProfileStatus{
		{},
		{FetchedAt: now, AuthStatus: ProfileAuthAuthenticated},
		{FetchedAt: now, AuthStatus: ProfileAuthAuthenticated, UsedPercent: &invalidPercent},
		{FetchedAt: now, AuthStatus: "unexpected"},
	} {
		if err := manager.SaveProfileStatus("invalid", invalid); err == nil {
			t.Fatalf("SaveProfileStatus(%#v) error = nil", invalid)
		}
	}
}

func TestReconcileStatusCredentialsRejectsIdentityMismatchDeletedAndChangedActiveAuth(t *testing.T) {
	original := []byte(`{"auth_mode":"account","tokens":{"account_id":"acct"},"token":"old"}`)
	refreshed := []byte(`{"auth_mode":"account","tokens":{"account_id":"acct"},"token":"new"}`)
	tests := []struct {
		name   string
		fresh  []byte
		setup  func(t *testing.T, manager Manager, profilePath string)
		wanted string
	}{
		{name: "identity mismatch", fresh: []byte(`{"auth_mode":"account","tokens":{"account_id":"other"}}`), wanted: "different account"},
		{name: "deleted profile", fresh: refreshed, setup: func(t *testing.T, _ Manager, profilePath string) {
			if err := os.Remove(profilePath); err != nil {
				t.Fatal(err)
			}
		}, wanted: "profile changed"},
		{name: "changed active auth", fresh: refreshed, setup: func(t *testing.T, manager Manager, _ string) {
			if err := manager.Activate("work"); err != nil {
				t.Fatal(err)
			}
			writeStatusTestFile(t, manager.AuthFile, []byte(`{"auth_mode":"account","tokens":{"account_id":"acct"},"token":"concurrent"}`))
		}, wanted: "active credentials changed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(filepath.Join(t.TempDir(), ".codex"))
			profilePath := filepath.Join(manager.ProfileDir, "work")
			writeStatusTestFile(t, profilePath, original)
			if test.setup != nil {
				test.setup(t, manager, profilePath)
			}
			err := manager.ReconcileStatusCredentials("work", original, test.fresh)
			if err == nil || !strings.Contains(err.Error(), test.wanted) {
				t.Fatalf("ReconcileStatusCredentials() error = %v, want %q", err, test.wanted)
			}
		})
	}
}

func TestCredentialReplacementAndDeletionInvalidateStatusCache(t *testing.T) {
	for _, operation := range []string{"replace", "delete"} {
		t.Run(operation, func(t *testing.T) {
			manager := NewManager(filepath.Join(t.TempDir(), ".codex"))
			profilePath := filepath.Join(manager.ProfileDir, "work")
			original := []byte(`{"auth_mode":"account","tokens":{"account_id":"acct"},"token":"old"}`)
			writeStatusTestFile(t, profilePath, original)
			if _, err := manager.Snapshot(); err != nil {
				t.Fatal(err)
			}
			if err := manager.SaveProfileStatus("work", ProfileStatus{FetchedAt: time.Now(), AuthStatus: ProfileAuthSignInRequired}); err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "replace":
				freshPath := filepath.Join(t.TempDir(), "fresh.json")
				writeStatusTestFile(t, freshPath, []byte(`{"auth_mode":"account","tokens":{"account_id":"acct"},"token":"new"}`))
				if err := manager.ReplaceAndActivate("work", freshPath); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := manager.Delete("work", ""); err != nil {
					t.Fatal(err)
				}
			}
			loaded, err := manager.LoadProfileStatuses(map[string]AuthKind{"work": AuthKindChatGPT}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded) != 0 {
				t.Fatalf("status cache survived %s: %#v", operation, loaded)
			}
		})
	}
}

func writeStatusTestFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}
