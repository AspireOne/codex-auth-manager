package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyPressMsg:
		switch m.mode {
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

	case "enter":
		if len(m.profiles) == 0 {
			m.setError("No profiles to activate.")
			return m, nil
		}
		name := m.selectedProfile()
		if err := m.activateSelectedProfile(name); err != nil {
			m.setError(err.Error())
			return m, nil
		}
		m.setStatus(fmt.Sprintf("Activated profile %q.", name))
		return m, nil

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

	case "enter":
		value := strings.TrimSpace(m.inputValue)
		switch m.pendingAction {
		case actionSave:
			if err := saveCurrentAuth(m.authFile, m.profileDir, value); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			marker, err := markerForProfile(m.profileDir, value)
			if err != nil {
				return m.reloadAndExitWithError(err), nil
			}
			if err := writeCurrentProfileMarker(m.currentProfileFile, marker); err != nil {
				return m.reloadAndExitWithError(err), nil
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			m.setStatus(fmt.Sprintf("Saved current auth as %q.", value))
			return m.exitMode(), nil

		case actionRename:
			oldName := m.selectedProfile()
			if err := renameProfile(m.profileDir, oldName, value); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			if m.currentProfile == oldName {
				marker, err := markerForProfile(m.profileDir, value)
				if err != nil {
					return m.reloadAndExitWithError(err), nil
				}
				if err := writeCurrentProfileMarker(m.currentProfileFile, marker); err != nil {
					return m.reloadAndExitWithError(err), nil
				}
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
	case "esc", "n", "enter":
		return m.cancelMode(), nil

	case "y":
		switch m.pendingAction {
		case actionDelete:
			name := m.selectedProfile()
			if err := deleteProfile(m.profileDir, name); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			var markerErr error
			if m.currentProfile == name {
				markerErr = clearCurrentProfileMarker(m.currentProfileFile)
			}
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			if m.cursor >= len(m.profiles) && m.cursor > 0 {
				m.cursor--
			}
			if markerErr != nil {
				m.setError(markerErr.Error())
				return m.exitMode(), nil
			}
			m.setStatus(fmt.Sprintf("Deleted profile %q.", name))
			return m.exitMode(), nil

		case actionLogout:
			if err := m.syncTrackedProfile(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			if err := logoutAuth(m.authFile); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			markerErr := clearCurrentProfileMarker(m.currentProfileFile)
			if err := m.reload(); err != nil {
				m.setError(err.Error())
				return m.exitMode(), nil
			}
			if markerErr != nil {
				m.setError(markerErr.Error())
				return m.exitMode(), nil
			}
			m.setStatus("Logged out.")
			return m.exitMode(), nil
		}
	}

	return m, nil
}
