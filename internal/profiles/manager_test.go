package profiles

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagerActivateRestoresMissingAuthAndWritesCurrentProfileMarker(t *testing.T) {
	m, paths := newTestManager(t)
	profileName := "restored"
	profilePath := filepath.Join(paths.profileDir, profileName)
	writeAuthFile(t, profilePath, authFixture("account-activate", "api-activate"))
	assertFileMissing(t, paths.authFile)

	if err := m.Activate(profileName); err != nil {
		t.Fatalf("Activate(%q) error = %v", profileName, err)
	}

	assertFileExists(t, paths.authFile)
	profileData, err := os.ReadFile(profilePath) // #nosec G304 -- test fixture path is created under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", profilePath, err)
	}
	authData, err := os.ReadFile(paths.authFile)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", paths.authFile, err)
	}
	if !bytes.Equal(authData, profileData) {
		t.Fatalf("activated auth.json content = %q, want %q", authData, profileData)
	}

	marker, err := readCurrentProfileMarker(paths.markerFile, paths.profileDir)
	if err != nil {
		t.Fatalf("readCurrentProfileMarker() error = %v", err)
	}
	if marker.Name != profileName {
		t.Fatalf("current profile marker name = %q, want %q", marker.Name, profileName)
	}
}

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

func TestManagerSaveCurrentRejectsDuplicateCredentialsUnderNewName(t *testing.T) {
	m, paths := newTestManager(t)
	auth := authFixture("account-duplicate", "api-duplicate")
	writeAuthFile(t, paths.authFile, auth)
	writeAuthFile(t, filepath.Join(paths.profileDir, "existing"), auth)

	err := m.SaveCurrent("new-name")
	if err == nil {
		t.Fatal("SaveCurrent error = nil, want duplicate credentials error")
	}
	if want := `same auth already exists as profile "existing"`; err.Error() != want {
		t.Fatalf("SaveCurrent error = %q, want %q", err.Error(), want)
	}

	assertFileMissing(t, filepath.Join(paths.profileDir, "new-name"))
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

func TestManagerSyncTrackedProfileDoesNotOverwriteProfileWhenAuthIdentityNoLongerMatchesMarker(t *testing.T) {
	m, paths := newTestManager(t)
	profilePath := filepath.Join(paths.profileDir, "profile-a")
	profileA := authFixture("account-a", "api-a")
	writeAuthFile(t, profilePath, profileA)
	writeMarkerFile(t, paths.markerFile, currentProfileMarker{
		Name:     "profile-a",
		Identity: authIdentity{AuthMode: "account", AccountID: "account-a"},
	})
	writeAuthFile(t, paths.authFile, authFixture("account-b", "api-b"))

	want, err := os.ReadFile(profilePath) // #nosec G304 -- test fixture path is created under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", profilePath, err)
	}

	if err := m.SyncTrackedProfile(); err != nil {
		t.Fatalf("SyncTrackedProfile() error = %v", err)
	}

	got, err := os.ReadFile(profilePath) // #nosec G304 -- test fixture path is created under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", profilePath, err)
	}
	if string(got) != string(want) {
		t.Fatalf("profile content changed after SyncTrackedProfile()\ngot:  %s\nwant: %s", got, want)
	}
}

func TestManagerSyncTrackedProfileCopiesChangedAuthWhenIdentityStillMatches(t *testing.T) {
	m, paths := newTestManager(t)
	profileName := "work"
	profilePath := filepath.Join(paths.profileDir, profileName)

	savedAuth := realisticAuthFixture("acct_same_identity", "session-original", "refresh-original", "https://chatgpt.com/backend-api")
	updatedAuth := realisticAuthFixture("acct_same_identity", "session-updated", "refresh-updated", "https://chatgpt.com/backend-api")
	updatedAuth["last_refresh_at"] = "2026-04-17T12:34:56Z"
	updatedAuth["extra"] = map[string]any{
		"workspace": "codex-manage",
	}

	writeAuthFile(t, profilePath, savedAuth)
	writeAuthFile(t, paths.authFile, updatedAuth)
	writeMarkerFile(t, paths.markerFile, currentProfileMarker{
		Name: profileName,
		Identity: authIdentity{
			AuthMode:  "account",
			AccountID: "acct_same_identity",
		},
	})

	if err := m.SyncTrackedProfile(); err != nil {
		t.Fatalf("SyncTrackedProfile() error = %v", err)
	}

	assertFilesEqual(t, profilePath, paths.authFile)

	marker, err := readCurrentProfileMarker(paths.markerFile, paths.profileDir)
	if err != nil {
		t.Fatalf("readCurrentProfileMarker() error = %v", err)
	}
	if marker.Name != profileName {
		t.Fatalf("marker.Name = %q, want %q", marker.Name, profileName)
	}
	if !marker.Identity.matches(authIdentity{AuthMode: "account", AccountID: "acct_same_identity"}) {
		t.Fatalf("marker.Identity = %#v, want matching account identity", marker.Identity)
	}
}

