package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	profilemgr "codex-manage/internal/profiles"
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
		if m.currentProfileKey != "" {
			current = m.currentAuth.Label
		} else {
			current = m.currentAuth.Label
			if current == "" {
				current = "custom auth"
			}
			current += " (unsaved)"
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
		m.renderUpdateLine(),
		fmt.Sprintf("Saved profiles:  %s", headerValue.Render(fmt.Sprintf("%d", len(m.profiles)))),
		fmt.Sprintf("Profile dir:     %s", lipgloss.NewStyle().Foreground(mutedColor).Render(m.profileManager.ProfileDir)),
	)

	return panelStyle.Render(body)
}

func (m appModel) renderUpdateLine() string {
	if !m.updateChecked {
		return "Update:          not checked"
	}

	if m.updateNotice == "" {
		return "Update:          up to date"
	}

	return fmt.Sprintf("Update:          %s", infoStyle.Render(m.updateNotice))
}

func (m appModel) renderList() string {
	if len(m.profiles) == 0 {
		return emptyStyle.Render("No saved profiles.")
	}

	lines := make([]string, 0, len(m.profiles))
	profileColumnWidth := m.profileColumnWidth()
	for i, p := range m.profiles {
		prefix := "  "
		style := itemStyle

		if i == m.cursor {
			prefix = headerTitle.Render("»") + " "
			style = selectedItemStyle
		}

		lines = append(lines, m.renderProfileLineWithStatus(style, prefix, p, p.Key == m.currentProfileKey, profileColumnWidth))
	}

	return panelStyle.Render(strings.Join(lines, "\n"))
}

func (m appModel) renderProfileLine(style lipgloss.Style, prefix, label, note string, plan profilemgr.Plan, isCurrent bool, profileColumnWidth int) string {
	profile := profilemgr.ProfileSummary{Label: label, Note: note, Plan: plan}
	return m.renderProfileLineWithStatus(style, prefix, profile, isCurrent, profileColumnWidth)
}

func (m appModel) renderProfileLineWithStatus(style lipgloss.Style, prefix string, profile profilemgr.ProfileSummary, isCurrent bool, profileColumnWidth int) string {
	base := m.renderProfileCell(style, prefix, profile.Label, isCurrent, profileColumnWidth) + "  " + renderPlan(profile.Plan)
	trailing := strings.TrimSpace(profile.Note)
	if profile.Kind == profilemgr.AuthKindChatGPT {
		status := m.renderProfileStatus(profile.Key)
		if trailing != "" {
			trailing = status + "  " + trailing
		} else {
			trailing = status
		}
	}
	if trailing == "" {
		return base
	}

	separator := "  "
	noteStyle := footerStyle
	if profile.Kind == profilemgr.AuthKindChatGPT {
		noteStyle = lipgloss.NewStyle()
		if m.profileStatuses[profile.Key].stale {
			noteStyle = mutedStatusStyle
		}
	}
	continuationIndent := "    "
	availableWidth := m.listContentWidth()
	if availableWidth <= 0 {
		return base + noteStyle.Render(separator+trailing)
	}

	firstLineWidth := availableWidth - lipgloss.Width(base) - lipgloss.Width(separator)
	if firstLineWidth < 12 {
		firstLineWidth = 12
	}
	continuationWidth := availableWidth - lipgloss.Width(continuationIndent)
	if continuationWidth < 12 {
		continuationWidth = 12
	}

	wrapped := wrapWords(trailing, firstLineWidth, continuationWidth)
	if len(wrapped) == 0 {
		return base
	}

	line := base + noteStyle.Render(separator+wrapped[0])
	for _, part := range wrapped[1:] {
		line += "\n" + noteStyle.Render(continuationIndent+part)
	}
	return line
}

func (m appModel) renderProfileStatus(key string) string {
	view, ok := m.profileStatuses[key]
	if !ok {
		view = profileStatusView{phase: statusLoading}
	}
	usage := "x% used (resets at x)"
	auth := "Unknown"
	if view.status != nil {
		if view.status.UsedPercent != nil && view.status.ResetsAt != nil {
			usage = fmt.Sprintf("%d%% used (resets at %s)", *view.status.UsedPercent, view.status.ResetsAt.In(time.Local).Format("2006-01-02 15:04"))
		}
		switch view.status.AuthStatus {
		case profilemgr.ProfileAuthAuthenticated:
			auth = "Authenticated"
		case profilemgr.ProfileAuthSignInRequired:
			auth = "Sign-in required"
		}
	}
	cache := "Loading"
	switch view.phase {
	case statusLoading:
		cache = "Loading"
	case statusCached:
		cache = "Cached"
	case statusFailed:
		if view.status != nil {
			cache = "Cached · failed"
		} else {
			cache = "Unavailable · failed"
		}
	}
	return usage + "  " + auth + "  " + cache
}

func renderPlan(plan profilemgr.Plan) string {
	const planColumnWidth = 4

	label := plan.Label()
	style := freePlanStyle
	switch plan {
	case profilemgr.PlanPro:
		style = proPlanStyle
	case profilemgr.PlanPlus:
		style = plusPlanStyle
	case profilemgr.PlanFree:
		style = freePlanStyle
	}
	return style.Render(label) + strings.Repeat(" ", planColumnWidth-lipgloss.Width(label))
}

func (m appModel) renderProfileCell(style lipgloss.Style, prefix, label string, isCurrent bool, profileColumnWidth int) string {
	const currentMarker = " ●"
	const markerWidth = 2

	cellText := prefix + label
	paddingWidth := profileColumnWidth - lipgloss.Width(cellText) - markerWidth
	if paddingWidth < 0 {
		paddingWidth = 0
	}

	cell := style.Render(cellText) + strings.Repeat(" ", paddingWidth)
	if isCurrent {
		return cell + currentTag.Render(currentMarker)
	}
	return cell + strings.Repeat(" ", markerWidth)
}

func (m appModel) profileColumnWidth() int {
	const markerWidth = 2

	width := 0
	for i, p := range m.profiles {
		prefix := "  "
		if i == m.cursor {
			prefix = "» "
		}

		lineWidth := lipgloss.Width(prefix+p.Label) + markerWidth
		if lineWidth > width {
			width = lineWidth
		}
	}
	return width
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
		return m.textInput.View()
	case modeConfirm:
		return footerStyle.Render(m.confirmPrompt)
	case modeAuthenticating:
		return footerStyle.Render(formatKeyHint("esc", "cancel authentication"))
	case modeNormal:
		navigationCommands := []string{
			formatKeyHint("↑/↓", "move"),
			formatKeyHint("enter", "activate"),
			formatKeyHint("p", "cycle plan"),
		}
		profileCommands := []string{
			formatKeyHint("n", "edit note"),
		}
		if m.selectedProfile().Kind == profilemgr.AuthKindChatGPT {
			profileCommands = append(profileCommands, formatKeyHint("a", "authenticate"))
		}
		if m.selectedProfile().Kind == profilemgr.AuthKindAPIKey {
			profileCommands = append(profileCommands, formatKeyHint("r", "edit label"))
		}
		profileCommands = append(profileCommands, formatKeyHint("d", "delete"))
		globalCommands := []string{
			formatKeyHint("s", "save"),
			formatKeyHint("l", "logout"),
			formatKeyHint("ctrl+r", "refresh"),
			formatKeyHint("q", "quit"),
		}

		return lipgloss.JoinVertical(
			lipgloss.Left,
			footerStyle.Render("UI: "+strings.Join(navigationCommands, " • ")),
			footerStyle.Render("Manage: "+strings.Join(profileCommands, " • ")),
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
