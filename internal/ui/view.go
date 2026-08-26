package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	profilemgr "codex-manage/internal/profiles"
)

type statusDisplayState int

const (
	statusDisplayCached statusDisplayState = iota
	statusDisplayLoading
	statusDisplayFailed
)

const profileStatusTimestampFormat = "02.01. 15:04"

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

	lines := []string{
		headerTitle.Render("Codex Auth Manager"),
		"",
		fmt.Sprintf("Current profile: %s", headerValue.Render(current)),
		fmt.Sprintf("Profile status:  %s", currentTag.Render(authState)),
	}
	if currentStatus := m.renderCurrentStatusLine(); currentStatus != "" {
		lines = append(lines, currentStatus)
	}
	lines = append(lines,
		m.renderUpdateLine(),
		fmt.Sprintf("Browser auth:     %s", footerStyle.Render(m.browserAuthStatus)),
		fmt.Sprintf("Profile dir:     %s", lipgloss.NewStyle().Foreground(mutedColor).Render(m.profileManager.ProfileDir)),
	)
	body := lipgloss.JoinVertical(lipgloss.Left, lines...)

	return panelStyle.Render(body)
}

func (m appModel) renderCurrentStatusLine() string {
	view, ok := m.currentChatGPTStatusView()
	if !ok {
		return ""
	}

	state := statusDisplayStateFor(view)
	lastRefreshed := "never"
	if refreshedAt, ok := successfulFetchedAt(view); ok {
		lastRefreshed = refreshedAt.In(time.Local).Format(profileStatusTimestampFormat)
	}
	text := fmt.Sprintf("%s • last refreshed at %s", state.label(), lastRefreshed)
	return fmt.Sprintf("State:           %s", footerStyle.Render(text))
}

func (m appModel) currentChatGPTStatusView() (profileStatusView, bool) {
	if !m.authActive || m.currentProfileKey == "" {
		return profileStatusView{}, false
	}
	index := indexOfProfile(m.profiles, m.currentProfileKey)
	if index < 0 || m.profiles[index].Kind != profilemgr.AuthKindChatGPT {
		return profileStatusView{}, false
	}
	return m.statusViewForProfile(m.currentProfileKey), true
}

func (m appModel) statusViewForProfile(key string) profileStatusView {
	if view, ok := m.profileStatuses[key]; ok {
		return view
	}
	return profileStatusView{phase: statusLoading}
}

func successfulFetchedAt(view profileStatusView) (time.Time, bool) {
	if view.status == nil || view.status.AuthStatus != profilemgr.ProfileAuthAuthenticated || view.status.FetchedAt.IsZero() {
		return time.Time{}, false
	}
	return view.status.FetchedAt, true
}

func statusDisplayStateFor(view profileStatusView) statusDisplayState {
	switch view.phase {
	case statusLoading:
		return statusDisplayLoading
	case statusFailed:
		return statusDisplayFailed
	case statusCached:
		if _, ok := successfulFetchedAt(view); !ok {
			return statusDisplayFailed
		}
		return statusDisplayCached
	default:
		return statusDisplayFailed
	}
}

func (state statusDisplayState) label() string {
	switch state {
	case statusDisplayCached:
		return "Cached"
	case statusDisplayLoading:
		return "Loading"
	case statusDisplayFailed:
		return "Failed"
	default:
		return "Failed"
	}
}

func (state statusDisplayState) icon() string {
	switch state {
	case statusDisplayCached:
		return "✓"
	case statusDisplayLoading:
		return "?"
	case statusDisplayFailed:
		return "✗"
	default:
		return "✗"
	}
}

