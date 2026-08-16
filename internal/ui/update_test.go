package ui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	profilemgr "codex-manage/internal/profiles"
	"codex-manage/internal/profilestatus"
	"codex-manage/internal/updatecheck"
)

type fakeUIAuthenticator struct {
	run func(context.Context, profilemgr.ProfileSummary) error
}

type fakeStatusFetcher struct {
	run func(context.Context, profilemgr.ProfileSummary) (profilemgr.ProfileStatus, error)
}

func (f fakeStatusFetcher) Fetch(ctx context.Context, profile profilemgr.ProfileSummary) (profilemgr.ProfileStatus, error) {
	return f.run(ctx, profile)
}

func (a fakeUIAuthenticator) Reauthenticate(ctx context.Context, profile profilemgr.ProfileSummary) error {
	return a.run(ctx, profile)
}

const testWorkProfileName = "work"

func TestInitStartsUpdateCheckForReleaseBuilds(t *testing.T) {
	m := newAppModel(t.TempDir())
	m.appVersion = "v1.2.3"

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() = nil, want update-check command")
	}
}

func TestUpdateCheckMessageSetsPersistentNotice(t *testing.T) {
	m := appModel{}

	updatedModel, _ := m.Update(updateCheckMsg{
		result: updatecheck.Result{
			CurrentVersion:  "v1.2.3",
			LatestVersion:   "v1.3.0",
			Checked:         true,
			UpdateAvailable: true,
		},
	})
	got := updatedModel.(appModel)

	if got.updateNotice != "Update available: v1.3.0" {
		t.Fatalf("updateNotice = %q, want %q", got.updateNotice, "Update available: v1.3.0")
	}
	if !got.updateChecked {
		t.Fatal("updateChecked = false, want true after successful check")
	}
}

func TestSkippedUpdateCheckStaysNeutral(t *testing.T) {
	m := appModel{}

	updatedModel, _ := m.Update(updateCheckMsg{
		result: updatecheck.Result{
			CurrentVersion: "dev",
			Checked:        false,
		},
	})
	got := updatedModel.(appModel)

	if got.updateChecked {
		t.Fatal("updateChecked = true, want false when the checker skips the lookup")
	}

	view := fmt.Sprint(got.View())
	if !strings.Contains(view, "not checked") {
		t.Fatalf("View() missing neutral unchecked state after skipped check:\n%s", view)
	}
	if strings.Contains(view, "up to date") {
		t.Fatalf("View() incorrectly claims up to date after skipped check:\n%s", view)
	}
}

func TestBrowserStatusMessageDescribesBrowserOrUnavailable(t *testing.T) {
	m := newAppModel(t.TempDir())
	updatedModel, _ := m.Update(browserStatusMsg{name: "Brave"})
	updated := updatedModel.(appModel)
	if updated.browserAuthStatus != "Brave · isolated profiles" {
		t.Fatalf("browserAuthStatus = %q", updated.browserAuthStatus)
	}
	if header := stripANSI(updated.renderHeader()); !strings.Contains(header, "Browser auth:     Brave · isolated profiles") {
		t.Fatalf("header is missing browser status:\n%s", header)
	}

	updatedModel, _ = updated.Update(browserStatusMsg{err: errors.New("not found")})
	updated = updatedModel.(appModel)
	if updated.browserAuthStatus != "unavailable" {
		t.Fatalf("browserAuthStatus = %q, want unavailable", updated.browserAuthStatus)
	}
}

func TestRenderHeaderIncludesUpdateNotice(t *testing.T) {
	m := appModel{
		updateChecked: true,
		updateNotice:  "Update available: v1.3.0",
	}

	view := fmt.Sprint(m.View())
	for _, want := range []string{"Update:", "Update available: v1.3.0"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
}

func TestRenderHeaderDoesNotClaimUpToDateBeforeSuccessfulCheck(t *testing.T) {
	m := appModel{}

	view := fmt.Sprint(m.View())
	if !strings.Contains(view, "Update:") {
		t.Fatalf("View() missing update line:\n%s", view)
	}
	if !strings.Contains(view, "not checked") {
		t.Fatalf("View() missing neutral unchecked state:\n%s", view)
	}
	if strings.Contains(view, "up to date") {
		t.Fatalf("View() incorrectly claims up to date before a successful check:\n%s", view)
	}
}

func TestSuccessfulUpdateCheckWithoutUpdateRendersUpToDate(t *testing.T) {
	m := appModel{}

	updatedModel, _ := m.Update(updateCheckMsg{
		result: updatecheck.Result{
			CurrentVersion:  "v1.2.3",
			LatestVersion:   "v1.2.3",
			Checked:         true,
			UpdateAvailable: false,
		},
	})
	got := updatedModel.(appModel)

	if !got.updateChecked {
		t.Fatal("updateChecked = false, want true after successful check")
	}
	if got.updateNotice != "" {
		t.Fatalf("updateNotice = %q, want empty", got.updateNotice)
	}

	view := fmt.Sprint(got.View())
	if !strings.Contains(view, "up to date") {
		t.Fatalf("View() missing up-to-date state after successful check:\n%s", view)
	}
}

func TestSuccessfulNoUpdateClearsStaleNotice(t *testing.T) {
	m := appModel{
		updateChecked: true,
		updateNotice:  "Update available: v1.3.0",
	}

	updatedModel, _ := m.Update(updateCheckMsg{
		result: updatecheck.Result{
			CurrentVersion:  "v1.3.0",
			LatestVersion:   "v1.3.0",
			Checked:         true,
			UpdateAvailable: false,
		},
	})
	got := updatedModel.(appModel)

	if !got.updateChecked {
		t.Fatal("updateChecked = false, want true after a successful no-update check")
	}
	if got.updateNotice != "" {
		t.Fatalf("updateNotice = %q, want empty", got.updateNotice)
	}

	view := fmt.Sprint(got.View())
	if strings.Contains(view, "Update available: v1.3.0") {
		t.Fatalf("View() still shows stale notice:\n%s", view)
	}
	if !strings.Contains(view, "up to date") {
		t.Fatalf("View() missing up-to-date state after clearing stale notice:\n%s", view)
	}
}

func TestFailedUpdateCheckClearsStaleNoticeToNeutral(t *testing.T) {
	m := appModel{
		updateChecked: true,
		updateNotice:  "Update available: v1.3.0",
	}

	updatedModel, _ := m.Update(updateCheckMsg{
		err: fmt.Errorf("network unavailable"),
	})
	got := updatedModel.(appModel)

	if got.updateChecked {
		t.Fatal("updateChecked = true, want false after a failed check")
	}
	if got.updateNotice != "" {
		t.Fatalf("updateNotice = %q, want empty", got.updateNotice)
	}

	view := fmt.Sprint(got.View())
	if strings.Contains(view, "Update available: v1.3.0") {
		t.Fatalf("View() still shows stale notice after failed check:\n%s", view)
	}
	if !strings.Contains(view, "not checked") {
		t.Fatalf("View() missing neutral unchecked state after failed check:\n%s", view)
	}
}

func TestHandleActionErrorReloadsStateForErrStateChanged(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	authPath := filepath.Join(codexDir, "auth.json")
	profilePath := filepath.Join(codexDir, "auth_manager", "profiles", "saved")

	if err := os.MkdirAll(filepath.Dir(profilePath), 0o700); err != nil {
		t.Fatalf("MkdirAll profile dir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"acct\"}}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}
	if err := os.WriteFile(profilePath, []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"acct\"}}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile profile: %v", err)
	}

	m := newAppModel(home)
	m, _ = m.enterInput(actionSave, "Save:", "saved")
	m.status = "stale"
	m.authActive = false
	m.profiles = nil

	got := m.handleActionError(fmt.Errorf("%w: marker update failed", profilemgr.ErrStateChanged))

	if got.mode != modeNormal {
		t.Fatalf("mode = %v, want %v", got.mode, modeNormal)
	}
	if got.pendingAction != actionNone {
		t.Fatalf("pendingAction = %v, want %v", got.pendingAction, actionNone)
	}
	if !got.authActive {
		t.Fatalf("authActive = false, want true after reload")
	}
	if len(got.profiles) != 1 || got.profiles[0].Key != "saved" {
		t.Fatalf("profiles = %#v, want [\"saved\"]", got.profiles)
	}
	if got.errText == "" {
		t.Fatal("errText is empty, want propagated error")
	}
	if got.status != "stale" {
		t.Fatalf("status = %q, want previous status preserved", got.status)
	}
}

func TestRestartRequired(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	authPath := filepath.Join(codexDir, "auth.json")
	profileDir := filepath.Join(codexDir, "auth_manager", "profiles")
	profilePath := filepath.Join(profileDir, "test-profile")

	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll profile dir: %v", err)
	}
	if err := os.WriteFile(profilePath, []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"acct\"}}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile profile: %v", err)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if m.restartRequired {
		t.Error("restartRequired should be false initially")
	}

	// Mock activation
	// We need to trigger "enter" in updateNormal
	m.cursor = 0
	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	updatedModel, _ := m.Update(msg)
	m = updatedModel.(appModel)

	if !m.restartRequired {
		t.Error("restartRequired should be true after activating a profile")
	}

	// Reset and test logout
	m.restartRequired = false
	if err := os.WriteFile(authPath, []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"acct\"}}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Trigger "l" for logout
	msg = tea.KeyPressMsg(tea.Key{Text: "l"})
	updatedModel, _ = m.Update(msg)
	m = updatedModel.(appModel)

	// Now it should be in confirm mode
	if m.mode != modeConfirm || m.pendingAction != actionLogout {
		t.Fatalf("Expected confirm mode for logout, got mode=%v action=%v", m.mode, m.pendingAction)
	}

	// Trigger "y" for confirmation
	msg = tea.KeyPressMsg(tea.Key{Text: "y"})
	updatedModel, _ = m.Update(msg)
	m = updatedModel.(appModel)

	if !m.restartRequired {
		t.Error("restartRequired should be true after logout")
	}
}

func TestAuthenticateKeyStartsExplicitBusyState(t *testing.T) {
	m := appModel{
		profiles:      []profilemgr.ProfileSummary{{Key: "work", Label: "work@example.com", Kind: profilemgr.AuthKindChatGPT}},
		authenticator: fakeUIAuthenticator{run: func(context.Context, profilemgr.ProfileSummary) error { return nil }},
	}

	updatedModel, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "a"}))
	got := updatedModel.(appModel)
	if got.mode != modeAuthenticating || got.authProfileKey != "work" || cmd == nil {
		t.Fatalf("mode/key/cmd = %v/%q/%v, want authenticating work", got.mode, got.authProfileKey, cmd)
	}
	if !strings.Contains(got.status, "Complete the sign-in") || !strings.Contains(stripANSI(got.renderFooter()), "cancel authentication") {
		t.Fatalf("status/footer = %q/%q, want explicit waiting state", got.status, stripANSI(got.renderFooter()))
	}
}

