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

		label := p.Name
		if p.Name == m.currentProfile {
			label += currentTag.Render("  • current")
		}

		lines = append(lines, m.renderProfileLine(style, prefix, label, p.Note))
	}

	return panelStyle.Render(strings.Join(lines, "\n"))
}

func (m appModel) renderProfileLine(style lipgloss.Style, prefix, label, note string) string {
	base := style.Render(prefix + label)
	note = strings.TrimSpace(note)
	if note == "" {
		return base
	}

	separator := "  -  "
	noteStyle := footerStyle
	continuationIndent := strings.Repeat(" ", lipgloss.Width(base)+lipgloss.Width(separator))
	availableWidth := m.listContentWidth()
	if availableWidth <= 0 {
		return base + noteStyle.Render(separator+note)
	}

	firstLineWidth := availableWidth - lipgloss.Width(base) - lipgloss.Width(separator)
	if firstLineWidth < 12 {
		firstLineWidth = 12
	}
	continuationWidth := availableWidth - lipgloss.Width(continuationIndent)
	if continuationWidth < 12 {
		continuationWidth = 12
	}

	wrapped := wrapWords(note, firstLineWidth, continuationWidth)
	if len(wrapped) == 0 {
		return base
	}

	line := base + noteStyle.Render(separator+wrapped[0])
	for _, part := range wrapped[1:] {
		line += "\n" + noteStyle.Render(continuationIndent+part)
	}
	return line
}

func (m appModel) listContentWidth() int {
	if m.width <= 0 {
		return 0
	}

	const horizontalChrome = 10
	width := m.width - horizontalChrome
	if width < 20 {
		return 20
	}
	return width
}

func wrapWords(text string, firstLineWidth, continuationWidth int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	lines := make([]string, 0, 1)
	currentWidth := firstLineWidth
	current := words[0]

	for _, word := range words[1:] {
		candidate := current + " " + word
		if lipgloss.Width(candidate) <= currentWidth {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = word
		currentWidth = continuationWidth
	}

	lines = append(lines, current)
	return lines
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
			formatKeyHint("n", "edit note"),
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
