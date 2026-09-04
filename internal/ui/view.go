package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

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

const (
	profileStatusTimestampFormat = "02.01. 15:04"
	stateHeading                 = "State"
	stateColumnHeading           = "⚿"
)

func (m appModel) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	header := m.renderHeader(0)
	list := m.renderList()
	diagnostics := m.renderDiagnostics()
	footer := m.renderFooter()
	status := m.renderStatus()

	width := panelContentWidth(m.width, header, list, diagnostics)
	header = m.renderHeader(width)

	headerPanel := headerPanelStyle
	listPanel := panelStyle
	diagnosticsPanel := diagnosticsPanelStyle
	if width > 0 {
		// lipgloss Width includes border and padding.
		frame := panelStyle.GetHorizontalFrameSize()
		headerPanel = headerPanel.Width(width + frame)
		listPanel = listPanel.Width(width + frame)
		diagnosticsPanel = diagnosticsPanel.Width(width + frame)
	}

	parts := []string{headerPanel.Render(header), listPanel.Render(list)}
	if diagnostics != "" {
		parts = append(parts, diagnosticsPanel.Render(diagnostics))
	}
	parts = append(parts, "", footer, "", status)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return tea.NewView(baseStyle.Render(content))
}

// panelContentWidth computes the shared inner width for all panels so they
// stack with equal widths, clamped to the terminal width when known.
func panelContentWidth(terminalWidth int, bodies ...string) int {
	width := 0
	for _, body := range bodies {
		if body == "" {
			continue
		}
		width = max(width, lipgloss.Width(body))
	}
	if terminalWidth > 0 {
		width = min(width, max(20, terminalWidth-10))
	}
	return width
}