func TestAuthenticateRejectsAPIKeyProfile(t *testing.T) {
	m := appModel{profiles: []profilemgr.ProfileSummary{{Key: "api", Label: "API", Kind: profilemgr.AuthKindAPIKey}}}
	updatedModel, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "a"}))
	got := updatedModel.(appModel)
	if cmd != nil || got.mode != modeNormal || !strings.Contains(got.status, "Only ChatGPT") {
		t.Fatalf("model/cmd = %#v/%v, want ChatGPT-only info", got, cmd)
	}
	if strings.Contains(stripANSI(got.renderFooter()), "authenticate") {
		t.Fatalf("API key footer unexpectedly offers authentication: %s", stripANSI(got.renderFooter()))
	}
}

func TestAuthenticateEscapeCancelsAndCompletionPreservesState(t *testing.T) {
	started := make(chan struct{})
	m := appModel{
		profiles: []profilemgr.ProfileSummary{{Key: "work", Label: "work@example.com", Kind: profilemgr.AuthKindChatGPT}},
		authenticator: fakeUIAuthenticator{run: func(ctx context.Context, _ profilemgr.ProfileSummary) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}},
	}
	updatedModel, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "a"}))
	m = updatedModel.(appModel)
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started

	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = updatedModel.(appModel)
	if m.mode != modeAuthenticating || m.status != "Cancelling authentication..." {
		t.Fatalf("mode/status = %v/%q, want cancelling state", m.mode, m.status)
	}
	updatedModel, _ = m.Update(<-result)
	m = updatedModel.(appModel)
	if m.mode != modeNormal || !strings.Contains(m.status, "No credentials changed") || m.errText != "" {
		t.Fatalf("model after cancellation = %#v", m)
	}
}

func TestAuthenticationCompletionReloadsAndRequiresRestart(t *testing.T) {
	home := t.TempDir()
	profilePath := filepath.Join(home, ".codex", "auth_manager", "profiles", "work")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, uiChatGPTAuth("acct-work", "work@example.com"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatal(err)
	}
	m.mode = modeAuthenticating
	m.authProfileKey = "work"

	updatedModel, _ := m.Update(authenticationFinishedMsg{profileKey: "work", label: "work@example.com"})
	got := updatedModel.(appModel)
	if got.mode != modeNormal || !got.restartRequired || !strings.Contains(got.status, "Authenticated and activated") {
		t.Fatalf("model after success = %#v", got)
	}
}

func TestDeleteConfirmationPromptText(t *testing.T) {
	tests := []struct {
		name              string
		profiles          []profilemgr.ProfileSummary
		cursor            int
		currentProfileKey string
		want              string
	}{
		{
			name:              "non-current profile",
			profiles:          profileViews(testWorkProfileName, "side"),
			cursor:            1,
			currentProfileKey: testWorkProfileName,
			want:              `Delete saved profile "side"? [y/N]`,
		},
		{
			name:              "current profile",
			profiles:          profileViews(testWorkProfileName, "side"),
			cursor:            0,
			currentProfileKey: testWorkProfileName,
			want:              fmt.Sprintf("Delete saved profile %q? Current login stays active. [y/N]", testWorkProfileName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := appModel{
				profiles:          tt.profiles,
				cursor:            tt.cursor,
				currentProfileKey: tt.currentProfileKey,
				authActive:        true,
			}

			updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "d"}))
			got := updatedModel.(appModel)

			if got.mode != modeConfirm {
				t.Fatalf("mode = %v, want %v", got.mode, modeConfirm)
			}
			if got.pendingAction != actionDelete {
				t.Fatalf("pendingAction = %v, want %v", got.pendingAction, actionDelete)
			}
			if got.confirmPrompt != tt.want {
				t.Fatalf("confirmPrompt = %q, want %q", got.confirmPrompt, tt.want)
			}
		})
	}
}

