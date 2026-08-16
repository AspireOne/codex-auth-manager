package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	profilemgr "codex-manage/internal/profiles"
)

const keyEnter = "enter"

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.mode == modeInput {
			m.resizeInput()
		}
		return m, nil

	case updateCheckMsg:
		if msg.err != nil || !msg.result.Checked {
			m.updateChecked = false
			m.updateNotice = ""
			return m, nil
		}

		m.updateChecked = true
		m.updateNotice = ""
		if msg.result.UpdateAvailable {
			m.updateNotice = fmt.Sprintf("Update available: %s", msg.result.LatestVersion)
		}
		return m, nil

	case tea.KeyPressMsg:
		switch m.mode {
		case modeNormal:
			return m.updateNormal(msg)
		case modeInput:
			return m.updateInput(msg)
		case modeConfirm:
			return m.updateConfirm(msg)
		default:
			return m.updateNormal(msg)
		}
	}
	if m.mode == modeInput {
		return m.updateInput(msg)
	}

	return m, nil
}

func (m appModel) updateNormal(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if err := m.syncTrackedProfile(); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit

	case "up", "k":
		if len(m.profiles) > 0 && m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if len(m.profiles) > 0 && m.cursor < len(m.profiles)-1 {
			m.cursor++
		}
		return m, nil

	case "r":
		return m.enterEditLabelMode()

	case "n":
		return m.enterEditNoteMode()

	case "p":
		return m.cycleSelectedPlan(), nil

	case "s":
		return m.saveActiveAuth()

	case "d":
		if len(m.profiles) == 0 {
			m.setError("No profiles to delete.")
			return m, nil
		}
		selected := m.selectedProfile()
		prompt := fmt.Sprintf("Delete saved profile %q? [y/N]", selected.Label)
		if selected.Key == m.currentProfileKey {
			prompt = fmt.Sprintf("Delete saved profile %q? Current login stays active. [y/N]", selected.Label)
		}
		return m.enterConfirm(actionDelete, prompt), nil

	case "l":
		if !m.authActive {
			m.setError("Already logged out.")
			return m, nil
		}
		return m.enterConfirm(actionLogout, "Remove active auth.json? Saved profiles stay untouched. [y/N]"), nil

	case keyEnter:
		if len(m.profiles) == 0 {
			m.setError("No profiles to activate.")
			return m, nil
		}
		selected := m.selectedProfile()
		if selected.Key == m.currentProfileKey {
			m.setInfo(fmt.Sprintf("Profile %q is already active.", selected.Label))
			return m, nil
		}
		if m.authActive && m.currentProfileKey == "" {
			return m.enterConfirm(actionActivate, fmt.Sprintf("Current auth is not saved as a profile. Replace it with %q? [y/N]", selected.Label)), nil
		}
		return m.activateProfile(selected.Key), nil

	case "F5", "ctrl+r":
		if err := m.syncTrackedProfile(); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		if err := m.reload(); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.setStatus("Refreshed.")
		return m, m.updateCheckCmd()
	}

	return m, nil
}

func (m appModel) saveActiveAuth() (tea.Model, tea.Cmd) {
	if !m.authActive {
		m.setError("No active auth.json to save.")
		return m, nil
	}
	if m.currentProfileKey != "" {
		if err := m.syncTrackedProfile(); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		if err := m.reload(); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.setInfo(fmt.Sprintf("%q is already saved.", m.currentAuth.Label))
		return m, nil
	}
	if m.currentAuth.Kind == profilemgr.AuthKindAPIKey {
		return m.enterInput(actionSave, "API key label (optional; blank uses fingerprint):", "")
	}
	return m.saveCurrent(""), nil
}

