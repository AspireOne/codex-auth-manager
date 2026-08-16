package profiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testProfileNameWork = "work"

func expectedChatGPTStorageKey(accountID string) string {
	return "chatgpt-" + hashString("chatgpt\x00"+accountID)
}

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

	err := m.SaveCurrent("")
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("SaveCurrent error = %v, want ErrStateChanged", err)
	}

	assertFileExists(t, filepath.Join(paths.profileDir, expectedChatGPTStorageKey("account-save")))
}

func TestManagerSaveCurrentRejectsDuplicateCredentialsUnderNewName(t *testing.T) {
	m, paths := newTestManager(t)
	auth := authFixture("account-duplicate", "api-duplicate")
	writeAuthFile(t, paths.authFile, auth)
	writeAuthFile(t, filepath.Join(paths.profileDir, "existing"), auth)

	err := m.SaveCurrent("")
	if err == nil {
		t.Fatal("SaveCurrent error = nil, want duplicate credentials error")
	}
	if !strings.Contains(err.Error(), "same auth already exists as profile") {
		t.Fatalf("SaveCurrent error = %q, want duplicate profile error", err.Error())
	}

	assertFileMissing(t, filepath.Join(paths.profileDir, "new-name"))
}

func TestManagerSaveCurrentAssignsStableInstallationID(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, paths.authFile, authFixture("account-save", "api-save"))

	if err := m.SaveCurrent(""); err != nil {
		t.Fatalf("SaveCurrent() error = %v", err)
	}
	key := expectedChatGPTStorageKey("account-save")

	firstID := assertInstallationIDMatchesProfile(t, m, paths, key)

	if err := m.SaveCurrent(""); err == nil {
		t.Fatal("SaveCurrent() error = nil, want duplicate credentials error")
	}

	if err := m.Activate(key); err != nil {
		t.Fatalf("Activate(%q) error = %v", key, err)
	}

	secondID := readInstallationIDFile(t, paths.installationIDFile)
	if secondID != firstID {
		t.Fatalf("installation_id = %q, want stable ID %q", secondID, firstID)
	}
}

func TestManagerSnapshotDerivesChatGPTEmailWithoutRenamingStorage(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "subjective-old-name"), chatGPTAuthFixture("acct-work", "person@example.com"))

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Profiles) != 1 {
		t.Fatalf("profiles = %#v, want one profile", snapshot.Profiles)
	}
	profile := snapshot.Profiles[0]
	if profile.Key != "subjective-old-name" || profile.Label != "person@example.com" || profile.Kind != AuthKindChatGPT {
		t.Fatalf("profile = %#v, want unchanged key with derived email label", profile)
	}
	assertFileExists(t, filepath.Join(paths.profileDir, "subjective-old-name"))
}

func TestManagerSnapshotFallsBackWhenChatGPTEmailIsUnavailable(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "legacy"), authFixture("acct-123456789", ""))

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got, want := snapshot.Profiles[0].Label, "ChatGPT account · 23456789"; got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
}

func TestEmailFromIDTokenRejectsTerminalControlCharacters(t *testing.T) {
	if got := emailFromIDToken(testIDToken("person\x1b[31m@example.com")); got != "" {
		t.Fatalf("emailFromIDToken() = %q, want empty unsafe label", got)
	}
}

