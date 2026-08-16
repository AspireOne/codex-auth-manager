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

const testProfileNameWork = "work"

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

	assertInstallationIDMatchesProfile(t, m, paths, profileName)
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

func TestManagerSaveCurrentAssignsStableInstallationID(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, paths.authFile, authFixture("account-save", "api-save"))

	if err := m.SaveCurrent("saved"); err != nil {
		t.Fatalf("SaveCurrent() error = %v", err)
	}

	firstID := assertInstallationIDMatchesProfile(t, m, paths, "saved")

	if err := m.SaveCurrent("saved-again"); err == nil {
		t.Fatal("SaveCurrent(saved-again) error = nil, want duplicate credentials error")
	}

	if err := m.Activate("saved"); err != nil {
		t.Fatalf("Activate(saved) error = %v", err)
	}

	secondID := readInstallationIDFile(t, paths.installationIDFile)
	if secondID != firstID {
		t.Fatalf("installation_id = %q, want stable ID %q", secondID, firstID)
	}
}

func TestManagerRenameReturnsErrStateChangedAfterProfileIsRenamed(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "old"), authFixture("account-rename", "api-rename"))
	writeMarkerFile(t, paths.markerFile, currentProfileMarker{
		Name:     "old",
		Identity: authIdentity{AuthMode: "account", AccountID: "account-rename"},
	})
	writeInstallationIDsFile(t, paths.installationIDsFile, map[string]string{"old": testInstallationID(1)})
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
	writeInstallationIDsFile(t, paths.installationIDsFile, map[string]string{"current": testInstallationID(1)})
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
	profileName := testProfileNameWork
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

	assertInstallationIDMatchesProfile(t, m, paths, profileName)
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

func TestManagerSnapshotClearsInstallationIDWhenAuthIsUnsaved(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, paths.authFile, authFixture("account-custom", "api-custom"))
	writeInstallationIDFile(t, paths.installationIDFile, testInstallationID(1))

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	if snapshot.CurrentProfile != "" {
		t.Fatalf("Snapshot().CurrentProfile = %q, want empty", snapshot.CurrentProfile)
	}
	assertFileMissing(t, paths.installationIDFile)
}

func TestManagerSnapshotRepairsActiveInstallationIDForTrackedProfile(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, testProfileNameWork), authFixture("account-work", "api-work"))
	writeAuthFile(t, paths.authFile, authFixture("account-work", "api-work"))
	writeMarkerFile(t, paths.markerFile, currentProfileMarker{
		Name:     testProfileNameWork,
		Identity: authIdentity{AuthMode: "account", AccountID: "account-work"},
	})
	writeInstallationIDsFile(t, paths.installationIDsFile, map[string]string{testProfileNameWork: testInstallationID(2)})
	writeInstallationIDFile(t, paths.installationIDFile, testInstallationID(1))

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	if snapshot.CurrentProfile != testProfileNameWork {
		t.Fatalf("Snapshot().CurrentProfile = %q, want work", snapshot.CurrentProfile)
	}
	if got := readInstallationIDFile(t, paths.installationIDFile); got != testInstallationID(2) {
		t.Fatalf("installation_id = %q, want %q", got, testInstallationID(2))
	}
}

func TestManagerSnapshotIgnoresMalformedInstallationIDsFile(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, testProfileNameWork), authFixture("account-work", "api-work"))
	writeAuthFile(t, paths.authFile, authFixture("account-work", "api-work"))
	writeMarkerFile(t, paths.markerFile, currentProfileMarker{
		Name:     testProfileNameWork,
		Identity: authIdentity{AuthMode: "account", AccountID: "account-work"},
	})
	if err := os.WriteFile(paths.installationIDsFile, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile malformed installation IDs: %v", err)
	}

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	if snapshot.CurrentProfile != testProfileNameWork {
		t.Fatalf("Snapshot().CurrentProfile = %q, want work", snapshot.CurrentProfile)
	}
	assertInstallationIDMatchesProfile(t, m, paths, testProfileNameWork)
}

func TestManagerActivateRecoversFromMalformedInstallationIDsFile(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, testProfileNameWork), authFixture("account-work", "api-work"))
	if err := os.WriteFile(paths.installationIDsFile, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile malformed installation IDs: %v", err)
	}

	if err := m.Activate(testProfileNameWork); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	assertInstallationIDMatchesProfile(t, m, paths, testProfileNameWork)
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
	if got := snapshot.Profiles[0].Plan; got != PlanFree {
		t.Fatalf("snapshot plan = %q, want %q", got, PlanFree)
	}
}