func TestLogoutConfirmationPromptText(t *testing.T) {
	m := appModel{authActive: true}

	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "l"}))
	got := updatedModel.(appModel)

	if got.mode != modeConfirm {
		t.Fatalf("mode = %v, want %v", got.mode, modeConfirm)
	}
	if got.pendingAction != actionLogout {
		t.Fatalf("pendingAction = %v, want %v", got.pendingAction, actionLogout)
	}
	want := "Remove active auth.json? Saved profiles stay untouched. [y/N]"
	if got.confirmPrompt != want {
		t.Fatalf("confirmPrompt = %q, want %q", got.confirmPrompt, want)
	}
}

func TestSelectingCurrentProfileShowsInfoStatus(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	authPath := filepath.Join(codexDir, "auth.json")
	profileDir := filepath.Join(codexDir, "auth_manager", "profiles")
	profilePath := filepath.Join(profileDir, testWorkProfileName)
	auth := []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"acct\"}}\n")

	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll profile dir: %v", err)
	}
	if err := os.WriteFile(authPath, auth, 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}
	if err := os.WriteFile(profilePath, auth, 0o600); err != nil {
		t.Fatalf("WriteFile profile: %v", err)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(m.profiles) != 1 || m.profiles[0].Key != testWorkProfileName {
		t.Fatalf("profiles = %#v, want [%q]", m.profiles, testWorkProfileName)
	}
	if m.currentProfileKey != testWorkProfileName {
		t.Fatalf("currentProfileKey = %q, want %q", m.currentProfileKey, testWorkProfileName)
	}
	m.cursor = 0

	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	updatedModel, _ := m.Update(msg)
	got := updatedModel.(appModel)

	wantStatus := `Profile "ChatGPT account · acct" is already active.`
	if got.status != wantStatus {
		t.Fatalf("status = %q, want already-active message", got.status)
	}
	if got.statusKind != statusInfo {
		t.Fatalf("statusKind = %v, want %v", got.statusKind, statusInfo)
	}
	if got.restartRequired {
		t.Fatal("restartRequired = true, want false when profile is already active")
	}
	if got.errText != "" {
		t.Fatalf("errText = %q, want empty", got.errText)
	}
}

func TestSavingCurrentProfileShowsInfoAndSyncsTrackedProfile(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	authPath := filepath.Join(codexDir, "auth.json")
	profileDir := filepath.Join(codexDir, "auth_manager", "profiles")
	profilePath := filepath.Join(profileDir, testWorkProfileName)
	auth := []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"acct\"},\"updated\":true}\n")
	profile := []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"acct\"},\"updated\":false}\n")

	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll profile dir: %v", err)
	}
	if err := os.WriteFile(authPath, auth, 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}
	if err := os.WriteFile(profilePath, profile, 0o600); err != nil {
		t.Fatalf("WriteFile profile: %v", err)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if m.currentProfileKey != testWorkProfileName {
		t.Fatalf("currentProfileKey = %q, want %q", m.currentProfileKey, testWorkProfileName)
	}

	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "s"}))
	got := updatedModel.(appModel)

	if got.mode != modeNormal {
		t.Fatalf("mode = %v, want %v", got.mode, modeNormal)
	}
	if got.pendingAction != actionNone {
		t.Fatalf("pendingAction = %v, want %v", got.pendingAction, actionNone)
	}
	wantStatus := `"ChatGPT account · acct" is already saved.`
	if got.status != wantStatus {
		t.Fatalf("status = %q, want already-saved message", got.status)
	}
	if got.statusKind != statusInfo {
		t.Fatalf("statusKind = %v, want %v", got.statusKind, statusInfo)
	}
	if got.errText != "" {
		t.Fatalf("errText = %q, want empty", got.errText)
	}
	gotProfile := readTestFile(t, profilePath)
	if string(gotProfile) != string(auth) {
		t.Fatalf("profile = %q, want synced auth", gotProfile)
	}
}

func TestSavingUnsavedChatGPTAuthIsImmediateAndUsesEmailLabel(t *testing.T) {
	home := t.TempDir()
	authPath := filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatalf("MkdirAll auth dir: %v", err)
	}
	if err := os.WriteFile(authPath, uiChatGPTAuth("acct-new", "person@example.com"), 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := stripANSI(m.renderHeader()); !strings.Contains(got, "person@example.com (unsaved)") {
		t.Fatalf("header missing unsaved email label:\n%s", got)
	}

	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "s"}))
	got := updatedModel.(appModel)
	if got.mode != modeNormal || got.currentProfileKey == "" {
		t.Fatalf("model = %#v, want immediately saved profile", got)
	}
	if !strings.HasPrefix(got.currentProfileKey, "chatgpt-") {
		t.Fatalf("currentProfileKey = %q, want opaque ChatGPT key", got.currentProfileKey)
	}
	if got.status != `Saved current auth as "person@example.com".` {
		t.Fatalf("status = %q, want email save status", got.status)
	}
}

func TestSavingAPIKeyPromptsForOptionalLabel(t *testing.T) {
	home := t.TempDir()
	authPath := filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatalf("MkdirAll auth dir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-ui-test"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "s"}))
	got := updatedModel.(appModel)
	if got.mode != modeInput || got.pendingAction != actionSave {
		t.Fatalf("mode/action = %v/%v, want API save input", got.mode, got.pendingAction)
	}
	if !strings.Contains(got.textInput.Prompt, "optional") {
		t.Fatalf("prompt = %q, want optional-label guidance", got.textInput.Prompt)
	}

	updatedModel, _ = got.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got = updatedModel.(appModel)
	if got.currentProfileKey == "" || !strings.HasPrefix(got.currentAuth.Label, "API key · ") {
		t.Fatalf("model = %#v, want fingerprint-labeled saved API key", got)
	}
}

func TestRelabelingIsAvailableOnlyForAPIKeys(t *testing.T) {
	m := appModel{
		mode:      modeNormal,
		textInput: newTextInput(),
		profiles: []profilemgr.ProfileSummary{
			{Key: "chat", Label: "person@example.com", Kind: profilemgr.AuthKindChatGPT},
			{Key: "api", Label: "API key · 12345678", Kind: profilemgr.AuthKindAPIKey},
		},
	}

	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "r"}))
	got := updatedModel.(appModel)
	if got.mode != modeNormal || got.statusKind != statusInfo || !strings.Contains(got.status, "account email") {
		t.Fatalf("ChatGPT relabel result = %#v, want informational no-op", got)
	}

	m.cursor = 1
	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "r"}))
	got = updatedModel.(appModel)
	if got.mode != modeInput || got.pendingAction != actionEditLabel {
		t.Fatalf("API relabel mode/action = %v/%v, want edit-label input", got.mode, got.pendingAction)
	}
}

