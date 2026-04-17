package profiles

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerSaveCurrentReturnsErrStateChangedAfterProfileIsSaved(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, paths.authFile, authFixture("account-save", "api-save"))
	makeBlockingDir(t, paths.markerFile)

	err := m.SaveCurrent("saved")
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("SaveCurrent error = %v, want ErrStateChanged", err)
	}

	assertFileExists(t, filepath.Join(paths.profileDir, "saved"))
}

func TestManagerRenameReturnsErrStateChangedAfterProfileIsRenamed(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "old"), authFixture("account-rename", "api-rename"))
	writeMarkerFile(t, paths.markerFile, currentProfileMarker{
		Name:     "old",
		Identity: authIdentity{AuthMode: "account", AccountID: "account-rename"},
	})
	makeBlockingDir(t, paths.markerFile)

	err := m.Rename("old", "new", "old")
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("Rename error = %v, want ErrStateChanged", err)
	}

	assertFileExists(t, filepath.Join(paths.profileDir, "new"))
	assertFileMissing(t, filepath.Join(paths.profileDir, "old"))
}

func TestManagerDeleteReturnsErrStateChangedAfterProfileIsDeleted(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "current"), authFixture("account-delete", "api-delete"))
	makeBlockingDir(t, paths.markerFile)

	err := m.Delete("current", "current")
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("Delete error = %v, want ErrStateChanged", err)
	}

	assertFileMissing(t, filepath.Join(paths.profileDir, "current"))
}

func TestManagerLogoutReturnsErrStateChangedAfterAuthIsRemoved(t *testing.T) {
	m, paths := newTestManager(t)
	if err := os.WriteFile(paths.authFile, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", paths.authFile, err)
	}
	makeBlockingDir(t, paths.markerFile)

	err := m.Logout()
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("Logout error = %v, want ErrStateChanged", err)
	}

	assertFileMissing(t, paths.authFile)
}

type testManagerPaths struct {
	authFile   string
	profileDir string
	legacyDir  string
	markerFile string
}

func newTestManager(t *testing.T) (Manager, testManagerPaths) {
	t.Helper()

	root := t.TempDir()
	paths := testManagerPaths{
		authFile:   filepath.Join(root, "auth.json"),
		profileDir: filepath.Join(root, "auth_manager", "profiles"),
		legacyDir:  filepath.Join(root, "auth_manager"),
		markerFile: filepath.Join(root, "auth_manager", CurrentProfileMarkerName),
	}

	for _, dir := range []string{paths.profileDir, paths.legacyDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	return Manager{
		AuthFile:           paths.authFile,
		ProfileDir:         paths.profileDir,
		LegacyProfileDir:   paths.legacyDir,
		CurrentProfileFile: paths.markerFile,
	}, paths
}

func authFixture(accountID, apiKey string) map[string]any {
	return map[string]any{
		"auth_mode":      "account",
		"OPENAI_API_KEY": apiKey,
		"tokens": map[string]any{
			"account_id": accountID,
		},
	}
}

func writeAuthFile(t *testing.T, path string, body map[string]any) {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal auth fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func writeMarkerFile(t *testing.T, path string, marker currentProfileMarker) {
	t.Helper()

	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatalf("Marshal marker fixture: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func makeBlockingDir(t *testing.T, path string) {
	t.Helper()

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove(%q): %v", path, err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	blocker := filepath.Join(path, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", blocker, err)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if info.IsDir() {
		t.Fatalf("%q is a directory, want file", path)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(%q) = %v, want not exists", path, err)
	}
}