func TestManagerSnapshotMigratesLegacyNotes(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), authFixture("account-work", "api-work"))
	writeProfileNotesFile(t, paths.legacyNotesFile, map[string]string{"work": "Plus trial ends soon"})

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}

	assertProfiles(t, snapshot.Profiles, []string{"work"})
	if got := snapshot.Profiles[0].Note; got != "Plus trial ends soon" {
		t.Fatalf("snapshot note = %q, want migrated note", got)
	}
	if got := snapshot.Profiles[0].Plan; got != PlanFree {
		t.Fatalf("snapshot plan = %q, want %q", got, PlanFree)
	}
	assertFileMissing(t, paths.legacyNotesFile)
	assertFileExists(t, paths.metadataFile)
	info, err := os.Stat(paths.metadataFile)
	if err != nil {
		t.Fatalf("Stat metadata: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("metadata mode = %o, want 600", got)
	}
}

func TestManagerSnapshotPreservesMalformedLegacyNotes(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), authFixture("account-work", "api-work"))
	if err := os.WriteFile(paths.legacyNotesFile, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile malformed notes: %v", err)
	}

	_, err := m.Snapshot()
	if err == nil || !strings.Contains(err.Error(), "failed to migrate profile notes") {
		t.Fatalf("Snapshot() error = %v, want migration error", err)
	}
	assertFileExists(t, paths.legacyNotesFile)
	assertFileMissing(t, paths.metadataFile)
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

func TestManagerSetPlanPersistsAndPreservesNote(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), authFixture("account-work", "api-work"))
	if err := m.SetNote("work", "ends soon"); err != nil {
		t.Fatalf("SetNote() error = %v", err)
	}
	if err := m.SetPlan("work", PlanPlus); err != nil {
		t.Fatalf("SetPlan() error = %v", err)
	}

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got := snapshot.Profiles[0]; got.Note != "ends soon" || got.Plan != PlanPlus {
		t.Fatalf("profile = %#v, want preserved note and Plus plan", got)
	}

	if err := m.SetNote("work", "updated"); err != nil {
		t.Fatalf("SetNote() error = %v", err)
	}
	snapshot, err = m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got := snapshot.Profiles[0]; got.Note != "updated" || got.Plan != PlanPlus {
		t.Fatalf("profile = %#v, want updated note and preserved Plus plan", got)
	}

	if err := m.SetNote("work", ""); err != nil {
		t.Fatalf("SetNote(clear) error = %v", err)
	}
	snapshot, err = m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got := snapshot.Profiles[0]; got.Note != "" || got.Plan != PlanPlus {
		t.Fatalf("profile = %#v, want cleared note and preserved Plus plan", got)
	}
}

func TestManagerSetPlanRejectsInvalidValue(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), authFixture("account-work", "api-work"))

	err := m.SetPlan("work", Plan("enterprise"))
	if err == nil || !strings.Contains(err.Error(), "invalid profile plan") {
		t.Fatalf("SetPlan() error = %v, want invalid plan error", err)
	}
}

func TestManagerSnapshotRejectsMalformedMetadataWithoutRemovingLegacyNotes(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), authFixture("account-work", "api-work"))
	if err := os.WriteFile(paths.metadataFile, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile malformed metadata: %v", err)
	}
	writeProfileNotesFile(t, paths.legacyNotesFile, map[string]string{"work": "keep me"})

	_, err := m.Snapshot()
	if err == nil || !strings.Contains(err.Error(), "failed to parse profile metadata") {
		t.Fatalf("Snapshot() error = %v, want metadata parse error", err)
	}
	assertFileExists(t, paths.metadataFile)
	assertFileExists(t, paths.legacyNotesFile)
}