func TestActivatingFromCustomAuthPromptsWithoutOverwritingAuth(t *testing.T) {
	m, authPath, customAuth, _ := setupCustomAuthActivationTest(t)

	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	updatedModel, _ := m.Update(msg)
	got := updatedModel.(appModel)

	if got.mode != modeConfirm {
		t.Fatalf("mode = %v, want %v", got.mode, modeConfirm)
	}
	if got.pendingAction != actionActivate {
		t.Fatalf("pendingAction = %v, want %v", got.pendingAction, actionActivate)
	}
	wantPrompt := `Current auth is not saved as a profile. Replace it with "ChatGPT account · work"? [y/N]`
	if got.confirmPrompt != wantPrompt {
		t.Fatalf("confirmPrompt = %q, want %q", got.confirmPrompt, wantPrompt)
	}
	if got.restartRequired {
		t.Fatal("restartRequired = true, want false before confirmation")
	}
	gotAuth := readTestFile(t, authPath)
	if string(gotAuth) != string(customAuth) {
		t.Fatalf("auth.json = %q, want custom auth unchanged", gotAuth)
	}
}

func TestConfirmingCustomAuthActivationActivatesProfile(t *testing.T) {
	m, authPath, _, workAuth := setupCustomAuthActivationTest(t)

	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = updatedModel.(appModel)
	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "y"}))
	got := updatedModel.(appModel)

	if got.mode != modeNormal {
		t.Fatalf("mode = %v, want %v", got.mode, modeNormal)
	}
	if got.pendingAction != actionNone {
		t.Fatalf("pendingAction = %v, want %v", got.pendingAction, actionNone)
	}
	if got.currentProfileKey != testWorkProfileName {
		t.Fatalf("currentProfileKey = %q, want %q", got.currentProfileKey, testWorkProfileName)
	}
	if !got.restartRequired {
		t.Fatal("restartRequired = false, want true after activation")
	}
	wantStatus := `Activated profile "ChatGPT account · work".`
	if got.status != wantStatus {
		t.Fatalf("status = %q, want activated status", got.status)
	}
	gotAuth := readTestFile(t, authPath)
	if string(gotAuth) != string(workAuth) {
		t.Fatalf("auth.json = %q, want work profile auth", gotAuth)
	}
}

func TestCancellingCustomAuthActivationDoesNotActivateProfile(t *testing.T) {
	tests := []struct {
		name string
		key  tea.Key
	}{
		{name: "enter", key: tea.Key{Code: tea.KeyEnter}},
		{name: "n", key: tea.Key{Text: "n"}},
		{name: "esc", key: tea.Key{Code: tea.KeyEsc}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, authPath, customAuth, _ := setupCustomAuthActivationTest(t)

			updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
			m = updatedModel.(appModel)
			updatedModel, _ = m.Update(tea.KeyPressMsg(tt.key))
			got := updatedModel.(appModel)

			if got.mode != modeNormal {
				t.Fatalf("mode = %v, want %v", got.mode, modeNormal)
			}
			if got.pendingAction != actionNone {
				t.Fatalf("pendingAction = %v, want %v", got.pendingAction, actionNone)
			}
			if got.currentProfileKey != "" {
				t.Fatalf("currentProfileKey = %q, want empty", got.currentProfileKey)
			}
			if got.restartRequired {
				t.Fatal("restartRequired = true, want false after cancel")
			}
			gotAuth := readTestFile(t, authPath)
			if string(gotAuth) != string(customAuth) {
				t.Fatalf("auth.json = %q, want custom auth unchanged", gotAuth)
			}
		})
	}
}

func TestInvalidProfileDiagnosticsAreRendered(t *testing.T) {
	home := t.TempDir()
	profileDir := filepath.Join(home, ".codex", "auth_manager", "profiles")

	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll profile dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "corrupt"), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt profile: %v", err)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	view := fmt.Sprint(m.View())
	for _, want := range []string{
		"Ignored 1 invalid profile file(s):",
		"corrupt",
		"invalid JSON",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
}

func setupCustomAuthActivationTest(t *testing.T) (appModel, string, []byte, []byte) {
	t.Helper()

	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	authPath := filepath.Join(codexDir, "auth.json")
	profileDir := filepath.Join(codexDir, "auth_manager", "profiles")
	profilePath := filepath.Join(profileDir, testWorkProfileName)
	customAuth := []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"custom\"}}\n")
	workAuth := []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"work\"}}\n")

	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll profile dir: %v", err)
	}
	if err := os.WriteFile(authPath, customAuth, 0o600); err != nil {
		t.Fatalf("WriteFile auth: %v", err)
	}
	if err := os.WriteFile(profilePath, workAuth, 0o600); err != nil {
		t.Fatalf("WriteFile profile: %v", err)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !m.authActive {
		t.Fatal("authActive = false, want true")
	}
	if m.currentProfileKey != "" {
		t.Fatalf("currentProfileKey = %q, want custom/unsaved", m.currentProfileKey)
	}
	if len(m.profiles) != 1 || m.profiles[0].Key != testWorkProfileName {
		t.Fatalf("profiles = %#v, want [%q]", m.profiles, testWorkProfileName)
	}
	m.cursor = 0
	return m, authPath, customAuth, workAuth
}

func TestEditingProfileNoteUpdatesStatusAndView(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	profileDir := filepath.Join(codexDir, "auth_manager", "profiles")
	notesPath := filepath.Join(codexDir, "auth_manager", ".profile-notes.json")
	profilePath := filepath.Join(profileDir, testWorkProfileName)

	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll profile dir: %v", err)
	}
	if err := os.WriteFile(profilePath, []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"acct\"}}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile profile: %v", err)
	}
	if err := os.WriteFile(notesPath, []byte("{\"work\":\"old note\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile notes: %v", err)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	m.cursor = 0

	updatedModel, cmd := m.Update(tea.KeyPressMsg(tea.Key{Text: "n"}))
	m = updatedModel.(appModel)
	if m.mode != modeInput || m.pendingAction != actionEditNote {
		t.Fatalf("mode=%v pendingAction=%v, want note input mode", m.mode, m.pendingAction)
	}
	if got := m.textInput.Value(); got != "old note" {
		t.Fatalf("input value = %q, want existing note", got)
	}
	if !m.textInput.Focused() {
		t.Fatal("text input is not focused")
	}
	if cmd == nil {
		t.Fatal("entering input mode returned no cursor focus command")
	}
	if got, want := m.textInput.Position(), len([]rune("old note")); got != want {
		t.Fatalf("cursor position = %d, want %d", got, want)
	}

	m.textInput.SetValue("updated note")
	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := updatedModel.(appModel)

	if got.status != `Updated note for "ChatGPT account · acct".` {
		t.Fatalf("status = %q, want updated-note message", got.status)
	}
	if got.profiles[0].Note != "updated note" {
		t.Fatalf("note = %q, want %q", got.profiles[0].Note, "updated note")
	}
	if got.textInput.Focused() || got.textInput.Value() != "" || got.textInput.Prompt != "" {
		t.Fatalf("text input was not reset after submit: focused=%v value=%q prompt=%q", got.textInput.Focused(), got.textInput.Value(), got.textInput.Prompt)
	}
	view := fmt.Sprint(got.View())
	if !strings.Contains(view, "updated note") {
		t.Fatalf("View() missing updated note:\n%s", view)
	}
}

