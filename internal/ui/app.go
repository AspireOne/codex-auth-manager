package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	profilemgr "codex-manage/internal/profiles"
	"codex-manage/internal/reauth"
	"codex-manage/internal/updatecheck"
)

type mode int

const (
	modeNormal mode = iota
	modeInput
	modeConfirm
	modeAuthenticating
)

type action int

const (
	actionNone action = iota
	actionSave
	actionEditLabel
	actionEditNote
	actionDelete
	actionLogout
	actionActivate
)

type statusKind int

const (
	statusSuccess statusKind = iota
	statusInfo
)

type appModel struct {
	profileManager profilemgr.Manager
	authenticator  reauth.Authenticator

	profiles        []profilemgr.ProfileSummary
	cursor          int
	invalidProfiles []profilemgr.ProfileIssue

	currentProfileKey string
	currentAuth       profilemgr.AuthSummary
	authActive        bool
	appVersion        string
	updateChecked     bool
	updateNotice      string

	width  int
	height int

	mode            mode
	pendingAction   action
	textInput       textinput.Model
	confirmPrompt   string
	status          string
	statusKind      statusKind
	errText         string
	restartRequired bool
	authCancel      context.CancelFunc
	authProfileKey  string

	quitting bool
}

type updateCheckMsg struct {
	result updatecheck.Result
	err    error
}

type authenticationFinishedMsg struct {
	profileKey string
	label      string
	err        error
}

func Run(version string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	m := newAppModel(home)
	m.appVersion = version
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
	manager := profilemgr.NewManager(codexDir)
	return appModel{
		profileManager: manager,
		authenticator:  reauth.New(manager),
		textInput:      newTextInput(),
		status:         "Ready.",
	}
}

func (m appModel) authenticateCmd(ctx context.Context, profile profilemgr.ProfileSummary) tea.Cmd {
	return func() tea.Msg {
		return authenticationFinishedMsg{
			profileKey: profile.Key,
			label:      profile.Label,
			err:        m.authenticator.Reauthenticate(ctx, profile),
		}
	}
}

func newTextInput() textinput.Model {
	input := textinput.New()
	styles := input.Styles()
	styles.Focused.Prompt = footerStyle
	styles.Focused.Text = footerStyle
	styles.Blurred.Prompt = footerStyle
	styles.Blurred.Text = footerStyle
	styles.Cursor.Color = mutedColor
	input.SetStyles(styles)
	return input
}

func (m appModel) Init() tea.Cmd {
	return m.updateCheckCmd()
}

func (m *appModel) reload() error {
	snapshot, err := m.profileManager.Snapshot()
	if err != nil {
		return err
	}
	m.profiles = snapshot.Profiles
	sort.SliceStable(m.profiles, func(i, j int) bool {
		if m.profiles[i].Plan.Rank() != m.profiles[j].Plan.Rank() {
			return m.profiles[i].Plan.Rank() < m.profiles[j].Plan.Rank()
		}
		return m.profiles[i].Label < m.profiles[j].Label
	})
	m.invalidProfiles = snapshot.InvalidProfiles

	if len(m.profiles) == 0 {
		m.cursor = 0
	} else if m.cursor >= len(m.profiles) {
		m.cursor = len(m.profiles) - 1
	}

	m.authActive = snapshot.AuthActive
	m.currentProfileKey = snapshot.CurrentProfileKey
	m.currentAuth = snapshot.CurrentAuth
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

func (m *appModel) selectedProfile() profilemgr.ProfileSummary {
	if len(m.profiles) == 0 || m.cursor < 0 || m.cursor >= len(m.profiles) {
		return profilemgr.ProfileSummary{}
	}
	return m.profiles[m.cursor]
}

func (m *appModel) selectedProfileKey() string {
	return m.selectedProfile().Key
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

func (m *appModel) enterInput(nextAction action, prompt, value string) (appModel, tea.Cmd) {
	m.mode = modeInput
	m.pendingAction = nextAction
	m.confirmPrompt = ""
	m.resetInput()
	m.textInput.Prompt = prompt + " "
	m.textInput.SetValue(value)
	m.textInput.CursorEnd()
	m.resizeInput()
	m.clearMessages()
	cmd := m.textInput.Focus()
	return *m, cmd
}

func (m *appModel) enterConfirm(nextAction action, prompt string) appModel {
	m.mode = modeConfirm
	m.pendingAction = nextAction
	m.confirmPrompt = prompt
	m.resetInput()
	m.clearMessages()
	return *m
}

func (m *appModel) cancelMode() appModel {
	m.mode = modeNormal
	m.pendingAction = actionNone
	m.confirmPrompt = ""
	m.resetInput()
	m.setStatus("Cancelled.")
	return *m
}

func (m *appModel) exitMode() appModel {
	m.mode = modeNormal
	m.pendingAction = actionNone
	m.confirmPrompt = ""
	m.resetInput()
	return *m
}

func (m *appModel) resetInput() {
	m.textInput.Blur()
	m.textInput.Reset()
	m.textInput.Prompt = ""
}

func (m *appModel) resizeInput() {
	if m.width <= 0 {
		m.textInput.SetWidth(0)
		return
	}

	width := m.width - baseStyle.GetHorizontalFrameSize() - lipgloss.Width(m.textInput.Prompt)
	m.textInput.SetWidth(max(1, width))
}

func (m *appModel) reloadAndExitWithError(err error) appModel {
	if reloadErr := m.reload(); reloadErr != nil {
		m.setError(reloadErr.Error())
	} else {
		m.setError(err.Error())
	}
	return m.exitMode()
}

func (m appModel) updateCheckCmd() tea.Cmd {
	return func() tea.Msg {
		result, err := updatecheck.New().Check(context.Background(), m.appVersion)
		return updateCheckMsg{result: result, err: err}
	}
}
