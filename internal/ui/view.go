package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

func (m appModel) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	header := m.renderHeader()
	list := m.renderList()
	diagnostics := m.renderDiagnostics()
	footer := m.renderFooter()
	status := m.renderStatus()

	parts := []string{header, "", list}
	if diagnostics != "" {
		parts = append(parts, "", diagnostics)
	}
	parts = append(parts, "", footer, "", status)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return tea.NewView(baseStyle.Render(content))
}

func (m appModel) renderHeader() string {
	current := "none"
	if m.authActive {
		if m.currentProfile != "" {
			current = m.currentProfile
		} else {
			current = "custom/unsaved"
		}
	}

	authState := "logged out"
	if m.authActive {
		authState = "active"
	}

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		headerTitle.Render("Codex Auth Manager"),
		"",
		fmt.Sprintf("Current profile: %s", headerValue.Render(current)),
		fmt.Sprintf("Auth status:     %s", currentTag.Render(authState)),
		fmt.Sprintf("Saved profiles:  %s", headerValue.Render(fmt.Sprintf("%d", len(m.profiles)))),
		fmt.Sprintf("Profile dir:     %s", lipgloss.NewStyle().Foreground(mutedColor).Render(m.profileManager.ProfileDir)),
	)

	return panelStyle.Render(body)
}

func (m appModel) renderList() string {
	if len(m.profiles) == 0 {
		return emptyStyle.Render("No saved profiles.")
	}

	lines := make([]string, 0, len(m.profiles))
	for i, p := range m.profiles {
		prefix := "  "
		style := itemStyle

		if i == m.cursor {
			prefix = headerTitle.Render("»") + " "
			style = selectedItemStyle
		}

		label := p
		if p == m.currentProfile {
			label += currentTag.Render("  • current")
		}

		lines = append(lines, style.Render(prefix+label))
	}

	return panelStyle.Render(strings.Join(lines, "\n"))
}

func (m appModel) renderFooter() string {
	switch m.mode {
	case modeInput:
		return footerStyle.Render(fmt.Sprintf("%s %s", m.inputPrompt, m.inputValue+"█"))
	case modeConfirm:
		return footerStyle.Render(m.confirmPrompt)
	case modeNormal:
		profileCommands := []string{
			formatKeyHint("↑/↓", "move"),
			formatKeyHint("enter", "activate"),
			formatKeyHint("r", "rename"),
			formatKeyHint("d", "delete"),
		}
		globalCommands := []string{
			formatKeyHint("s", "save"),
			formatKeyHint("l", "logout"),
			formatKeyHint("ctrl+r", "refresh"),
			formatKeyHint("q", "quit"),
		}

		return lipgloss.JoinVertical(
			lipgloss.Left,
			footerStyle.Render("UI: "+strings.Join(profileCommands, " • ")),
			footerStyle.Render("Global: "+strings.Join(globalCommands, " • ")),
		)
	default:
		return ""
	}
}

func (m appModel) renderDiagnostics() string {
	if len(m.invalidProfiles) == 0 {
		return ""
	}

	lines := []string{
		warningStyle.Render(fmt.Sprintf("Ignored %d invalid profile file(s):", len(m.invalidProfiles))),
	}
	for i, issue := range m.invalidProfiles {
		if i == 3 {
			lines = append(lines, footerStyle.Render(fmt.Sprintf("...and %d more", len(m.invalidProfiles)-i)))
			break
		}
		lines = append(lines, fmt.Sprintf("%s %s", headerValue.Render(issue.Name), footerStyle.Render(issue.Reason)))
	}

	return panelStyle.Render(strings.Join(lines, "\n"))
}

func (m appModel) renderStatus() string {
	if m.errText != "" {
		return errorStyle.Render("Error: " + m.errText)
	}
	style := statusStyle
	if m.statusKind == statusInfo {
		style = infoStyle
	}
	s := style.Render(m.status)
	if m.restartRequired {
		s += lipgloss.NewStyle().Foreground(mutedColor).Render(" (Restart Codex to apply)")
	}
	return s
}