func TestRemovingProfileNoteClearsIt(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	profileDir := filepath.Join(codexDir, "auth_manager", "profiles")
	notesPath := filepath.Join(codexDir, "auth_manager", ".profile-notes.json")
	profilePath := filepath.Join(profileDir, testWorkProfileName)

	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll profile dir: %v", err)
	}
	if err := os.WriteFile(profilePath, []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"acct\"}}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile profile: %v", err)
	}
	if err := os.WriteFile(notesPath, []byte("{\"work\":\"old note\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile notes: %v", err)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	m.cursor = 0
	m, _ = m.enterInput(actionEditNote, "Edit note:", "")

	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := updatedModel.(appModel)

	if got.status != `Removed note for "ChatGPT account · acct".` {
		t.Fatalf("status = %q, want removed-note message", got.status)
	}
	if got.profiles[0].Note != "" {
		t.Fatalf("note = %q, want empty", got.profiles[0].Note)
	}
}

func TestInputModeAcceptsSpaceAndPrintableText(t *testing.T) {
	m := newInputModeModel(actionEditNote, "old")

	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: " "}))
	m = updatedModel.(appModel)
	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "note"}))
	got := updatedModel.(appModel)

	if value := got.textInput.Value(); value != "old note" {
		t.Fatalf("input value = %q, want %q", value, "old note")
	}
}

func TestInputModeEditsAtCursor(t *testing.T) {
	m := newInputModeModel(actionEditNote, "old note")

	for range 4 {
		updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
		m = updatedModel.(appModel)
	}
	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "new "}))
	m = updatedModel.(appModel)

	if got := m.textInput.Value(); got != "old new note" {
		t.Fatalf("input value after insertion = %q, want %q", got, "old new note")
	}

	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	m = updatedModel.(appModel)
	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete}))
	got := updatedModel.(appModel)

	if value := got.textInput.Value(); value != "old new nte" {
		t.Fatalf("input value after navigation and delete = %q, want %q", value, "old new nte")
	}
}

func TestInputModeSupportsHomeEndAndBackspace(t *testing.T) {
	m := newInputModeModel(actionEditNote, "old note")

	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	m = updatedModel.(appModel)
	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete}))
	m = updatedModel.(appModel)
	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	m = updatedModel.(appModel)
	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	got := updatedModel.(appModel)

	if value := got.textInput.Value(); value != "ld not" {
		t.Fatalf("input value = %q, want %q", value, "ld not")
	}
}

func TestInputModeNavigatesUnicodeText(t *testing.T) {
	m := newInputModeModel(actionEditNote, "café")

	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	m = updatedModel.(appModel)
	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	m = updatedModel.(appModel)
	updatedModel, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "ff"}))
	got := updatedModel.(appModel)

	if value := got.textInput.Value(); value != "caffé" {
		t.Fatalf("input value = %q, want %q", value, "caffé")
	}
}

func TestCancellingInputModeResetsTextInput(t *testing.T) {
	m := newInputModeModel(actionEditNote, "unsaved note")

	updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	got := updatedModel.(appModel)

	if got.mode != modeNormal || got.pendingAction != actionNone {
		t.Fatalf("mode=%v pendingAction=%v, want normal mode", got.mode, got.pendingAction)
	}
	if got.textInput.Focused() || got.textInput.Value() != "" || got.textInput.Prompt != "" {
		t.Fatalf("text input was not reset after cancel: focused=%v value=%q prompt=%q", got.textInput.Focused(), got.textInput.Value(), got.textInput.Prompt)
	}
	if got.status != "Cancelled." {
		t.Fatalf("status = %q, want %q", got.status, "Cancelled.")
	}
}

func TestInputModeInsertsBracketedPasteAtCursor(t *testing.T) {
	m := newInputModeModel(actionEditNote, "old")

	for range 2 {
		updatedModel, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
		m = updatedModel.(appModel)
	}

	updatedModel, _ := m.Update(tea.PasteMsg{Content: " note\tfrom\r\npaste"})
	got := updatedModel.(appModel)

	if value := got.textInput.Value(); value != "o note from  pasteld" {
		t.Fatalf("input value = %q, want %q", value, "o note from  pasteld")
	}
}

func TestInputModeResizesAvailableTextWidth(t *testing.T) {
	m := newAppModel(t.TempDir())
	m.width = 50
	m, _ = m.enterInput(actionEditNote, "Edit note:", "old note")

	want := 50 - baseStyle.GetHorizontalFrameSize() - lipgloss.Width(m.textInput.Prompt)
	if got := m.textInput.Width(); got != want {
		t.Fatalf("input width = %d, want %d", got, want)
	}

	updatedModel, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	got := updatedModel.(appModel)
	want = 30 - baseStyle.GetHorizontalFrameSize() - lipgloss.Width(got.textInput.Prompt)
	if gotWidth := got.textInput.Width(); gotWidth != want {
		t.Fatalf("resized input width = %d, want %d", gotWidth, want)
	}
}

func TestRenderProfileLinePlacesNoteInlineAndWrapsContinuation(t *testing.T) {
	m := appModel{
		width: 44,
		profiles: []profilemgr.ProfileSummary{
			{Key: "work", Label: "work"},
		},
	}

	line := m.renderProfileLine(
		itemStyle,
		"  ",
		"work",
		"this note should wrap onto another line cleanly",
		profilemgr.PlanFree,
		false,
		m.profileColumnWidth(),
	)

	parts := strings.Split(line, "\n")
	if len(parts) < 2 {
		t.Fatalf("rendered line did not wrap:\n%s", line)
	}
	if !strings.Contains(parts[0], "work") || !strings.Contains(parts[0], "this note") {
		t.Fatalf("rendered first line missing inline note:\n%s", line)
	}
}

func TestRenderChatGPTStatusUsesAlignedSeparatedColumns(t *testing.T) {
	firstPercent, secondPercent := 1, 100
	firstReset := time.Date(2026, 8, 16, 15, 30, 0, 0, time.Local)
	secondReset := time.Date(2026, 9, 2, 1, 2, 0, 0, time.Local)
	m := appModel{
		width: 200,
		profiles: []profilemgr.ProfileSummary{
			{Key: "short", Label: "a@example.com", Kind: profilemgr.AuthKindChatGPT, Plan: profilemgr.PlanFree},
			{Key: "long", Label: "longer@example.com", Kind: profilemgr.AuthKindChatGPT, Plan: profilemgr.PlanPlus},
		},
		profileStatuses: map[string]profileStatusView{
			"short": {status: &profilemgr.ProfileStatus{AuthStatus: profilemgr.ProfileAuthAuthenticated, UsedPercent: &firstPercent, ResetsAt: &firstReset}, phase: statusCached},
			"long":  {status: &profilemgr.ProfileStatus{AuthStatus: profilemgr.ProfileAuthSignInRequired, UsedPercent: &secondPercent, ResetsAt: &secondReset}, phase: statusCached},
		},
	}

	columnWidth := m.profileColumnWidth()
	firstLine := stripANSI(m.renderProfileLineWithStatus(itemStyle, "  ", m.profiles[0], false, columnWidth))
	secondLine := stripANSI(m.renderProfileLineWithStatus(itemStyle, "  ", m.profiles[1], false, columnWidth))
	if strings.Count(firstLine, "│") != 4 || strings.Count(secondLine, "│") != 4 {
		t.Fatalf("rows do not have distinct columns:\n%s\n%s", firstLine, secondLine)
	}
	if !strings.Contains(firstLine, "99%") || !strings.Contains(secondLine, "0%") {
		t.Fatalf("remaining percentages are incorrect:\n%s\n%s", firstLine, secondLine)
	}
	if firstAuth, secondAuth := strings.Index(firstLine, "✓"), strings.Index(secondLine, "✗"); firstAuth != secondAuth {
		t.Fatalf("authentication columns are not aligned: first=%d second=%d\n%s\n%s", firstAuth, secondAuth, firstLine, secondLine)
	}
}

