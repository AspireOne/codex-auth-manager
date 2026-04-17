package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	profilemgr "codex-manage/internal/profiles"
)

func TestHandleActionErrorReloadsStateForErrStateChanged(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	authPath := filepath.Join(codexDir, "auth.json")
	profilePath := filepath.Join(codexDir, "auth_manager", "profiles", "saved")

	if err := os.MkdirAll(filepath.Dir(profilePath), 0o755); err != nil {
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

	if err := os.MkdirAll(profileDir, 0o755); err != nil {
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

func TestSelectingCurrentProfileShowsInfoStatus(t *testing.T) {
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex")
	authPath := filepath.Join(codexDir, "auth.json")
	profileDir := filepath.Join(codexDir, "auth_manager", "profiles")
	profilePath := filepath.Join(profileDir, "work")
	auth := []byte("{\"auth_mode\":\"account\",\"tokens\":{\"account_id\":\"acct\"}}\n")

	if err := os.MkdirAll(profileDir, 0o755); err != nil {
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
	if len(m.profiles) != 1 || m.profiles[0] != "work" {
		t.Fatalf("profiles = %#v, want [\"work\"]", m.profiles)
	}
	if m.currentProfile != "work" {
		t.Fatalf("currentProfile = %q, want %q", m.currentProfile, "work")
	}
	m.cursor = 0

	msg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	updatedModel, _ := m.Update(msg)
	got := updatedModel.(appModel)

	if got.status != `Profile "work" is already active.` {
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