func TestEmailFromIDTokenRejectsMalformedOrUnusableClaims(t *testing.T) {
	tooLong := strings.Repeat("x", 255)
	tests := []struct {
		name  string
		token string
	}{
		{name: "empty", token: ""},
		{name: "missing payload", token: "header"},
		{name: "invalid base64", token: "header.%%%.signature"},
		{name: "invalid JSON", token: "header." + base64.RawURLEncoding.EncodeToString([]byte("{")) + ".signature"},
		{name: "missing email", token: "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user"}`)) + ".signature"},
		{name: "empty email", token: testIDToken("   ")},
		{name: "overlong email", token: testIDToken(tooLong)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := emailFromIDToken(tt.token); got != "" {
				t.Fatalf("emailFromIDToken() = %q, want fallback", got)
			}
		})
	}
}

func TestValidateProfileLabelTrimsAndCountsUnicodeCharacters(t *testing.T) {
	label := strings.Repeat("ž", 64)
	got, err := validateProfileLabel("  " + label + "  ")
	if err != nil {
		t.Fatalf("validateProfileLabel(64 runes) error = %v", err)
	}
	if got != label {
		t.Fatalf("validated label = %q, want trimmed Unicode label", got)
	}
	if _, err := validateProfileLabel(label + "ž"); err == nil {
		t.Fatal("validateProfileLabel(65 runes) error = nil")
	}
}

func TestManagerAPIKeyLabelDefaultsToFingerprintAndCanBeEditedOrReset(t *testing.T) {
	m, paths := newTestManager(t)
	const key = "sk-test-secret"
	writeAuthFile(t, filepath.Join(paths.profileDir, "legacy-api-name"), apiKeyAuthFixture(key))

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	sum := sha256.Sum256([]byte(key))
	wantDefault := "API key · " + hex.EncodeToString(sum[:])[:8]
	if got := snapshot.Profiles[0]; got.Label != wantDefault || got.CustomLabel != "" || got.Kind != AuthKindAPIKey {
		t.Fatalf("profile = %#v, want default API fingerprint", got)
	}

	if err := m.SetLabel("legacy-api-name", "Personal project"); err != nil {
		t.Fatalf("SetLabel() error = %v", err)
	}
	snapshot, err = m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() after label error = %v", err)
	}
	if got := snapshot.Profiles[0]; got.Label != "Personal project" || got.CustomLabel != "Personal project" {
		t.Fatalf("profile = %#v, want custom API label", got)
	}

	if err := m.SetLabel("legacy-api-name", ""); err != nil {
		t.Fatalf("SetLabel(reset) error = %v", err)
	}
	snapshot, err = m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() after reset error = %v", err)
	}
	if got := snapshot.Profiles[0]; got.Label != wantDefault || got.CustomLabel != "" {
		t.Fatalf("profile = %#v, want reset API fingerprint", got)
	}
}

func TestManagerSnapshotDisambiguatesDuplicateLabelsStably(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "first"), chatGPTAuthFixture("acct-first", "same@example.com"))
	writeAuthFile(t, filepath.Join(paths.profileDir, "second"), chatGPTAuthFixture("acct-second", "same@example.com"))

	first, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	second, err := m.Snapshot()
	if err != nil {
		t.Fatalf("second Snapshot() error = %v", err)
	}
	if first.Profiles[0].Label == first.Profiles[1].Label {
		t.Fatalf("collision labels are equal: %#v", first.Profiles)
	}
	for i := range first.Profiles {
		if !strings.HasPrefix(first.Profiles[i].Label, "same@example.com · ") {
			t.Fatalf("label = %q, want collision suffix", first.Profiles[i].Label)
		}
		if first.Profiles[i].Label != second.Profiles[i].Label {
			t.Fatalf("label changed between snapshots: %q != %q", first.Profiles[i].Label, second.Profiles[i].Label)
		}
	}
}

func TestManagerSnapshotDisambiguatesLabelsCaseInsensitively(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "upper"), chatGPTAuthFixture("acct-upper", "Same@Example.com"))
	writeAuthFile(t, filepath.Join(paths.profileDir, "lower"), chatGPTAuthFixture("acct-lower", "same@example.com"))

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Profiles) != 2 {
		t.Fatalf("profiles = %#v, want two", snapshot.Profiles)
	}
	if strings.EqualFold(snapshot.Profiles[0].Label, snapshot.Profiles[1].Label) {
		t.Fatalf("effective labels still collide case-insensitively: %#v", snapshot.Profiles)
	}
}

func TestManagerSnapshotResolvesCollisionWithGeneratedEffectiveLabel(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "first"), chatGPTAuthFixture("acct-first", "shared@example.com"))
	writeAuthFile(t, filepath.Join(paths.profileDir, "second"), chatGPTAuthFixture("acct-second", "shared@example.com"))

	firstIdentity, err := readAuthIdentity(filepath.Join(paths.profileDir, "first"))
	if err != nil {
		t.Fatalf("readAuthIdentity(first) error = %v", err)
	}
	generatedLabel := "shared@example.com · " + shortHead(hashString(canonicalIdentity(firstIdentity)), 8)
	writeAuthFile(t, filepath.Join(paths.profileDir, "api"), apiKeyAuthFixture("sk-collision"))
	writeProfileMetadataFile(t, paths.metadataFile, map[string]profileMetadata{
		"api": {Label: generatedLabel},
	})

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	seen := make(map[string]struct{})
	for _, profile := range snapshot.Profiles {
		key := strings.ToLower(profile.Label)
		if _, exists := seen[key]; exists {
			t.Fatalf("duplicate effective label %q in %#v", profile.Label, snapshot.Profiles)
		}
		seen[key] = struct{}{}
	}
	if api := profileByKey(snapshot.Profiles, "api"); api == nil || api.Label == generatedLabel {
		t.Fatalf("API label = %#v, want globally disambiguated label", api)
	}
}

func TestManagerSaveCurrentUsesOpaqueKeyAndPersistsOptionalAPIKeyLabel(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, paths.authFile, apiKeyAuthFixture("sk-save-me"))
	descriptor, err := readAuthDescriptor(paths.authFile)
	if err != nil {
		t.Fatalf("readAuthDescriptor() error = %v", err)
	}
	wantKey := storageKeyForIdentity(descriptor)

	if err := m.SaveCurrent("Personal project"); err != nil {
		t.Fatalf("SaveCurrent() error = %v", err)
	}
	assertFileExists(t, filepath.Join(paths.profileDir, wantKey))
	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.CurrentProfileKey != wantKey || snapshot.CurrentAuth.Label != "Personal project" {
		t.Fatalf("snapshot = %#v, want saved API profile", snapshot)
	}
}

func TestManagerSaveCurrentRejectsRefreshedDuplicateIdentity(t *testing.T) {
	m, paths := newTestManager(t)
	existing := chatGPTAuthFixture("acct-same", "same@example.com")
	existing["updated"] = false
	current := chatGPTAuthFixture("acct-same", "same@example.com")
	current["updated"] = true
	writeAuthFile(t, filepath.Join(paths.profileDir, "existing"), existing)
	writeAuthFile(t, paths.authFile, current)

	err := m.SaveCurrent("")
	if err == nil || !strings.Contains(err.Error(), "same auth already exists as profile") {
		t.Fatalf("SaveCurrent() error = %v, want identity duplicate error", err)
	}
}

func TestManagerSetLabelRejectsChatGPTAndInvalidAPILabels(t *testing.T) {
	m, paths := newTestManager(t)
	writeAuthFile(t, filepath.Join(paths.profileDir, "chatgpt"), chatGPTAuthFixture("acct", "person@example.com"))
	writeAuthFile(t, filepath.Join(paths.profileDir, "apikey"), apiKeyAuthFixture("sk-test"))

	if err := m.SetLabel("chatgpt", "Work"); err == nil || !strings.Contains(err.Error(), "account email") {
		t.Fatalf("SetLabel(ChatGPT) error = %v, want account-email error", err)
	}
	if err := m.SetLabel("apikey", "line one\nline two"); err == nil || !strings.Contains(err.Error(), "single line") {
		t.Fatalf("SetLabel(multiline) error = %v, want single-line error", err)
	}
	if err := m.SetLabel("apikey", strings.Repeat("x", 65)); err == nil || !strings.Contains(err.Error(), "64 characters") {
		t.Fatalf("SetLabel(long) error = %v, want length error", err)
	}
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

func TestManagerSnapshotResolvesChatGPTProfileAcrossAuthModeChange(t *testing.T) {
	m, paths := newTestManager(t)
	saved := chatGPTAuthFixture("acct-same", "same@example.com")
	saved["auth_mode"] = "account"
	current := chatGPTAuthFixture("acct-same", "same@example.com")
	writeAuthFile(t, filepath.Join(paths.profileDir, "legacy"), saved)
	writeAuthFile(t, paths.authFile, current)

	snapshot, err := m.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.CurrentProfileKey != "legacy" {
		t.Fatalf("CurrentProfileKey = %q, want legacy", snapshot.CurrentProfileKey)
	}
	if snapshot.CurrentAuth.Label != "same@example.com" {
		t.Fatalf("CurrentAuth.Label = %q, want email label", snapshot.CurrentAuth.Label)
	}
}

func TestManagerSyncTrackedAPIKeyProfileAcrossAuthModeChange(t *testing.T) {
	m, paths := newTestManager(t)
	saved := apiKeyAuthFixture("sk-same")
	saved["auth_mode"] = ""
	current := apiKeyAuthFixture("sk-same")
	current["updated"] = true
	profilePath := filepath.Join(paths.profileDir, "legacy-api")
	writeAuthFile(t, profilePath, saved)
	writeAuthFile(t, paths.authFile, current)
	identity, err := readAuthIdentity(profilePath)
	if err != nil {
		t.Fatalf("readAuthIdentity() error = %v", err)
	}
	writeMarkerFile(t, paths.markerFile, currentProfileMarker{Name: "legacy-api", Identity: identity})

	if err := m.SyncTrackedProfile(); err != nil {
		t.Fatalf("SyncTrackedProfile() error = %v", err)
	}
	assertFilesEqual(t, profilePath, paths.authFile)
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

func TestManagerSnapshotSecuresExistingDirectoriesAndManagedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission bits")
	}
	m, paths := newTestManager(t)
	profilePath := filepath.Join(paths.profileDir, "world-readable-api")
	writeAuthFile(t, profilePath, apiKeyAuthFixture("sk-permissions"))
	writeAuthFile(t, paths.authFile, apiKeyAuthFixture("sk-permissions"))
	writeProfileMetadataFile(t, paths.metadataFile, map[string]profileMetadata{
		"world-readable-api": {Label: "Permissions test"},
	})
	for _, path := range []string{filepath.Dir(paths.profileDir), paths.profileDir, profilePath, paths.authFile, paths.metadataFile} {
		if err := os.Chmod(path, 0o777); err != nil {
			t.Fatalf("Chmod(%q): %v", path, err)
		}
	}

	if _, err := m.Snapshot(); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	for _, path := range []string{filepath.Dir(paths.profileDir), paths.profileDir} {
		assertFileMode(t, path, 0o700)
	}
	for _, path := range []string{profilePath, paths.authFile, paths.metadataFile, paths.markerFile, paths.installationIDFile} {
		assertFileMode(t, path, 0o600)
	}
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
	if snapshot.CurrentProfileKey != "" {
		t.Fatalf("Snapshot().CurrentProfileKey = %q, want empty", snapshot.CurrentProfileKey)
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

	if snapshot.CurrentProfileKey != "" {
		t.Fatalf("Snapshot().CurrentProfileKey = %q, want empty", snapshot.CurrentProfileKey)
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

	if snapshot.CurrentProfileKey != testProfileNameWork {
		t.Fatalf("Snapshot().CurrentProfileKey = %q, want work", snapshot.CurrentProfileKey)
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

	if snapshot.CurrentProfileKey != testProfileNameWork {
		t.Fatalf("Snapshot().CurrentProfileKey = %q, want work", snapshot.CurrentProfileKey)
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
	if runtime.GOOS != "windows" {
		info, err := os.Stat(paths.metadataFile)
		if err != nil {
			t.Fatalf("Stat metadata: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("metadata mode = %o, want 600", got)
		}
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

func chatGPTAuthFixture(accountID, email string) map[string]any {
	return map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"account_id": accountID,
			"id_token":   testIDToken(email),
		},
	}
}

func apiKeyAuthFixture(apiKey string) map[string]any {
	return map[string]any{
		"auth_mode":      "apikey",
		"OPENAI_API_KEY": apiKey,
	}
}

func testIDToken(email string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"email":%q}`, email)))
	return "header." + payload + ".signature"
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

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode of %q = %o, want %o", path, got, want)
	}
}

func assertProfiles(t *testing.T, got []ProfileSummary, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("profiles = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i].Key != want[i] {
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