func TestRenderListAlignsNotesWithCompactCurrentIndicator(t *testing.T) {
	m := appModel{
		width:             80,
		cursor:            0,
		currentProfileKey: "work",
		profiles: []profilemgr.ProfileSummary{
			{Key: "work", Label: "work", Note: "current note", Plan: profilemgr.PlanPro},
			{Key: "personal", Label: "personal", Note: "other note", Plan: profilemgr.PlanFree},
		},
	}

	rendered := m.renderList()
	lines := strings.Split(rendered, "\n")

	var currentLine string
	var otherLine string
	for _, line := range lines {
		if strings.Contains(line, "current note") {
			currentLine = line
		}
		if strings.Contains(line, "other note") {
			otherLine = line
		}
	}

	if currentLine == "" || otherLine == "" {
		t.Fatalf("rendered list missing expected notes:\n%s", rendered)
	}

	currentPlain := stripANSI(currentLine)
	otherPlain := stripANSI(otherLine)
	currentNoteIdx := strings.Index(currentPlain, "current note")
	otherNoteIdx := strings.Index(otherPlain, "other note")
	if currentNoteIdx < 0 || otherNoteIdx < 0 {
		t.Fatalf("rendered list missing expected note positions:\n%s", rendered)
	}
	currentIdx := lipgloss.Width(currentPlain[:currentNoteIdx])
	otherIdx := lipgloss.Width(otherPlain[:otherNoteIdx])
	if currentIdx != otherIdx {
		t.Fatalf("notes start at different columns: current=%d other=%d\n%s", currentIdx, otherIdx, rendered)
	}
	if strings.Contains(currentPlain, "• current") {
		t.Fatalf("current row still renders verbose label:\n%s", currentPlain)
	}
}

func TestReloadSortsProfilesByPlanThenName(t *testing.T) {
	home := t.TempDir()
	writeUIProfile(t, home, "z-pro", "acct-pro")
	writeUIProfile(t, home, "z-plus", "acct-plus-z")
	writeUIProfile(t, home, "a-plus", "acct-plus-a")
	writeUIProfile(t, home, "a-free", "acct-free")
	writeUIAPIProfile(t, home, "api-free")
	writeUIMetadata(t, home, `{
  "z-pro": {"plan": "pro"},
  "z-plus": {"plan": "plus"},
  "a-plus": {"plan": "plus"}
}`)

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	want := []string{"z-pro", "a-plus", "z-plus", "a-free", "api-free"}
	for i, name := range want {
		if got := m.profiles[i].Key; got != name {
			t.Fatalf("profiles[%d].Key = %q, want %q; profiles=%#v", i, got, name, m.profiles)
		}
	}
}

func TestCyclePlanReordersAndKeepsSelectedProfile(t *testing.T) {
	home := t.TempDir()
	writeUIProfile(t, home, "a-free", "acct-free")
	writeUIProfile(t, home, "z-plus", "acct-plus")
	writeUIMetadata(t, home, `{"z-plus":{"plan":"plus"}}`)

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	m.cursor = indexOfProfile(m.profiles, "a-free")

	for _, want := range []struct {
		plan   profilemgr.Plan
		cursor int
	}{
		{profilemgr.PlanPlus, 0},
		{profilemgr.PlanPro, 0},
		{profilemgr.PlanFree, 1},
	} {
		updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "p"}))
		m = updated.(appModel)
		if got := m.selectedProfile(); got.Key != "a-free" || got.Plan != want.plan {
			t.Fatalf("selected profile = %#v, want a-free with plan %q", got, want.plan)
		}
		if m.cursor != want.cursor {
			t.Fatalf("cursor = %d, want %d after cycling to %q", m.cursor, want.cursor, want.plan)
		}
	}
}

func TestRenderPlanLabelsUseDistinctStyles(t *testing.T) {
	rendered := map[profilemgr.Plan]string{
		profilemgr.PlanFree: renderPlan(profilemgr.PlanFree),
		profilemgr.PlanPlus: renderPlan(profilemgr.PlanPlus),
		profilemgr.PlanPro:  renderPlan(profilemgr.PlanPro),
	}
	for plan, want := range map[profilemgr.Plan]string{
		profilemgr.PlanFree: "Free",
		profilemgr.PlanPlus: "Plus",
		profilemgr.PlanPro:  "Pro",
	} {
		if got := strings.TrimSpace(stripANSI(rendered[plan])); got != want {
			t.Fatalf("renderPlan(%q) = %q, want %q", plan, got, want)
		}
	}
	if rendered[profilemgr.PlanFree] == rendered[profilemgr.PlanPlus] || rendered[profilemgr.PlanPlus] == rendered[profilemgr.PlanPro] {
		t.Fatalf("plan styles are not distinct: %#v", rendered)
	}
}

func TestFooterIncludesCyclePlanHint(t *testing.T) {
	footer := stripANSI(appModel{mode: modeNormal}.renderFooter())
	if !strings.Contains(footer, "p cycle plan") {
		t.Fatalf("footer missing plan hint: %q", footer)
	}
	for _, line := range strings.Split(footer, "\n") {
		if got := lipgloss.Width(line); got > 80 {
			t.Fatalf("footer line width = %d, want at most 80: %q", got, line)
		}
	}
}

func TestStatusSchedulerUsesFreshCacheAndLimitsConcurrency(t *testing.T) {
	home := t.TempDir()
	for index, name := range []string{"one@example.com", "two@example.com", "three@example.com"} {
		writeUIProfile(t, home, name, fmt.Sprintf("acct-%d", index))
	}
	m := newAppModel(home)
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	if err := m.reload(); err != nil {
		t.Fatal(err)
	}
	percent := 20
	reset := now.Add(time.Hour)
	if err := m.profileManager.SaveProfileStatus(m.profiles[0].Key, profilemgr.ProfileStatus{
		FetchedAt: now.Add(-time.Minute), AuthStatus: profilemgr.ProfileAuthAuthenticated,
		UsedPercent: &percent, ResetsAt: &reset,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.reload(); err != nil {
		t.Fatal(err)
	}
	if len(m.statusQueue) != 2 {
		t.Fatalf("status queue length = %d, want 2 stale/missing profiles", len(m.statusQueue))
	}
	m.statusFetcher = fakeStatusFetcher{run: func(ctx context.Context, _ profilemgr.ProfileSummary) (profilemgr.ProfileStatus, error) {
		<-ctx.Done()
		return profilemgr.ProfileStatus{}, ctx.Err()
	}}
	if cmd := m.dispatchStatusFetches(); cmd == nil {
		t.Fatal("dispatchStatusFetches() = nil")
	}
	if len(m.statusCancels) != 2 {
		t.Fatalf("in-flight count = %d, want 2", len(m.statusCancels))
	}
	m.cancelStatusFetches()
}

func TestRenderChatGPTStatusStatesAndLeavesAPIKeysUnchanged(t *testing.T) {
	percent := 57
	reset := time.Date(2026, 8, 16, 15, 30, 0, 0, time.Local)
	m := appModel{
		width: 200,
		profiles: []profilemgr.ProfileSummary{
			{Key: "chat", Label: "chat@example.com", Kind: profilemgr.AuthKindChatGPT, Plan: profilemgr.PlanPlus},
			{Key: "api", Label: "API", Kind: profilemgr.AuthKindAPIKey, Plan: profilemgr.PlanFree},
		},
		profileStatuses: map[string]profileStatusView{
			"chat": {status: &profilemgr.ProfileStatus{AuthStatus: profilemgr.ProfileAuthAuthenticated, UsedPercent: &percent, ResetsAt: &reset}, phase: statusCached},
		},
	}
	rendered := stripANSI(m.renderList())
	for _, want := range []string{"Profile", "Plan", "Rem.", "Resets at", "Auth", "Cache", "Note", "43%", "16.08. 15:30", "✓", "Cached"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered list missing %q:\n%s", want, rendered)
		}
	}
	apiLine := ""
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "API") {
			apiLine = line
		}
	}
	if strings.Contains(apiLine, "%") || strings.Contains(apiLine, "Cached") {
		t.Fatalf("API-key row contains ChatGPT status: %q", apiLine)
	}
}