func TestManagerSnapshotCreatesMissingAuthManagerDirectory(t *testing.T) {
	codexDir := t.TempDir()
	m := NewManager(codexDir)

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	if snapshot.AuthActive {
		t.Fatalf("Snapshot().AuthActive = true, want false")
	}
	if len(snapshot.Profiles) != 0 {
		t.Fatalf("Snapshot().Profiles = %#v, want empty", snapshot.Profiles)
	}
	assertDirExists(t, filepath.Join(codexDir, "auth_manager"))
	assertDirExists(t, filepath.Join(codexDir, "auth_manager", "profiles"))
}

func TestManagerSnapshotMigratesLegacyProfile(t *testing.T) {
	m, paths := newTestManager(t)
	const profileName = "work-account"
	legacyProfile := filepath.Join(paths.legacyDir, profileName)
	migratedProfile := filepath.Join(paths.profileDir, profileName)
	writeAuthFile(t, legacyProfile, realisticAuthFixture(
		"acct_legacy_work",
		"session-legacy-work",
		"refresh-legacy-work",
		"https://chatgpt.com/backend-api",
	))

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	assertFileMissing(t, legacyProfile)
	assertFileExists(t, migratedProfile)
	assertProfiles(t, snapshot.Profiles, []string{profileName})
}

func TestManagerSnapshotIgnoresStaleCurrentProfileMarker(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, paths.authFile, authFixture("account-active", "api-active"))
	writeMarkerFile(t, paths.markerFile, currentProfileMarker{
		Name:     "deleted-profile",
		Identity: authIdentity{AuthMode: "account", AccountID: "account-active"},
	})

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	if !snapshot.AuthActive {
		t.Fatalf("Snapshot().AuthActive = false, want true")
	}
	if snapshot.CurrentProfile != "" {
		t.Fatalf("Snapshot().CurrentProfile = %q, want empty", snapshot.CurrentProfile)
	}
}

func TestManagerSnapshotIgnoresInvalidProfileFiles(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "valid"), authFixture("account-valid", "api-valid"))
	if err := os.WriteFile(filepath.Join(paths.profileDir, "corrupt"), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt profile: %v", err)
	}

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	assertProfiles(t, snapshot.Profiles, []string{"valid"})
	if len(snapshot.InvalidProfiles) != 1 {
		t.Fatalf("Snapshot().InvalidProfiles = %#v, want one invalid profile", snapshot.InvalidProfiles)
	}
	if snapshot.InvalidProfiles[0].Name != "corrupt" {
		t.Fatalf("invalid profile name = %q, want corrupt", snapshot.InvalidProfiles[0].Name)
	}
	if snapshot.InvalidProfiles[0].Reason != "invalid JSON" {
		t.Fatalf("invalid profile reason = %q, want invalid JSON", snapshot.InvalidProfiles[0].Reason)
	}
}

func TestManagerSnapshotReportsInvalidLegacyProfileFiles(t *testing.T) {
	m, paths := newTestManager(t)
	if err := os.WriteFile(filepath.Join(paths.legacyDir, "corrupt-legacy"), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt legacy profile: %v", err)
	}

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	assertProfiles(t, snapshot.Profiles, nil)
	if len(snapshot.InvalidProfiles) != 1 {
		t.Fatalf("Snapshot().InvalidProfiles = %#v, want one invalid profile", snapshot.InvalidProfiles)
	}
	if snapshot.InvalidProfiles[0].Name != "corrupt-legacy" {
		t.Fatalf("invalid legacy profile name = %q, want corrupt-legacy", snapshot.InvalidProfiles[0].Name)
	}
	if snapshot.InvalidProfiles[0].Reason != "invalid JSON" {
		t.Fatalf("invalid legacy profile reason = %q, want invalid JSON", snapshot.InvalidProfiles[0].Reason)
	}
}

func TestManagerSnapshotMigratesLegacyConflictKeepingBothProfiles(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.legacyDir, "foo"), authFixture("legacy-account", "legacy-api"))
	writeAuthFile(t, filepath.Join(paths.profileDir, "foo"), authFixture("profiles-account", "profiles-api"))

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	assertFileExists(t, filepath.Join(paths.profileDir, "foo"))
	assertFileExists(t, filepath.Join(paths.profileDir, "foo-legacy"))
	assertFileMissing(t, filepath.Join(paths.legacyDir, "foo"))
	assertProfiles(t, snapshot.Profiles, []string{"foo", "foo-legacy"})

	profilesIdentity, err := readAuthIdentity(filepath.Join(paths.profileDir, "foo"))
	if err != nil {
		t.Fatalf("readAuthIdentity(profiles/foo): %v", err)
	}
	if profilesIdentity.AccountID != "profiles-account" {
		t.Fatalf("profiles/foo account ID = %q, want profiles-account", profilesIdentity.AccountID)
	}

	legacyIdentity, err := readAuthIdentity(filepath.Join(paths.profileDir, "foo-legacy"))
	if err != nil {
		t.Fatalf("readAuthIdentity(profiles/foo-legacy): %v", err)
	}
	if legacyIdentity.AccountID != "legacy-account" {
		t.Fatalf("profiles/foo-legacy account ID = %q, want legacy-account", legacyIdentity.AccountID)
	}
	if profilesIdentity.matches(legacyIdentity) || legacyIdentity.matches(profilesIdentity) {
		t.Fatalf("migrated identities match, want distinct identities: profiles/foo=%#v profiles/foo-legacy=%#v", profilesIdentity, legacyIdentity)
	}
}

