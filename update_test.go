package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
	if !errors.Is(fmt.Errorf("%w", profilemgr.ErrStateChanged), profilemgr.ErrStateChanged) {
		t.Fatal("errors.Is sanity check failed")
	}
	if got.errText == "" {
		t.Fatal("errText is empty, want propagated error")
	}
	if got.status != "stale" {
		t.Fatalf("status = %q, want previous status preserved", got.status)
	}
}