func TestRenderEveryChatGPTStatusState(t *testing.T) {
	percent := 57
	reset := time.Date(2026, 8, 16, 15, 30, 0, 0, time.Local)
	cached := &profilemgr.ProfileStatus{AuthStatus: profilemgr.ProfileAuthAuthenticated, UsedPercent: &percent, ResetsAt: &reset}
	signIn := &profilemgr.ProfileStatus{AuthStatus: profilemgr.ProfileAuthSignInRequired}
	tests := []struct {
		name string
		view profileStatusView
		want []string
	}{
		{name: "fresh cache", view: profileStatusView{status: cached, phase: statusCached}, want: []string{"43%", "✓", "Cached"}},
		{name: "expired cache loading", view: profileStatusView{status: cached, phase: statusLoading, stale: true}, want: []string{"43%", "✓", "Loading"}},
		{name: "missing cache loading", view: profileStatusView{phase: statusLoading}, want: []string{"-", "?", "Loading"}},
		{name: "sign in required", view: profileStatusView{status: signIn, phase: statusCached}, want: []string{"-", "✗", "Cached"}},
		{name: "cached failure", view: profileStatusView{status: cached, phase: statusFailed, stale: true}, want: []string{"43%", "✓", "Cached · failed"}},
		{name: "uncached failure", view: profileStatusView{phase: statusFailed}, want: []string{"-", "?", "Unavailable · failed"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := appModel{profileStatuses: map[string]profileStatusView{"chat": test.view}}
			got := m.renderProfileStatus("chat")
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Fatalf("renderProfileStatus() = %q, want %q", got, want)
				}
			}
		})
	}
}

