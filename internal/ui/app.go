package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	profilemgr "codex-manage/internal/profiles"
	"codex-manage/internal/profilestatus"
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
	statusFetcher  profilestatus.Fetcher

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

	profileStatuses map[string]profileStatusView
	statusQueue     []string
	statusCancels   map[string]context.CancelFunc
	statusPaused    bool
	statusEpoch     int
	now             func() time.Time

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

type startStatusMsg struct{}

type statusFetchFinishedMsg struct {
	profileKey string
	epoch      int
	status     profilemgr.ProfileStatus
	err        error
}

type statusPhase int

const (
	statusLoading statusPhase = iota
	statusCached
	statusFailed
)

type profileStatusView struct {
	status *profilemgr.ProfileStatus
	phase  statusPhase
	stale  bool
}

const profileStatusTTL = 30 * time.Minute

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
		profileManager:  manager,
		authenticator:   reauth.New(manager),
		statusFetcher:   profilestatus.New(manager),
		profileStatuses: make(map[string]profileStatusView),
		statusCancels:   make(map[string]context.CancelFunc),
		now:             time.Now,
		textInput:       newTextInput(),
		status:          "Ready.",
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
	return tea.Batch(m.updateCheckCmd(), func() tea.Msg { return startStatusMsg{} })
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
	if m.profileStatuses == nil {
		m.profileStatuses = make(map[string]profileStatusView)
	}
	if m.statusCancels == nil {
		m.statusCancels = make(map[string]context.CancelFunc)
	}
	if m.now == nil {
		m.now = time.Now
	}
	valid := make(map[string]profilemgr.AuthKind, len(m.profiles))
	for _, profile := range m.profiles {
		valid[profile.Key] = profile.Kind
	}
	cached, err := m.profileManager.LoadProfileStatuses(valid, m.now())
	if err != nil {
		return err
	}
	m.statusQueue = m.statusQueue[:0]
	seen := make(map[string]struct{}, len(m.profiles))
	for _, profile := range m.profiles {
		if profile.Kind != profilemgr.AuthKindChatGPT {
			delete(m.profileStatuses, profile.Key)
			continue
		}
		seen[profile.Key] = struct{}{}
		status, exists := cached[profile.Key]
		fresh := exists && m.now().Sub(status.FetchedAt) < profileStatusTTL
		if exists {
			copy := status
			phase := statusCached
			if !fresh {
				phase = statusLoading
			}
			m.profileStatuses[profile.Key] = profileStatusView{status: &copy, phase: phase, stale: !fresh}
		} else {
			m.profileStatuses[profile.Key] = profileStatusView{phase: statusLoading}
		}
		if !fresh {
			if _, running := m.statusCancels[profile.Key]; !running {
				m.statusQueue = append(m.statusQueue, profile.Key)
			}
		}
	}
	for key := range m.profileStatuses {
		if _, ok := seen[key]; !ok {
			delete(m.profileStatuses, key)
		}
	}
	return nil
}

func (m appModel) statusFetchCmd(ctx context.Context, epoch int, profile profilemgr.ProfileSummary) tea.Cmd {
	return func() tea.Msg {
		status, err := m.statusFetcher.Fetch(ctx, profile)
		return statusFetchFinishedMsg{profileKey: profile.Key, epoch: epoch, status: status, err: err}
	}
}

func (m *appModel) dispatchStatusFetches() tea.Cmd {
	if m.statusPaused || m.statusFetcher == nil {
		return nil
	}
	var commands []tea.Cmd
	for len(m.statusCancels) < 2 && len(m.statusQueue) > 0 {
		key := m.statusQueue[0]
		m.statusQueue = m.statusQueue[1:]
		index := indexOfProfile(m.profiles, key)
		if index < 0 || m.profiles[index].Kind != profilemgr.AuthKindChatGPT {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		m.statusCancels[key] = cancel
		view := m.profileStatuses[key]
		view.phase = statusLoading
		m.profileStatuses[key] = view
		commands = append(commands, m.statusFetchCmd(ctx, m.statusEpoch, m.profiles[index]))
	}
	return tea.Batch(commands...)
}

func (m *appModel) cancelStatusFetches() {
	m.statusEpoch++
	for key, cancel := range m.statusCancels {
		cancel()
		delete(m.statusCancels, key)
	}
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
