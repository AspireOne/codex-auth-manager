package profiles

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type recordingAuthTarget struct {
	synced     []AuthCredentials
	clearCalls int
	syncErr    error
	clearErr   error
}

func (t *recordingAuthTarget) Name() string {
	return "recording target"
}

func (t *recordingAuthTarget) Sync(credentials AuthCredentials) error {
	t.synced = append(t.synced, credentials)
	return t.syncErr
}

func (t *recordingAuthTarget) Clear() error {
	t.clearCalls++
	return t.clearErr
}

func TestManagerActivateProjectsOAuthCredentialsToConfiguredTargets(t *testing.T) {
	m, paths := newTestManager(t)
	target := &recordingAuthTarget{}
	m.targets = []AuthTarget{target}
	expiresAt := time.Unix(1_900_000_000, 0).UTC()
	accessToken := jwtFixture(expiresAt)
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), oauthAuthFixture("acct-work", accessToken, "refresh-work"))

	if err := m.Activate("work"); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	if len(target.synced) != 1 {
		t.Fatalf("target sync calls = %d, want 1", len(target.synced))
	}
	got := target.synced[0]
	if got.Kind != AuthKindChatGPT || got.AccountID != "acct-work" ||
		got.AccessToken != accessToken || got.RefreshToken != "refresh-work" ||
		!got.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("synced credentials = %#v, want ChatGPT OAuth material", got)
	}
}

func TestManagerActivateProjectsAPIKeyStateToConfiguredTargets(t *testing.T) {
	m, paths := newTestManager(t)
	target := &recordingAuthTarget{}
	m.targets = []AuthTarget{target}
	writeAuthFile(t, filepath.Join(paths.profileDir, "api"), apiKeyAuthFixture("sk-test"))

	if err := m.Activate("api"); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}

	if len(target.synced) != 1 || target.synced[0].Kind != AuthKindAPIKey || target.synced[0].APIKey != "sk-test" {
		t.Fatalf("synced credentials = %#v, want API-key state", target.synced)
	}
}

func TestManagerLogoutClearsConfiguredTargets(t *testing.T) {
	m, paths := newTestManager(t)
	target := &recordingAuthTarget{}
	m.targets = []AuthTarget{target}
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), authFixture("acct-work", "api-work"))
	writeAuthFile(t, paths.authFile, authFixture("acct-work", "api-work"))
	identity, err := readAuthIdentity(paths.authFile)
	if err != nil {
		t.Fatalf("readAuthIdentity() error = %v", err)
	}
	writeMarkerFile(t, paths.markerFile, currentProfileMarker{Name: "work", Identity: identity})

	if err := m.Logout(); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if target.clearCalls != 1 {
		t.Fatalf("target clear calls = %d, want 1", target.clearCalls)
	}
}

func TestManagerTargetFailureReportsStateChangedAfterActivation(t *testing.T) {
	m, paths := newTestManager(t)
	target := &recordingAuthTarget{syncErr: errors.New("target unavailable")}
	m.targets = []AuthTarget{target}
	writeAuthFile(t, filepath.Join(paths.profileDir, "work"), oauthAuthFixture("acct-work", jwtFixture(time.Unix(1_900_000_000, 0)), "refresh-work"))

	err := m.Activate("work")
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("Activate() error = %v, want ErrStateChanged", err)
	}
	if !errors.Is(err, ErrTargetSync) {
		t.Fatalf("Activate() error = %v, want ErrTargetSync", err)
	}
	if !fileExists(paths.authFile) {
		t.Fatal("active auth file is missing after target failure")
	}
}

func oauthAuthFixture(accountID, accessToken, refreshToken string) map[string]any {
	return map[string]any{
		"auth_mode": "account",
		"tokens": map[string]any{
			"account_id":    accountID,
			"id_token":      testIDToken(accountID + "@example.com"),
			"access_token":  accessToken,
			"refresh_token": refreshToken,
		},
	}
}

func jwtFixture(expiresAt time.Time) string {
	payload := `{"exp":` + strconv.FormatInt(expiresAt.Unix(), 10) + `}`
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
}