func TestStatusTTLBoundaryAndAPIKeyExclusion(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		age       time.Duration
		wantQueue int
	}{
		{name: "just fresh", age: profileStatusTTL - time.Nanosecond, wantQueue: 0},
		{name: "exactly expired", age: profileStatusTTL, wantQueue: 1},
		{name: "older", age: profileStatusTTL + time.Minute, wantQueue: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			writeUIProfile(t, home, "chat@example.com", "acct")
			manager := profilemgr.NewManager(filepath.Join(home, ".codex"))
			snapshot, err := manager.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			percent := 10
			reset := now.Add(time.Hour)
			if err := manager.SaveProfileStatus(snapshot.Profiles[0].Key, profilemgr.ProfileStatus{
				FetchedAt: now.Add(-test.age), AuthStatus: profilemgr.ProfileAuthAuthenticated,
				UsedPercent: &percent, ResetsAt: &reset,
			}); err != nil {
				t.Fatal(err)
			}
			m := newAppModel(home)
			m.now = func() time.Time { return now }
			if err := m.reload(); err != nil {
				t.Fatal(err)
			}
			if got := len(m.statusQueue); got != test.wantQueue {
				t.Fatalf("queue length = %d, want %d", got, test.wantQueue)
			}
		})
	}

	home := t.TempDir()
	apiPath := filepath.Join(home, ".codex", "auth_manager", "profiles", "api")
	if err := os.MkdirAll(filepath.Dir(apiPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apiPath, []byte(`{"OPENAI_API_KEY":"sk-test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatal(err)
	}
	if len(m.statusQueue) != 0 || len(m.profileStatuses) != 0 {
		t.Fatalf("API-key status state = queue %v, statuses %#v", m.statusQueue, m.profileStatuses)
	}
}

func TestReloadRejectsVersionOneSignInCacheAndSchedulesRefetch(t *testing.T) {
	home := t.TempDir()
	writeUIProfile(t, home, "chat@example.com", "acct")
	cachePath := filepath.Join(home, ".codex", "auth_manager", ".profile-status-cache.json")
	legacy := `{"version":1,"profiles":{"chat@example.com":{"fetched_at":"2026-08-16T12:00:00Z","auth_status":"sign_in_required"}}}`
	if err := os.WriteFile(cachePath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatal(err)
	}
	view := m.profileStatuses["chat@example.com"]
	if view.status != nil || view.phase != statusLoading || len(m.statusQueue) != 1 {
		t.Fatalf("legacy cache was trusted: view=%#v queue=%v", view, m.statusQueue)
	}
}

func TestStatusSchedulerProgressesQueueAndIgnoresLateEpoch(t *testing.T) {
	profiles := []profilemgr.ProfileSummary{
		{Key: "one", Kind: profilemgr.AuthKindChatGPT},
		{Key: "two", Kind: profilemgr.AuthKindChatGPT},
		{Key: "three", Kind: profilemgr.AuthKindChatGPT},
	}
	m := appModel{
		profiles: profiles, statusFetcher: fakeStatusFetcher{run: func(context.Context, profilemgr.ProfileSummary) (profilemgr.ProfileStatus, error) {
			return profilemgr.ProfileStatus{}, errors.New("unused")
		}},
		profileStatuses: map[string]profileStatusView{"one": {}, "two": {}, "three": {}},
		statusQueue:     []string{"one", "two", "three"}, statusCancels: map[string]context.CancelFunc{},
		now: time.Now,
	}
	if cmd := m.dispatchStatusFetches(); cmd == nil || len(m.statusCancels) != 2 || len(m.statusQueue) != 1 {
		t.Fatalf("initial dispatch = cmd %v, in-flight %d, queued %d", cmd, len(m.statusCancels), len(m.statusQueue))
	}
	updated, _ := m.handleStatusFetchFinished(statusFetchFinishedMsg{profileKey: "one", epoch: m.statusEpoch, err: errors.New("network")})
	m = updated.(appModel)
	if len(m.statusCancels) != 2 || len(m.statusQueue) != 0 {
		t.Fatalf("progressed scheduler = in-flight %d, queued %d", len(m.statusCancels), len(m.statusQueue))
	}
	before := m.profileStatuses["two"]
	updated, cmd := m.handleStatusFetchFinished(statusFetchFinishedMsg{profileKey: "two", epoch: m.statusEpoch - 1, err: errors.New("late")})
	got := updated.(appModel)
	if cmd != nil || got.profileStatuses["two"] != before {
		t.Fatalf("late result changed state: before %#v, after %#v", before, got.profileStatuses["two"])
	}
	m.cancelStatusFetches()
}

func TestStatusCacheWriteFailureKeepsOnlyActuallyCachedValues(t *testing.T) {
	home := t.TempDir()
	writeUIProfile(t, home, "chat@example.com", "acct")
	m := newAppModel(home)
	m.now = time.Now
	if err := m.reload(); err != nil {
		t.Fatal(err)
	}
	key := m.profiles[0].Key
	m.statusQueue = nil
	if err := os.MkdirAll(m.profileManager.StatusCacheFile, 0o700); err != nil {
		t.Fatal(err)
	}
	percent := 20
	reset := time.Now().Add(time.Hour)
	updated, _ := m.handleStatusFetchFinished(statusFetchFinishedMsg{
		profileKey: key, epoch: m.statusEpoch,
		status: profilemgr.ProfileStatus{FetchedAt: time.Now(), AuthStatus: profilemgr.ProfileAuthAuthenticated, UsedPercent: &percent, ResetsAt: &reset},
	})
	got := updated.(appModel).profileStatuses[key]
	if got.phase != statusFailed || got.status != nil {
		t.Fatalf("uncached write failure = %#v, want unavailable failure", got)
	}

	oldPercent := 5
	oldReset := reset.Add(time.Hour)
	old := &profilemgr.ProfileStatus{FetchedAt: time.Now().Add(-time.Hour), AuthStatus: profilemgr.ProfileAuthAuthenticated, UsedPercent: &oldPercent, ResetsAt: &oldReset}
	m.profileStatuses[key] = profileStatusView{status: old, phase: statusLoading, stale: true}
	updated, _ = m.handleStatusFetchFinished(statusFetchFinishedMsg{profileKey: key, epoch: m.statusEpoch, err: profilestatus.ErrSignInRequired})
	got = updated.(appModel).profileStatuses[key]
	if got.phase != statusFailed || got.status != old || !got.stale {
		t.Fatalf("cached write failure = %#v, want retained stale cache", got)
	}
}

func TestAuthenticationPausesCancelsAndResumesStatusQueue(t *testing.T) {
	home := t.TempDir()
	writeUIProfile(t, home, "one@example.com", "one")
	writeUIProfile(t, home, "two@example.com", "two")
	m := newAppModel(home)
	if err := m.reload(); err != nil {
		t.Fatal(err)
	}
	m.authenticator = fakeUIAuthenticator{run: func(context.Context, profilemgr.ProfileSummary) error { return context.Canceled }}
	if cmd := m.dispatchStatusFetches(); cmd == nil || len(m.statusCancels) != 2 {
		t.Fatalf("initial status dispatch = cmd %v, in-flight %d", cmd, len(m.statusCancels))
	}
	previousEpoch := m.statusEpoch
	started, authCmd := m.startAuthentication()
	m = started.(appModel)
	if authCmd == nil || !m.statusPaused || len(m.statusCancels) != 0 || m.statusEpoch != previousEpoch+1 {
		t.Fatalf("authentication did not pause/cancel statuses: paused=%v in-flight=%d epoch=%d", m.statusPaused, len(m.statusCancels), m.statusEpoch)
	}
	resumed, statusCmd := m.Update(authenticationFinishedMsg{profileKey: m.authProfileKey, label: m.selectedProfile().Label, err: context.Canceled})
	m = resumed.(appModel)
	if statusCmd == nil || m.statusPaused || len(m.statusCancels) != 2 || m.mode != modeNormal {
		t.Fatalf("authentication cancellation did not resume statuses: paused=%v in-flight=%d mode=%v cmd=%v", m.statusPaused, len(m.statusCancels), m.mode, statusCmd)
	}
	m.cancelStatusFetches()
}

func TestChatGPTStatusUsesMutedStaleStyleAndFourSpaceContinuation(t *testing.T) {
	percent := 57
	reset := time.Date(2026, 8, 16, 15, 30, 0, 0, time.Local)
	status := &profilemgr.ProfileStatus{AuthStatus: profilemgr.ProfileAuthAuthenticated, UsedPercent: &percent, ResetsAt: &reset}
	profile := profilemgr.ProfileSummary{Key: "chat", Label: "chat@example.com", Kind: profilemgr.AuthKindChatGPT, Note: "a custom note"}
	fresh := appModel{width: 54, profiles: []profilemgr.ProfileSummary{profile}, profileStatuses: map[string]profileStatusView{
		"chat": {status: status, phase: statusCached},
	}}
	stale := fresh
	stale.profileStatuses = map[string]profileStatusView{"chat": {status: status, phase: statusLoading, stale: true}}
	freshLine := fresh.renderProfileLineWithStatus(itemStyle, "  ", profile, false, fresh.profileColumnWidth())
	staleLine := stale.renderProfileLineWithStatus(itemStyle, "  ", profile, false, stale.profileColumnWidth())
	if freshLine == staleLine {
		t.Fatal("fresh and stale status styling are identical")
	}
	parts := strings.Split(stripANSI(staleLine), "\n")
	if len(parts) < 2 {
		t.Fatalf("narrow status did not wrap:\n%s", staleLine)
	}
	for _, continuation := range parts[1:] {
		if !strings.HasPrefix(continuation, "    ") {
			t.Fatalf("continuation is not four-space indented: %q", continuation)
		}
	}
}

func profileViews(names ...string) []profilemgr.ProfileSummary {
	profiles := make([]profilemgr.ProfileSummary, len(names))
	for i, name := range names {
		profiles[i] = profilemgr.ProfileSummary{Key: name, Label: name, Kind: profilemgr.AuthKindChatGPT}
	}
	return profiles
}

func newInputModeModel(nextAction action, value string) appModel {
	m := appModel{textInput: newTextInput()}
	m, _ = m.enterInput(nextAction, "Input:", value)
	return m
}

func writeUIProfile(t *testing.T, home, name, accountID string) {
	t.Helper()
	path := filepath.Join(home, ".codex", "auth_manager", "profiles", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll profile dir: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"email":%q}`, name)))
	body := fmt.Sprintf(`{"auth_mode":"account","tokens":{"account_id":%q,"id_token":%q}}`, accountID, "header."+payload+".signature")
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile profile: %v", err)
	}
}

func writeUIAPIProfile(t *testing.T, home, name string) {
	t.Helper()
	path := filepath.Join(home, ".codex", "auth_manager", "profiles", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll profile dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-ui-test"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile profile: %v", err)
	}
}

func uiChatGPTAuth(accountID, email string) []byte {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"email":%q}`, email)))
	body := fmt.Sprintf(`{"auth_mode":"chatgpt","tokens":{"account_id":%q,"id_token":%q}}`, accountID, "header."+payload+".signature")
	return []byte(body + "\n")
}

func writeUIMetadata(t *testing.T, home, body string) {
	t.Helper()
	path := filepath.Join(home, ".codex", "auth_manager", ".profile-metadata.json")
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile metadata: %v", err)
	}
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()

	// Test-controlled temp path.
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return data
}

func stripANSI(s string) string {
	t := ansiPattern.ReplaceAllString(s, "")
	return strings.ReplaceAll(t, "\u001b[m", "")
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)
