package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
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

const currentProfileMarkerName = "current-profile"

type appModel struct {
	codexDir           string
	authFile           string
	profileDir         string
	legacyProfileDir   string
	currentProfileFile string

	profiles []string
	cursor   int

	currentProfile string
	authActive     bool

	width  int
	height int

	mode          mode
	pendingAction action
	inputValue    string
	inputPrompt   string
	confirmPrompt string
	status        string
	errText       string

	quitting bool
}

func newAppModel(home string) appModel {
	codexDir := filepath.Join(home, ".codex")
	managerDir := filepath.Join(codexDir, "auth_manager")
	return appModel{
		codexDir:           codexDir,
		authFile:           filepath.Join(codexDir, "auth.json"),
		profileDir:         filepath.Join(managerDir, "profiles"),
		legacyProfileDir:   managerDir,
		currentProfileFile: filepath.Join(managerDir, currentProfileMarkerName),
		status:             "Ready.",
	}
}

func newProgram(m appModel) *tea.Program {
	return tea.NewProgram(m)
}

func (m appModel) Init() tea.Cmd {
	return nil
}

func (m *appModel) reload() error {
	if err := os.MkdirAll(m.profileDir, 0o755); err != nil {
		return fmt.Errorf("failed to create profile directory: %w", err)
	}
	if err := os.MkdirAll(m.legacyProfileDir, 0o755); err != nil {
		return fmt.Errorf("failed to create legacy profile directory: %w", err)
	}
	if err := migrateLegacyProfiles(m.legacyProfileDir, m.profileDir); err != nil {
		return err
	}

	profiles, err := listProfiles(m.profileDir)
	if err != nil {
		return err
	}
	m.profiles = profiles

	if len(m.profiles) == 0 {
		m.cursor = 0
	} else if m.cursor >= len(m.profiles) {
		m.cursor = len(m.profiles) - 1
	}

	m.authActive = fileExists(m.authFile)
	if !m.authActive {
		m.currentProfile = ""
		return nil
	}

	marker, err := resolveCurrentProfileMarker(m.authFile, m.currentProfileFile, m.profileDir, m.profiles)
	if err != nil {
		return err
	}
	m.currentProfile = marker.Name

	return nil
}

func (m *appModel) syncTrackedProfile() error {
	if !m.authActive {
		return nil
	}
	marker, err := resolveCurrentProfileMarker(m.authFile, m.currentProfileFile, m.profileDir, m.profiles)
	if err != nil {
		return err
	}
	if marker.Name == "" {
		return nil
	}
	return syncProfileFromAuth(m.authFile, m.profileDir, marker)
}

func (m *appModel) activateSelectedProfile(name string) error {
	if err := m.syncTrackedProfile(); err != nil {
		return err
	}
	if err := activateProfile(m.authFile, []string{m.profileDir, m.legacyProfileDir}, name); err != nil {
		return err
	}

	marker, err := markerForProfile(m.profileDir, name)
	if err != nil {
		m.currentProfile = ""
		_ = clearCurrentProfileMarker(m.currentProfileFile)
		return err
	}
	if err := writeCurrentProfileMarker(m.currentProfileFile, marker); err != nil {
		m.currentProfile = ""
		_ = clearCurrentProfileMarker(m.currentProfileFile)
		return fmt.Errorf("activated profile %q, but failed to track it: %v", name, err)
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
	m.errText = ""
}

func (m *appModel) setError(s string) {
	m.errText = s
}

func (m *appModel) clearMessages() {
	m.status = ""
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

var errNoMatchingProfile = errors.New("no matching saved profile")