func (m appModel) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.String() {
		case "esc":
			return m.cancelMode(), nil

		case keyEnter:
			value := strings.TrimSpace(m.textInput.Value())
			switch m.pendingAction {
			case actionNone, actionDelete, actionLogout, actionActivate:
				return m.exitMode(), nil
			case actionSave:
				m = m.saveCurrent(value)
				return m.exitMode(), nil

			case actionEditLabel:
				selected := m.selectedProfile()
				if err := m.profileManager.SetLabel(selected.Key, value); err != nil {
					return m.handleActionError(err), nil
				}
				if err := m.reload(); err != nil {
					m.setError(err.Error())
					return m.exitMode(), nil
				}
				m.cursor = indexOfProfile(m.profiles, selected.Key)
				if m.cursor < 0 {
					m.cursor = 0
				}
				m.setStatus(fmt.Sprintf("Updated API key label to %q.", m.selectedProfile().Label))
				return m.exitMode(), nil
			case actionEditNote:
				selected := m.selectedProfile()
				if err := m.profileManager.SetNote(selected.Key, value); err != nil {
					return m.handleActionError(err), nil
				}
				if err := m.reload(); err != nil {
					m.setError(err.Error())
					return m.exitMode(), nil
				}
				if value == "" {
					m.setStatus(fmt.Sprintf("Removed note for %q.", selected.Label))
				} else {
					m.setStatus(fmt.Sprintf("Updated note for %q.", selected.Label))
				}
				return m.exitMode(), nil
			}
			return m.exitMode(), nil
		}
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m appModel) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "esc", "n", keyEnter:
		return m.cancelMode(), nil

	case "y":
		switch m.pendingAction {
		case actionNone, actionSave, actionEditLabel, actionEditNote:
			return m.exitMode(), nil
		case actionActivate:
			m = m.activateProfile(m.selectedProfileKey())
			return m.exitMode(), nil
		case actionDelete:
			selected := m.selectedProfile()
			if err := m.profileManager.Delete(selected.Key, m.currentProfileKey); err != nil {
				return m.handleActionError(err), nil
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			if m.cursor >= len(m.profiles) && m.cursor > 0 {
				m.cursor--
			}
			m.setStatus(fmt.Sprintf("Deleted profile %q.", selected.Label))
			return m.exitMode(), nil

		case actionLogout:
			if err := m.profileManager.Logout(); err != nil {
				return m.handleActionError(err), nil
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			m.setStatus("Logged out.")
			m.restartRequired = true
			return m.exitMode(), nil
		}
	}

	return m, nil
}

func (m appModel) enterEditLabelMode() (tea.Model, tea.Cmd) {
	if len(m.profiles) == 0 {
		m.setError("No profiles to edit.")
		return m, nil
	}
	selected := m.selectedProfile()
	if selected.Kind != profilemgr.AuthKindAPIKey {
		m.setInfo("ChatGPT profile labels come from the account email.")
		return m, nil
	}
	return m.enterInput(actionEditLabel, fmt.Sprintf("Edit API key label for %q (blank uses fingerprint):", selected.Label), selected.CustomLabel)
}

func (m appModel) enterEditNoteMode() (tea.Model, tea.Cmd) {
	if len(m.profiles) == 0 {
		m.setError("No profiles to edit.")
		return m, nil
	}
	selected := m.selectedProfile()
	return m.enterInput(actionEditNote, fmt.Sprintf("Edit note for %q:", selected.Label), selected.Note)
}

func (m appModel) cycleSelectedPlan() appModel {
	if len(m.profiles) == 0 {
		m.setError("No profiles to update.")
		return m
	}

	selected := m.selectedProfile()
	nextPlan := selected.Plan.Next()
	if err := m.profileManager.SetPlan(selected.Key, nextPlan); err != nil {
		return m.handleActionError(err)
	}
	if err := m.reload(); err != nil {
		m.setError(err.Error())
		return m
	}
	m.cursor = indexOfProfile(m.profiles, selected.Key)
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.setStatus(fmt.Sprintf("Set plan for %q to %s.", selected.Label, nextPlan.Label()))
	return m
}

func (m *appModel) activateProfile(key string) appModel {
	selected := m.selectedProfile()
	if err := m.activateSelectedProfile(key); err != nil {
		m.setError(err.Error())
		return *m
	}
	m.setStatus(fmt.Sprintf("Activated profile %q.", selected.Label))
	m.restartRequired = true
	return *m
}

func (m *appModel) saveCurrent(label string) appModel {
	if err := m.profileManager.SaveCurrent(label); err != nil {
		return m.handleActionError(err)
	}
	if err := m.reload(); err != nil {
		m.setError(err.Error())
		return *m
	}
	m.setStatus(fmt.Sprintf("Saved current auth as %q.", m.currentAuth.Label))
	return *m
}

func (m *appModel) handleActionError(err error) appModel {
	if errors.Is(err, profilemgr.ErrStateChanged) {
		return m.reloadAndExitWithError(err)
	}
	m.setError(err.Error())
	return m.exitMode()
}