func TestManagerSetNotePersistsAndSnapshotReturnsIt(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), authFixture("account-work", "api-work"))

	if err := m.SetNote("work", "Plus trial ends soon"); err != nil {
		t.Fatalf("SetNote() error = %v", err)
	}

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	assertProfiles(t, snapshot.Profiles, []string{"work"})
	if got := snapshot.Profiles[0].Note; got != "Plus trial ends soon" {
		t.Fatalf("snapshot note = %q, want %q", got, "Plus trial ends soon")
	}
}

func TestManagerSnapshotIgnoresMalformedNotesFile(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), authFixture("account-work", "api-work"))
	if err := os.WriteFile(paths.notesFile, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile malformed notes: %v", err)
	}

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	assertProfiles(t, snapshot.Profiles, []string{"work"})
	if got := snapshot.Profiles[0].Note; got != "" {
		t.Fatalf("snapshot note = %q, want empty", got)
	}
}

func TestManagerSetNoteRejectsLongValues(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), authFixture("account-work", "api-work"))

	err := m.SetNote("work", strings.Repeat("x", 256))
	if err == nil {
		t.Fatal("SetNote() error = nil, want length error")
	}
	if want := "profile note cannot exceed 255 characters"; err.Error() != want {
		t.Fatalf("SetNote() error = %q, want %q", err.Error(), want)
	}
}

func TestManagerRenameMovesNote(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "old"), authFixture("account-rename", "api-rename"))
	writeProfileNotesFile(t, paths.notesFile, map[string]string{"old": "tracked"})

	if err := m.Rename("old", "new", ""); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	assertProfiles(t, snapshot.Profiles, []string{"new"})
	if got := snapshot.Profiles[0].Note; got != "tracked" {
		t.Fatalf("renamed note = %q, want %q", got, "tracked")
	}
}

func TestManagerDeleteRemovesNote(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), authFixture("account-work", "api-work"))
	writeProfileNotesFile(t, paths.notesFile, map[string]string{"work": "tracked"})

	if err := m.Delete("work", ""); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	notes, err := readProfileNotes(paths.notesFile)
	if err != nil {
		t.Fatalf("readProfileNotes() error = %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("notes = %#v, want empty", notes)
	}
}

type testManagerPaths struct {
	authFile   string
	profileDir string
	legacyDir  string
	markerFile string
	notesFile  string
}

func newTestManager(t *testing.T) (Manager, testManagerPaths) {
	t.Helper()

	root := t.TempDir()
	paths := testManagerPaths{
		authFile:   filepath.Join(root, "auth.json"),
		profileDir: filepath.Join(root, "auth_manager", "profiles"),
		legacyDir:  filepath.Join(root, "auth_manager"),
		markerFile: filepath.Join(root, "auth_manager", CurrentProfileMarkerName),
		notesFile:  filepath.Join(root, "auth_manager", profileNotesFileName),
	}

	for _, dir := range []string{paths.profileDir, paths.legacyDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	return Manager{
		AuthFile:           paths.authFile,
		ProfileDir:         paths.profileDir,
		LegacyProfileDir:   paths.legacyDir,
		CurrentProfileFile: paths.markerFile,
		NotesFile:          paths.notesFile,
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

func realisticAuthFixture(accountID, accessToken, refreshToken, apiURL string) map[string]any {
	return map[string]any{
		"auth_mode": "account",
		"tokens": map[string]any{
			"access_token":  accessToken,
			"refresh_token": refreshToken,
			"account_id":    accountID,
			"expires_at":    "2026-04-17T13:34:56Z",
		},
		"client": map[string]any{
			"api_url": apiURL,
		},
	}
}

func writeAuthFile(t *testing.T, path string, body map[string]any) {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal auth fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func assertFilesEqual(t *testing.T, gotPath, wantPath string) {
	t.Helper()

	got, err := os.ReadFile(gotPath) // #nosec G304 -- test fixture path is created under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", gotPath, err)
	}
	want, err := os.ReadFile(wantPath) // #nosec G304 -- test fixture path is created under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", wantPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%q content = %s, want contents of %q: %s", gotPath, got, wantPath, want)
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

func writeProfileNotesFile(t *testing.T, path string, notes map[string]string) {
	t.Helper()

	data, err := json.Marshal(notes)
	if err != nil {
		t.Fatalf("Marshal notes fixture: %v", err)
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
	if err := os.MkdirAll(path, 0o700); err != nil {
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

func assertDirExists(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is a file, want directory", path)
	}
}

func assertProfiles(t *testing.T, got []ProfileSummary, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("profiles = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Fatalf("profiles = %#v, want %#v", got, want)
		}
	}
}