func (m appModel) renderHeader(width int) string {
	title := headerTitle.Render("Codex Auth Manager")
	if m.updateChecked && m.updateNotice != "" {
		title += "  " + currentTag.Render("●") + "  " + infoStyle.Render(m.updateNotice)
	}
	if m.appVersion != "" && width > 0 {
		version := versionStyle.Render(m.appVersion)
		if gap := width - lipgloss.Width(title) - lipgloss.Width(version); gap >= 2 {
			title += strings.Repeat(" ", gap) + version
		}
	}

	ruleWidth := width
	if ruleWidth <= 0 {
		ruleWidth = lipgloss.Width(title)
	}

	rows := []struct {
		label string
		value string
	}{
		{"Profile", m.headerProfileValue()},
		{stateHeading, m.headerStateValue()},
		{"Browser", headerDetail.Render(m.browserAuthStatus)},
		{"Directory", headerDetail.Render(m.profileManager.ProfileDir)},
	}
	labelWidth := 0
	for _, row := range rows {
		labelWidth = max(labelWidth, lipgloss.Width(row.label))
	}

	lines := make([]string, 0, len(rows)+2)
	lines = append(lines, title, dividerStyle.Render(strings.Repeat("─", ruleWidth)))
	for _, row := range rows {
		label := headerLabel.Render(row.label) + strings.Repeat(" ", labelWidth-lipgloss.Width(row.label)+2)
		lines = append(lines, label+row.value)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m appModel) headerProfileValue() string {
	if !m.authActive {
		return emptyStyle.Render("none")
	}
	current := m.currentAuth.Label
	if current == "" {
		current = "custom auth"
	}
	if m.currentProfileKey == "" {
		return headerValue.Render(current) + " " + headerDetail.Render("(unsaved)")
	}
	return headerValue.Render(current)
}

func (m appModel) headerStateValue() string {
	if !m.authActive {
		return inactiveTag.Render("○ logged out")
	}

	view, ok := m.currentChatGPTStatusView()
	if !ok {
		return headerDetail.Render("—")
	}

	state := statusDisplayStateFor(view)
	parts := []string{stateLabelStyle.Render(state.label())}
	if refreshedAt, ok := successfulFetchedAt(view); ok {
		parts = append(parts, headerDetail.Render("refreshed "+refreshedAt.In(time.Local).Format(profileStatusTimestampFormat)))
	}
	return strings.Join(parts, headerDetail.Render(" · "))
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
		return "…"
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

func (m appModel) renderList() string {
	if len(m.profiles) == 0 {
		return emptyStyle.Render("No saved profiles.")
	}

	profileColumnWidth := m.profileColumnWidth()
	planColumnWidth := m.planColumnWidth()
	lines := make([]string, 0, len(m.profiles)+1)
	lines = append(lines, m.renderProfileTableHeader(profileColumnWidth, planColumnWidth))
	for i, p := range m.profiles {
		style := itemStyle
		isCurrent := p.Key == m.currentProfileKey

		if isCurrent {
			style = activeItemStyle
		}

		if i == m.cursor {
			style = selectedItemStyle
		}

		lines = append(lines, m.renderProfileLineWithStatus(style, p, isCurrent, profileColumnWidth, planColumnWidth))
	}

	return strings.Join(lines, "\n")
}

func (m appModel) renderProfileTableHeader(profileColumnWidth, planColumnWidth int) string {
	profile := tableHeaderStyle.Render("Profile") + strings.Repeat(" ", max(0, profileColumnWidth-lipgloss.Width("Profile")))
	plan := tableHeaderStyle.Render("Plan") + strings.Repeat(" ", max(0, planColumnWidth-lipgloss.Width("Plan")))
	base := profile + "    " + plan
	headings := []string{"Limits", stateColumnHeading}
	widths := m.profileStatusColumnWidths()
	for i, heading := range headings {
		style := tableHeaderStyle
		if i == 1 {
			style = stateHeaderStyle
			heading += " "
		}
		headings[i] = style.Render(heading) + strings.Repeat(" ", widths[i]-lipgloss.Width(heading))
	}
	return renderProfileColumns(append([]string{base}, append(headings, tableHeaderStyle.Render("Note"))...)...)
}

func (m appModel) renderProfileLine(style lipgloss.Style, label, note string, plan profilemgr.Plan, isCurrent bool, profileColumnWidth int) string {
	profile := profilemgr.ProfileSummary{Label: label, Note: note, Plan: plan}
	return m.renderProfileLineWithStatus(style, profile, isCurrent, profileColumnWidth, m.planColumnWidth())
}

func (m appModel) renderProfileLineWithStatus(style lipgloss.Style, profile profilemgr.ProfileSummary, isCurrent bool, profileColumnWidth int, planColumnWidths ...int) string {
	planColumnWidth := m.planColumnWidth()
	if len(planColumnWidths) > 0 {
		planColumnWidth = planColumnWidths[0]
	}
	marker := " "
	if isCurrent {
		marker = currentTag.Render("●")
	}
	base := m.renderProfileCell(style, profile.Label, profileColumnWidth) + " " + marker + "  " + renderPlanCell(profile.Plan, planColumnWidth)
	separator := profileColumnSeparator.Render(" │ ")

	note := strings.TrimSpace(profile.Note)
	trailing := ""
	if note != "" {
		trailing = noteStyle.Render(note)
	}
	trailingStyle := lipgloss.NewStyle()
	if profile.Kind == profilemgr.AuthKindChatGPT {
		status := renderProfileColumns(m.renderProfileStatusColumns(profile.Key)...)
		if trailing != "" {
			trailing = status + separator + trailing
		} else {
			trailing = status
		}
		if m.profileStatuses[profile.Key].stale {
			trailingStyle = mutedStatusStyle
		}
	}
	if trailing == "" {
		return base
	}

	continuationIndent := "    "
	availableWidth := m.listContentWidth()
	if availableWidth <= 0 {
		return base + trailingStyle.Render(separator+trailing)
	}
	if lipgloss.Width(base)+lipgloss.Width(separator)+lipgloss.Width(trailing) <= availableWidth {
		return base + trailingStyle.Render(separator+trailing)
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

	line := base + trailingStyle.Render(separator+wrapped[0])
	for _, part := range wrapped[1:] {
		line += "\n" + trailingStyle.Render(continuationIndent+part)
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
		limits = m.formatProfileQuotas(profileQuotas(view.status))
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
	quotas := displayableQuotas(profileQuotas(view.status))
	if len(quotas) == 0 {
		return "-"
	}
	widths := m.quotaPercentWidths(quotas)
	parts := make([]string, 0, len(quotas))
	for i, quota := range quotas {
		remaining := max(0, min(100, quota.RemainingPercent))
		percent := remainingPercentStyle(remaining).Render(quotaPercentValue(remaining, widths[i]))
		parts = append(parts, fmt.Sprintf("%s %s · %s", profileQuotaLabel(quota), percent, profileQuotaReset(quota)))
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

func (m appModel) formatProfileQuotas(quotas []profilemgr.ProfileQuota) string {
	displayable := displayableQuotas(quotas)
	if len(displayable) == 0 {
		return "-"
	}
	widths := m.quotaPercentWidths(displayable)
	parts := make([]string, 0, len(displayable))
	for i, quota := range displayable {
		remaining := max(0, min(100, quota.RemainingPercent))
		parts = append(parts, fmt.Sprintf("%s %s · %s", profileQuotaLabel(quota), quotaPercentValue(remaining, widths[i]), profileQuotaReset(quota)))
	}
	return strings.Join(parts, "  ")
}

// quotaPercentValue renders a remaining percentage padded with trailing
// whitespace to the given digit width, so percentages across profiles line
// up by their leading digits (e.g. "88% " next to "100%").
func quotaPercentValue(remaining, width int) string {
	return fmt.Sprintf("%-*s", width+1, fmt.Sprintf("%d%%", remaining))
}

// quotaPercentWidths returns per-quota digit widths for the given displayable
// quotas, computed over profiles that share the same quota shape (identical
// labels in the same order). Shapes never align with each other visually, so
// they do not influence each other's padding (e.g. a lone monthly-only "0%"
// stays unpadded).
func (m appModel) quotaPercentWidths(quotas []profilemgr.ProfileQuota) []int {
	signature := quotaShapeSignature(quotas)
	widths := make([]int, len(quotas))
	for _, p := range m.profiles {
		if p.Kind != profilemgr.AuthKindChatGPT {
			continue
		}
		view := m.statusViewForProfile(p.Key)
		if view.status == nil || view.status.AuthStatus != profilemgr.ProfileAuthAuthenticated {
			continue
		}
		candidate := displayableQuotas(profileQuotas(view.status))
		if quotaShapeSignature(candidate) != signature {
			continue
		}
		for i, quota := range candidate {
			widths[i] = max(widths[i], percentDigitWidth(max(0, min(100, quota.RemainingPercent))))
		}
	}
	return widths
}

func quotaShapeSignature(quotas []profilemgr.ProfileQuota) string {
	labels := make([]string, 0, len(quotas))
	for _, quota := range quotas {
		labels = append(labels, profileQuotaLabel(quota))
	}
	return strings.Join(labels, ",")
}

func displayableQuotas(quotas []profilemgr.ProfileQuota) []profilemgr.ProfileQuota {
	displayable := make([]profilemgr.ProfileQuota, 0, len(quotas))
	for _, quota := range quotas {
		if displayableProfileQuota(quota) {
			displayable = append(displayable, quota)
		}
	}
	return displayable
}

func percentDigitWidth(remaining int) int {
	return len(strconv.Itoa(remaining))
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
	headings := []string{"Limits", stateColumnHeading}
	widths := make([]int, len(headings))
	for i, heading := range headings {
		widths[i] = lipgloss.Width(heading)
	}
	widths[1]++
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
	return renderPlanCell(plan, 4)
}

func renderPlanCell(plan profilemgr.Plan, planColumnWidth int) string {
	label := renderedPlanLabel(plan)
	style := unknownPlanStyle
	switch strings.ToLower(strings.TrimSpace(string(plan))) {
	case string(profilemgr.PlanPro), string(profilemgr.PlanProLite):
		style = proPlanStyle
	case string(profilemgr.PlanPlus):
		style = plusPlanStyle
	case string(profilemgr.PlanFree):
		style = freePlanStyle
	case string(profilemgr.PlanAPI):
		style = apiPlanStyle
	}
	return style.Render(label) + strings.Repeat(" ", max(0, planColumnWidth-lipgloss.Width(label)))
}

func renderedPlanLabel(plan profilemgr.Plan) string {
	label := plan.Label()
	if label == "—" {
		return label
	}
	label = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, label)
	if strings.TrimSpace(label) == "" {
		return "—"
	}
	return label
}

func (m appModel) renderProfileCell(style lipgloss.Style, label string, profileColumnWidth int) string {
	paddingWidth := profileColumnWidth - lipgloss.Width(label)
	if paddingWidth < 0 {
		paddingWidth = 0
	}

	return style.Render(label) + strings.Repeat(" ", paddingWidth)
}

func (m appModel) profileColumnWidth() int {
	width := 0
	for _, p := range m.profiles {
		lineWidth := lipgloss.Width(p.Label)
		if lineWidth > width {
			width = lineWidth
		}
	}
	return width
}

func (m appModel) planColumnWidth() int {
	width := lipgloss.Width("Plan")
	for _, p := range m.profiles {
		width = max(width, lipgloss.Width(renderedPlanLabel(p.Plan)))
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
		return confirmStyle.Render(m.confirmPrompt)
	case modeAuthenticating:
		return footerStyle.Render(formatKeyHint("esc", "cancel authentication"))
	case modeNormal:
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

		groups := []struct {
			name  string
			hints []string
		}{
			{"UI", []string{
				formatKeyHint("↑/↓", "move"),
				formatKeyHint("enter", "activate"),
			}},
			{"Manage", profileCommands},
			{"Global", []string{
				formatKeyHint("s", "save"),
				formatKeyHint("l", "logout"),
				formatKeyHint("ctrl+r", "refresh"),
				formatKeyHint("q", "quit"),
			}},
		}

		lines := make([]string, 0, len(groups))
		for _, group := range groups {
			label := footerGroupStyle.Render(group.name) + strings.Repeat(" ", 8-len(group.name))
			lines = append(lines, footerStyle.Render(label+strings.Join(group.hints, headerDetail.Render(" · "))))
		}
		return lipgloss.JoinVertical(lipgloss.Left, lines...)
	default:
		return ""
	}
}

func (m appModel) renderDiagnostics() string {
	if len(m.invalidProfiles) == 0 {
		return ""
	}

	lines := []string{
		warningStyle.Render(fmt.Sprintf("⚠ Ignored %d invalid profile file(s):", len(m.invalidProfiles))),
	}
	for i, issue := range m.invalidProfiles {
		if i == 3 {
			lines = append(lines, headerDetail.Render(fmt.Sprintf("...and %d more", len(m.invalidProfiles)-i)))
			break
		}
		lines = append(lines, fmt.Sprintf("%s %s", headerValue.Render(issue.Name), headerDetail.Render(issue.Reason)))
	}

	return strings.Join(lines, "\n")
}

func (m appModel) renderStatus() string {
	if m.errText != "" {
		return errorStyle.Render("✗ " + m.errText)
	}
	if m.status == "" {
		return ""
	}
	s := statusStyle.Render("✓ " + m.status)
	if m.statusKind == statusInfo {
		s = infoStyle.Render("ℹ " + m.status)
	}
	if m.restartRequired {
		s += headerDetail.Render(" (Restart Codex to apply)")
	}
	return s
}
