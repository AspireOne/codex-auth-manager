package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	profilemgr "codex-manage/internal/profiles"
)

const testWorkProfileName = "work"

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
	m.mode = modeInput
	m.pendingAction = actionSave
	m.inputPrompt = "Save:"
	m.inputValue = "saved"
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
	if len(got.profiles) != 1 || got.profiles[0] != "saved" {
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

func TestDeleteConfirmationPromptText(t *testing.T) {
	tests := []struct {
		name           string
		profiles       []string
		cursor         int
		currentProfile string
		want           string
	}{
		{
			name:           "non-current profile",
			profiles:       []string{testWorkProfileName, "side"},
			cursor:         1,
			currentProfile: testWorkProfileName,
			want:           `Delete saved profile "side"? [y/N]`,
		},
		{
			name:           "current profile",
			profiles:       []string{testWorkProfileName, "side"},
			cursor:         0,
			currentProfile: testWorkProfileName,
			want:           fmt.Sprintf("Delete saved profile %q? Current login stays active. [y/N]", testWorkProfileName),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := appModel{
				profiles:       tt.profiles,
				cursor:         tt.cursor,
				currentProfile: tt.currentProfile,
				authActive:     true,
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
	if len(m.profiles) != 1 || m.profiles[0] != testWorkProfileName {
		t.Fatalf("profiles = %#v, want [%q]", m.profiles, testWorkProfileName)
	}
	if m.currentProfile != testWorkProfileName {
		t.Fatalf("currentProfile = %q, want %q", m.currentProfile, testWorkProfileName)
	}
	m.cursor = 0

	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	updatedModel, _ := m.Update(msg)
	got := updatedModel.(appModel)

	wantStatus := fmt.Sprintf("Profile %q is already active.", testWorkProfileName)
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
	wantPrompt := fmt.Sprintf("Current auth is not saved as a profile. Replace it with %q? [y/N]", testWorkProfileName)
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
	if got.currentProfile != testWorkProfileName {
		t.Fatalf("currentProfile = %q, want %q", got.currentProfile, testWorkProfileName)
	}
	if !got.restartRequired {
		t.Fatal("restartRequired = false, want true after activation")
	}
	wantStatus := fmt.Sprintf("Activated profile %q.", testWorkProfileName)
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
			if got.currentProfile != "" {
				t.Fatalf("currentProfile = %q, want empty", got.currentProfile)
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
	if m.currentProfile != "" {
		t.Fatalf("currentProfile = %q, want custom/unsaved", m.currentProfile)
	}
	if len(m.profiles) != 1 || m.profiles[0] != testWorkProfileName {
		t.Fatalf("profiles = %#v, want [%q]", m.profiles, testWorkProfileName)
	}
	m.cursor = 0
	return m, authPath, customAuth, workAuth
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