func (state statusDisplayState) style() lipgloss.Style {
	if state == statusDisplayCached {
		return currentTag
	}
	if state == statusDisplayFailed {
		return errorStyle
	}
	return footerStyle
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

	profileColumnWidth := m.profileColumnWidth()
	lines := make([]string, 0, len(m.profiles)+1)
	lines = append(lines, m.renderProfileTableHeader(profileColumnWidth))
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

func (m appModel) renderProfileTableHeader(profileColumnWidth int) string {
	profile := tableHeaderStyle.Render("Profile") + strings.Repeat(" ", max(0, profileColumnWidth-lipgloss.Width("Profile")))
	base := profile + "    " + tableHeaderStyle.Render("Plan")
	headings := []string{"Limits", "State"}
	widths := m.profileStatusColumnWidths()
	for i, heading := range headings {
		headings[i] = tableHeaderStyle.Render(heading) + strings.Repeat(" ", widths[i]-lipgloss.Width(heading))
	}
	return renderProfileColumns(append([]string{base}, append(headings, tableHeaderStyle.Render("Note"))...)...)
}

func (m appModel) renderProfileLine(style lipgloss.Style, prefix, label, note string, plan profilemgr.Plan, isCurrent bool, profileColumnWidth int) string {
	profile := profilemgr.ProfileSummary{Label: label, Note: note, Plan: plan}
	return m.renderProfileLineWithStatus(style, prefix, profile, isCurrent, profileColumnWidth)
}

func (m appModel) renderProfileLineWithStatus(style lipgloss.Style, prefix string, profile profilemgr.ProfileSummary, isCurrent bool, profileColumnWidth int) string {
	marker := " "
	if isCurrent {
		marker = currentTag.Render("●")
	}
	base := m.renderProfileCell(style, prefix, profile.Label, profileColumnWidth) + " " + marker + "  " + renderPlan(profile.Plan)
	trailing := strings.TrimSpace(profile.Note)
	if profile.Kind == profilemgr.AuthKindChatGPT {
		status := renderProfileColumns(m.renderProfileStatusColumns(profile.Key)...)
		if trailing != "" {
			trailing = status + profileColumnSeparator.Render(" │ ") + trailing
		} else {
			trailing = status
		}
	}
	if trailing == "" {
		return base
	}

	separator := profileColumnSeparator.Render(" │ ")
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
	if lipgloss.Width(base)+lipgloss.Width(separator)+lipgloss.Width(trailing) <= availableWidth {
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
	return renderProfileColumns(m.profileStatusValues(key)...)
}

func (m appModel) profileStatusValues(key string) []string {
	view := m.statusViewForProfile(key)
	limits := "-"
	if view.status != nil && view.status.AuthStatus == profilemgr.ProfileAuthAuthenticated {
		limits = formatProfileQuotas(profileQuotas(view.status))
	}
	return []string{limits, statusDisplayStateFor(view).icon()}
}

func remainingPercent(used int) int {
	return max(0, min(100, 100-used))
}

func (m appModel) renderProfileStatusColumns(key string) []string {
	state := statusDisplayStateFor(m.statusViewForProfile(key))
	values := m.profileStatusValues(key)
	widths := m.profileStatusColumnWidths()
	limitValue := values[0]
	values[0] = m.renderProfileQuotaCell(key) + strings.Repeat(" ", widths[0]-lipgloss.Width(limitValue))
	values[1] = state.style().Render(values[1]) + strings.Repeat(" ", widths[1]-lipgloss.Width(values[1]))
	return values
}

func (m appModel) renderProfileQuotaCell(key string) string {
	view := m.statusViewForProfile(key)
	if view.status == nil || view.status.AuthStatus != profilemgr.ProfileAuthAuthenticated {
		return m.profileStatusValues(key)[0]
	}
	quotas := profileQuotas(view.status)
	if len(quotas) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(quotas))
	for _, quota := range quotas {
		if !displayableProfileQuota(quota) {
			continue
		}
		remaining := max(0, min(100, quota.RemainingPercent))
		percent := remainingPercentStyle(remaining).Render(fmt.Sprintf("%d%%", remaining))
		parts = append(parts, fmt.Sprintf("%s %s · %s", profileQuotaLabel(quota), percent, profileQuotaReset(quota)))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "  ")
}

func remainingPercentStyle(percent int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(remainingPercentColor(percent))
}

func profileQuotas(status *profilemgr.ProfileStatus) []profilemgr.ProfileQuota {
	if status == nil {
		return nil
	}
	if len(status.Quotas) > 0 {
		quotas := append([]profilemgr.ProfileQuota(nil), status.Quotas...)
		sort.SliceStable(quotas, func(i, j int) bool {
			if quotas[i].Monthly != quotas[j].Monthly {
				return !quotas[i].Monthly
			}
			return quotas[i].WindowDurationMins < quotas[j].WindowDurationMins
		})
		return quotas
	}
	if status.UsedPercent == nil || status.ResetsAt == nil {
		return nil
	}
	return []profilemgr.ProfileQuota{{
		WindowDurationMins: 300,
		RemainingPercent:   remainingPercent(*status.UsedPercent),
		ResetsAt:           status.ResetsAt.In(time.Local),
	}}
}

func formatProfileQuotas(quotas []profilemgr.ProfileQuota) string {
	parts := make([]string, 0, len(quotas))
	for _, quota := range quotas {
		if !displayableProfileQuota(quota) {
			continue
		}
		remaining := max(0, min(100, quota.RemainingPercent))
		parts = append(parts, fmt.Sprintf("%s %d%% · %s", profileQuotaLabel(quota), remaining, profileQuotaReset(quota)))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "  ")
}

func displayableProfileQuota(quota profilemgr.ProfileQuota) bool {
	return (quota.Monthly || quota.WindowDurationMins > 0) && !quota.ResetsAt.IsZero()
}

func profileQuotaReset(quota profilemgr.ProfileQuota) string {
	format := profileStatusTimestampFormat
	if !quota.Monthly && quota.WindowDurationMins == 300 {
		format = "15:04"
	}
	return quota.ResetsAt.In(time.Local).Format(format)
}

func profileQuotaLabel(quota profilemgr.ProfileQuota) string {
	if quota.Monthly {
		return "Monthly"
	}
	switch quota.WindowDurationMins {
	case 300:
		return "5h"
	case 10080:
		return "Weekly"
	}

	const (
		minutesPerHour = int64(60)
		minutesPerDay  = 24 * minutesPerHour
	)
	if quota.WindowDurationMins >= 28*minutesPerDay {
		return "Monthly"
	}
	if quota.WindowDurationMins%minutesPerDay == 0 {
		return fmt.Sprintf("%dd", quota.WindowDurationMins/minutesPerDay)
	}
	if quota.WindowDurationMins%minutesPerHour == 0 {
		return fmt.Sprintf("%dh", quota.WindowDurationMins/minutesPerHour)
	}
	return fmt.Sprintf("%dm", quota.WindowDurationMins)
}

func (m appModel) profileStatusColumnWidths() []int {
	headings := []string{"Limits", "State"}
	widths := make([]int, len(headings))
	for i, heading := range headings {
		widths[i] = lipgloss.Width(heading)
	}
	for _, p := range m.profiles {
		if p.Kind != profilemgr.AuthKindChatGPT {
			continue
		}
		for i, value := range m.profileStatusValues(p.Key) {
			widths[i] = max(widths[i], lipgloss.Width(value))
		}
	}
	for i, value := range m.profileStatusValues("") {
		widths[i] = max(widths[i], lipgloss.Width(value))
	}
	return widths
}

func renderProfileColumns(columns ...string) string {
	return strings.Join(columns, profileColumnSeparator.Render(" │ "))
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
	case profilemgr.PlanAPI:
		style = apiPlanStyle
	}
	return style.Render(label) + strings.Repeat(" ", planColumnWidth-lipgloss.Width(label))
}

func (m appModel) renderProfileCell(style lipgloss.Style, prefix, label string, profileColumnWidth int) string {
	paddingWidth := profileColumnWidth - lipgloss.Width(prefix+label)
	if paddingWidth < 0 {
		paddingWidth = 0
	}

	return style.Render(prefix+label) + strings.Repeat(" ", paddingWidth)
}

func (m appModel) profileColumnWidth() int {
	width := 0
	for i, p := range m.profiles {
		prefix := "  "
		if i == m.cursor {
			prefix = "» "
		}

		lineWidth := lipgloss.Width(prefix + p.Label)
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