func TestManagerRenameMovesMetadata(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "old"), authFixture("account-rename", "api-rename"))
	writeProfileMetadataFile(t, paths.metadataFile, map[string]profileMetadata{
		"old": {Note: "tracked", Plan: PlanPro},
	})
	writeInstallationIDsFile(t, paths.installationIDsFile, map[string]string{"old": testInstallationID(1)})

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
	if got := snapshot.Profiles[0].Plan; got != PlanPro {
		t.Fatalf("renamed plan = %q, want %q", got, PlanPro)
	}
	ids := readInstallationIDsFile(t, paths.installationIDsFile)
	if got := ids["new"]; got != testInstallationID(1) {
		t.Fatalf("installation ID for new = %q, want %q", got, testInstallationID(1))
	}
	if _, ok := ids["old"]; ok {
		t.Fatalf("installation IDs still contain old key: %#v", ids)
	}
}

func TestManagerDeleteRemovesMetadata(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), authFixture("account-work", "api-work"))
	writeProfileMetadataFile(t, paths.metadataFile, map[string]profileMetadata{
		"work": {Note: "tracked", Plan: PlanPlus},
	})
	writeInstallationIDsFile(t, paths.installationIDsFile, map[string]string{"work": testInstallationID(1)})

	if err := m.Delete("work", ""); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	assertFileMissing(t, paths.metadataFile)
	assertFileMissing(t, paths.installationIDsFile)
}

type testManagerPaths struct {
	authFile            string
	installationIDFile  string
	profileDir          string
	markerFile          string
	metadataFile        string
	legacyNotesFile     string
	installationIDsFile string
}

func newTestManager(t *testing.T) (Manager, testManagerPaths) {
	t.Helper()

	root := t.TempDir()
	paths := testManagerPaths{
		authFile:            filepath.Join(root, "auth.json"),
		installationIDFile:  filepath.Join(root, "installation_id"),
		profileDir:          filepath.Join(root, "auth_manager", "profiles"),
		markerFile:          filepath.Join(root, "auth_manager", CurrentProfileMarkerName),
		metadataFile:        filepath.Join(root, "auth_manager", profileMetadataFileName),
		legacyNotesFile:     filepath.Join(root, "auth_manager", profileNotesFileName),
		installationIDsFile: filepath.Join(root, "auth_manager", profileInstallationIDsFileName),
	}

	for _, dir := range []string{paths.profileDir, filepath.Dir(paths.markerFile)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	return Manager{
		AuthFile:            paths.authFile,
		InstallationIDFile:  paths.installationIDFile,
		ProfileDir:          paths.profileDir,
		CurrentProfileFile:  paths.markerFile,
		MetadataFile:        paths.metadataFile,
		LegacyNotesFile:     paths.legacyNotesFile,
		InstallationIDsFile: paths.installationIDsFile,
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

func writeProfileMetadataFile(t *testing.T, path string, metadata map[string]profileMetadata) {
	t.Helper()

	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Marshal metadata fixture: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func writeInstallationIDsFile(t *testing.T, path string, ids map[string]string) {
	t.Helper()

	data, err := json.Marshal(ids)
	if err != nil {
		t.Fatalf("Marshal installation IDs fixture: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func writeInstallationIDFile(t *testing.T, path, value string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(strings.TrimSpace(value)+"\n"), 0o600); err != nil {
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

func readInstallationIDFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture path is created under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return strings.TrimSpace(string(data))
}

func readInstallationIDsFile(t *testing.T, path string) map[string]string {
	t.Helper()

	data, err := os.ReadFile(path) // #nosec G304 -- test fixture path is created under t.TempDir.
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	var ids map[string]string
	if err := json.Unmarshal(data, &ids); err != nil {
		t.Fatalf("Unmarshal(%q): %v", path, err)
	}
	return ids
}

func assertInstallationIDMatchesProfile(t *testing.T, m Manager, paths testManagerPaths, profileName string) string {
	t.Helper()

	id := readInstallationIDFile(t, paths.installationIDFile)
	if err := validateInstallationID(id); err != nil {
		t.Fatalf("installation_id = %q, want UUID v4: %v", id, err)
	}
	ids := readInstallationIDsFile(t, paths.installationIDsFile)
	if got := ids[profileName]; got != id {
		t.Fatalf("installation IDs[%q] = %q, want %q", profileName, got, id)
	}
	if _, err := m.ensureProfileInstallationID(profileName); err != nil {
		t.Fatalf("ensureProfileInstallationID(%q): %v", profileName, err)
	}
	return id
}

func testInstallationID(n int) string {
	return []string{
		"00000000-0000-4000-8000-000000000001",
		"00000000-0000-4000-8000-000000000002",
	}[n-1]
}
