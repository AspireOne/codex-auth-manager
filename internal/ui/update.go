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
		if len(m.profiles) == 0 {
			m.setError("No profiles to rename.")
			return m, nil
		}
		return m.enterInput(actionRename, fmt.Sprintf("Rename profile %q to:", m.selectedProfile()), m.selectedProfile()), nil

	case "s":
		if !m.authActive {
			m.setError("No active auth.json to save.")
			return m, nil
		}
		return m.enterInput(actionSave, "Save current auth as profile:", ""), nil

	case "d":
		if len(m.profiles) == 0 {
			m.setError("No profiles to delete.")
			return m, nil
		}
		return m.enterConfirm(actionDelete, fmt.Sprintf("Delete profile %q? [y/N]", m.selectedProfile())), nil

	case "l":
		if !m.authActive {
			m.setError("Already logged out.")
			return m, nil
		}
		return m.enterConfirm(actionLogout, "Remove current auth.json and log out? [y/N]"), nil

	case keyEnter:
		if len(m.profiles) == 0 {
			m.setError("No profiles to activate.")
			return m, nil
		}
		name := m.selectedProfile()
		if name == m.currentProfile {
			m.setInfo(fmt.Sprintf("Profile %q is already active.", name))
			return m, nil
		}
		if m.authActive && m.currentProfile == "" {
			return m.enterConfirm(actionActivate, fmt.Sprintf("Current auth is not saved as a profile. Replace it with %q? [y/N]", name)), nil
		}
		return m.activateProfile(name), nil

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
		return m, nil
	}

	return m, nil
}

func (m appModel) updateInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.cancelMode(), nil

	case keyEnter:
		value := strings.TrimSpace(m.inputValue)
		switch m.pendingAction {
		case actionNone, actionDelete, actionLogout, actionActivate:
			return m.exitMode(), nil
		case actionSave:
			if err := m.profileManager.SaveCurrent(value); err != nil {
				return m.handleActionError(err), nil
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			m.setStatus(fmt.Sprintf("Saved current auth as %q.", value))
			return m.exitMode(), nil

		case actionRename:
			oldName := m.selectedProfile()
			if err := m.profileManager.Rename(oldName, value, m.currentProfile); err != nil {
				return m.handleActionError(err), nil
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			m.cursor = indexOf(m.profiles, value)
			if m.cursor < 0 {
				m.cursor = 0
			}
			m.setStatus(fmt.Sprintf("Renamed %q to %q.", oldName, value))
			return m.exitMode(), nil
		}
		return m.exitMode(), nil

	case "backspace":
		runes := []rune(m.inputValue)
		if len(runes) > 0 {
			m.inputValue = string(runes[:len(runes)-1])
		}
		return m, nil
	}

	runes := []rune(msg.String())
	if len(runes) == 1 && isPrintableRune(runes[0]) {
		m.inputValue += string(runes[0])
	}

	return m, nil
}

func (m appModel) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "esc", "n", keyEnter:
		return m.cancelMode(), nil

	case "y":
		switch m.pendingAction {
		case actionNone, actionSave, actionRename:
			return m.exitMode(), nil
		case actionActivate:
			name := m.selectedProfile()
			m = m.activateProfile(name)
			return m.exitMode(), nil
		case actionDelete:
			name := m.selectedProfile()
			if err := m.profileManager.Delete(name, m.currentProfile); err != nil {
				return m.handleActionError(err), nil
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			if m.cursor >= len(m.profiles) && m.cursor > 0 {
				m.cursor--
			}
			m.setStatus(fmt.Sprintf("Deleted profile %q.", name))
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

func (m *appModel) activateProfile(name string) appModel {
	if err := m.activateSelectedProfile(name); err != nil {
		m.setError(err.Error())
		return *m
	}
	m.setStatus(fmt.Sprintf("Activated profile %q.", name))
	m.restartRequired = true
	return *m
}

func (m *appModel) handleActionError(err error) appModel {
	if errors.Is(err, profilemgr.ErrStateChanged) {
		return m.reloadAndExitWithError(err)
	}
	m.setError(err.Error())
	return m.exitMode()
}
