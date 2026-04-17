package ui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	profilemgr "codex-manage/internal/profiles"
)

type mode int

const (
	modeNormal mode = iota
	modeInput
	modeConfirm
)

type action int

const (
	actionNone action = iota
	actionSave
	actionRename
	actionDelete
	actionLogout
)

type statusKind int

const (
	statusSuccess statusKind = iota
	statusInfo
)

type appModel struct {
	profileManager profilemgr.Manager

	profiles []string
	cursor   int

	currentProfile string
	authActive     bool

	width  int
	height int

	mode            mode
	pendingAction   action
	inputValue      string
	inputPrompt     string
	confirmPrompt   string
	status          string
	statusKind      statusKind
	errText         string
	restartRequired bool

	quitting bool
}

func Run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	m := newAppModel(home)
	if err := m.reload(); err != nil {
		return fmt.Errorf("failed to initialize: %w", err)
	}

	if _, err := tea.NewProgram(m).Run(); err != nil {
		return fmt.Errorf("application error: %w", err)
	}
	return nil
}

func newAppModel(home string) appModel {
	codexDir := filepath.Join(home, ".codex")
	return appModel{
		profileManager: profilemgr.NewManager(codexDir),
		status:         "Ready.",
	}
}

func (m appModel) Init() tea.Cmd {
	return nil
}

func (m *appModel) reload() error {
	snapshot, err := m.profileManager.Snapshot()
	if err != nil {
		return err
	}
	m.profiles = snapshot.Profiles

	if len(m.profiles) == 0 {
		m.cursor = 0
	} else if m.cursor >= len(m.profiles) {
		m.cursor = len(m.profiles) - 1
	}

	m.authActive = snapshot.AuthActive
	m.currentProfile = snapshot.CurrentProfile
	return nil
}

func (m *appModel) syncTrackedProfile() error {
	return m.profileManager.SyncTrackedProfile()
}

func (m *appModel) activateSelectedProfile(name string) error {
	if err := m.profileManager.Activate(name); err != nil {
		return err
	}
	return m.reload()
}

func (m *appModel) selectedProfile() string {
	if len(m.profiles) == 0 || m.cursor < 0 || m.cursor >= len(m.profiles) {
		return ""
	}
	return m.profiles[m.cursor]
}

func (m *appModel) setStatus(s string) {
	m.status = s
	m.statusKind = statusSuccess
	m.errText = ""
}

func (m *appModel) setInfo(s string) {
	m.status = s
	m.statusKind = statusInfo
	m.errText = ""
}

func (m *appModel) setError(s string) {
	m.errText = s
}

func (m *appModel) clearMessages() {
	m.status = ""
	m.statusKind = statusSuccess
	m.errText = ""
}

func (m *appModel) enterInput(nextAction action, prompt, value string) appModel {
	m.mode = modeInput
	m.pendingAction = nextAction
	m.inputPrompt = prompt
	m.inputValue = value
	m.confirmPrompt = ""
	m.clearMessages()
	return *m
}

func (m *appModel) enterConfirm(nextAction action, prompt string) appModel {
	m.mode = modeConfirm
	m.pendingAction = nextAction
	m.confirmPrompt = prompt
	m.inputPrompt = ""
	m.inputValue = ""
	m.clearMessages()
	return *m
}

func (m *appModel) cancelMode() appModel {
	m.mode = modeNormal
	m.pendingAction = actionNone
	m.inputPrompt = ""
	m.inputValue = ""
	m.confirmPrompt = ""
	m.setStatus("Cancelled.")
	return *m
}

func (m *appModel) exitMode() appModel {
	m.mode = modeNormal
	m.pendingAction = actionNone
	m.inputPrompt = ""
	m.inputValue = ""
	m.confirmPrompt = ""
	return *m
}

func (m *appModel) reloadAndExitWithError(err error) appModel {
	if reloadErr := m.reload(); reloadErr != nil {
		m.setError(reloadErr.Error())
	} else {
		m.setError(err.Error())
	}
	return m.exitMode()
}
