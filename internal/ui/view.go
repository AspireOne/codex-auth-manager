package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	profilemgr "codex-manage/internal/profiles"
)

type statusDisplayState int

const (
	statusDisplayCached statusDisplayState = iota
	statusDisplayLoading
	statusDisplayFailed
)

const profileStatusTimestampFormat = "02.01. 15:04"

// Responsive layout breakpoints, based on the terminal width. width <= 0
// means the size is not known yet (before the first WindowSizeMsg); the view
// then renders in wide layout without any truncation guard.
type layoutMode int

const (
	layoutWide layoutMode = iota
	layoutCompact
	layoutMini
)

const (
	layoutWideMin    = 100
	layoutCompactMin = 60
	// fallbackWidth is used for layout decisions when the terminal size is
	// not known yet (tests, first frame).
	fallbackWidth = 120
	// defaultPanelContentW mirrors fallbackWidth for width==0 rendering.
	defaultPanelContentW = fallbackWidth - 10
)

func layoutForWidth(width int) layoutMode {
	if width <= 0 {
		return layoutWide
	}
	switch {
	case width >= layoutWideMin:
		return layoutWide
	case width >= layoutCompactMin:
		return layoutCompact
	default:
		return layoutMini
	}
}

// panelContentWidth is the text width available inside the outer panel:
// baseStyle padding (2+2) and the outer panel border+padding (2+4) are chrome.
func panelContentWidth(width int) int {
	if width <= 0 {
		return defaultPanelContentW
	}
	w := width - 10
	if w < 24 {
		w = 24
	}
	return w
}

func (m appModel) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	gridW := panelContentWidth(m.width)

	header := m.renderHeader()
	footer := m.renderFooter()
	status := m.renderStatus()
	diagnostics := m.renderDiagnostics()

	budget := m.profilesBudget(header, footer, diagnostics)
	list := m.renderList(gridW, budget)

	// Style Width is the total width including border and padding.
	panel := outerPanelStyle.Width(gridW + outerPanelStyle.GetHorizontalFrameSize()).Render(header + "\n\n" + list)
	panel = applyBorderTitle(panel, "Codex Auth Manager")

	parts := []string{panel}
	if diagnostics != "" {
		parts = append(parts, diagnostics)
	}
	parts = append(parts, footer, status)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	out := baseStyle.Render(content)
	if m.width > 0 {
		out = truncateLines(out, m.width)
	}
	return tea.NewView(out)
}

// truncateLines hard-cuts every line to width cells as a final safety net so
// no line can ever exceed the terminal width.
func truncateLines(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if lipgloss.Width(line) > width {
			lines[i] = ansi.Truncate(line, width, "")
		}
	}
	return strings.Join(lines, "\n")
}

// applyBorderTitle replaces the plain top border of a rendered bordered block
// with a `╭─ title ──...──╮` line.
func applyBorderTitle(block, title string) string {
	lines := strings.Split(block, "\n")
	if len(lines) == 0 {
		return block
	}
	width := lipgloss.Width(lines[0])
	borderStyle := lipgloss.NewStyle().Foreground(panelBorder)
	// ╭─ (2) + space (1) + title + space (1) + dashes + ╮ (1)
	dashes := width - lipgloss.Width(title) - 6
	if dashes < 2 {
		return block
	}
	lines[0] = borderStyle.Render("╭─ ") + headerTitle.Render(title) + borderStyle.Render(" "+strings.Repeat("─", dashes)+"╮")
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

type headerField struct {
	icon  string
	label string
	value string
}

func (m appModel) renderHeader() string {
	return m.renderHeaderGrid(panelContentWidth(m.width))
}

func (m appModel) renderHeaderGrid(gridW int) string {
	current := "none"
	if m.authActive {
		current = m.currentAuth.Label
		if m.currentProfileKey == "" {
			if current == "" {
				current = "custom auth"
			}
			current += " (unsaved)"
		}
	}

	status := footerStyle.Render("logged out")
	if m.authActive {
		status = currentTag.Render("active")
	}

	stateIcon := mutedStatusStyle.Render(icons.unknown)
	stateValue := mutedStatusStyle.Render("—")
	if view, ok := m.currentChatGPTStatusView(); ok {
		state := statusDisplayStateFor(view)
		lastRefreshed := "never"
		if refreshedAt, ok := successfulFetchedAt(view); ok {
			lastRefreshed = refreshedAt.In(time.Local).Format(profileStatusTimestampFormat)
		}
		stateIcon = state.style().Render(state.icon())
		stateValue = footerStyle.Render(fmt.Sprintf("%s • last refreshed at %s", state.label(), lastRefreshed))
	}

	updateValue := mutedStatusStyle.Render("not checked")
	if m.updateChecked {
		updateValue = footerStyle.Render("up to date")
		if m.updateNotice != "" {
			updateValue = infoStyle.Render(m.updateNotice)
		}
	}

	fields := []headerField{
		{mutedStatusStyle.Render(icons.current), "Current profile", headerValue.Render(current)},
		{mutedStatusStyle.Render(icons.status), "Profile status", status},
		{stateIcon, "State", stateValue},
		{mutedStatusStyle.Render(icons.update), "Update", updateValue},
		{mutedStatusStyle.Render(icons.browser), "Browser auth", footerStyle.Render(m.browserAuthStatus)},
		{mutedStatusStyle.Render(icons.dir), "Profile dir", mutedStatusStyle.Render(m.profileManager.ProfileDir)},
	}

	labelW := 0
	for _, f := range fields {
		if w := lipgloss.Width(f.icon) + 1 + lipgloss.Width(f.label); w > labelW {
			labelW = w
		}
	}

	chip := func(f headerField, width int) string {
		valueW := width - labelW - 7
		if valueW < 4 {
			valueW = 4
		}
		value := f.value
		if lipgloss.Width(value) > valueW {
			value = ansi.Truncate(value, valueW, "…")
		}
		value += strings.Repeat(" ", valueW-lipgloss.Width(value))
		bar := separatorStyle.Render("│")
		label := f.icon + " " + chipLabelStyle.Render(f.label)
		label += strings.Repeat(" ", labelW-lipgloss.Width(f.icon)-1-lipgloss.Width(f.label))
		return bar + " " + label + " " + bar + " " + chipValueStyle.Render(value) + " " + bar
	}

	twoColContentW := 2*(labelW+7+16) + 1
	var lines []string
	if gridW >= twoColContentW {
		cellW := (gridW - 1) / 2
		fullW := gridW // long-value fields get a full-width row
		row := func(a, b headerField) string { return chip(a, cellW) + " " + chip(b, cellW) }
		lines = append(lines,
			row(fields[0], fields[4]), // current profile | browser auth
			row(fields[1], fields[3]), // profile status | update
			chip(fields[2], fullW),    // state
			chip(fields[5], fullW),    // profile dir
		)
	} else {
		for _, f := range fields {
			lines = append(lines, chip(f, gridW))
		}
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Profiles section (cards)
// ---------------------------------------------------------------------------

// renderList renders the "Profiles" divider and the visible window of profile
// cards fitting into budget rows.
func (m appModel) renderList(width, budget int) string {
	divider := renderSectionDivider("Profiles", width)
	if len(m.profiles) == 0 {
		return divider + "\n" + emptyStyle.Render("No saved profiles.")
	}

	layout := layoutForWidth(m.width)
	cards := make([]string, len(m.profiles))
	heights := make([]int, len(m.profiles))
	for i := range m.profiles {
		cards[i] = m.renderProfileCard(i, layout, width)
		// + 1 for the blank gap between cards (uniform for window math).
		heights[i] = lipgloss.Height(cards[i]) + 1
	}

	start, end := visibleCardWindow(heights, m.cursor, budget)
	var blocks []string
	if start > 0 {
		blocks = append(blocks, "  "+scrollIndicatorStyle.Render(fmt.Sprintf("%s %d more", icons.moreUp, start)))
	}
	for i := start; i < end; i++ {
		blocks = append(blocks, cards[i])
	}
	if end < len(m.profiles) {
		blocks = append(blocks, "  "+scrollIndicatorStyle.Render(fmt.Sprintf("%s %d more", icons.moreDown, len(m.profiles)-end)))
	}
	return divider + "\n" + strings.Join(blocks, "\n\n")
}

func renderSectionDivider(title string, width int) string {
	prefix := "─ " + title + " "
	dashes := width - lipgloss.Width(prefix)
	if dashes < 0 {
		dashes = 0
	}
	return dividerStyle.Render("─ ") + sectionTitleStyle.Render(title) + dividerStyle.Render(" "+strings.Repeat("─", dashes))
}

// visibleCardWindow returns the [start, end) window of cards (heights include
// inter-card gaps) that fits budget rows while keeping cursor visible.
func visibleCardWindow(heights []int, cursor, budget int) (int, int) {
	n := len(heights)
	if n == 0 {
		return 0, 0
	}
	total := 0
	for _, h := range heights {
		total += h
	}
	if total <= budget {
		return 0, n
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= n {
		cursor = n - 1
	}
	start, end := cursor, cursor+1
	used := heights[cursor]
	for {
		// Indicator rows cost one row each side that is clipped.
		if end < n {
			need := used + heights[end]
			if start > 0 {
				need++
			}
			if end+1 < n {
				need++
			}
			if need <= budget {
				used += heights[end]
				end++
				continue
			}
		}
		if start > 0 {
			need := used + heights[start-1]
			if start-1 > 0 {
				need++
			}
			if end < n {
				need++
			}
			if need <= budget {
				start--
				used += heights[start]
				continue
			}
		}
		break
	}
	return start, end
}

// profilesBudget is the number of rows available for the cards window.
func (m appModel) profilesBudget(header, footer, diagnostics string) int {
	if m.height <= 0 {
		return 1 << 30
	}
	reserved := 2 /* baseStyle vertical padding */ +
		2 /* outer panel border */ +
		lipgloss.Height(header) +
		1 /* blank between header and divider */ +
		1 /* divider */ +
		lipgloss.Height(footer) +
		1 /* status line */
	if diagnostics != "" {
		reserved += lipgloss.Height(diagnostics)
	}
	budget := m.height - reserved
	if budget < 3 {
		budget = 3
	}
	return budget
}

// cardContentWidth is the text width inside a profile card (excluding gutter,
// card border and card padding).
func cardContentWidth(panelW int, layout layoutMode) int {
	w := panelW - 2 /* gutter */ - 2 /* border */ - 2 /* padding */
	if w < 12 {
		w = 12
	}
	return w
}

func (m appModel) renderProfileCard(i int, layout layoutMode, panelW int) string {
	p := m.profiles[i]
	selected := i == m.cursor
	inner := cardContentWidth(panelW, layout)

	lines := m.profileCardLines(p, layout, inner)

	style := cardStyle
	if selected {
		style = selectedCardStyle
	}
	// Width is the total card width: inner content + 2 border + 2 padding.
	block := style.Width(inner + 4).Render(strings.Join(lines, "\n"))

	rendered := strings.Split(block, "\n")
	for j := range rendered {
		gutter := "  "
		if selected && j == 1 { // mark the first content row, below the top border
			gutter = headerTitle.Render(icons.cursor) + " "
		}
		rendered[j] = gutter + rendered[j]
	}
	return strings.Join(rendered, "\n")
}

// profileCardLines builds the fully padded content lines of a single profile
// card (without border/gutter).
func (m appModel) profileCardLines(p profilemgr.ProfileSummary, layout layoutMode, inner int) []string {
	title := m.renderCardTitle(p, layout, inner)
	var quotaLines []string
	var note string

	if p.Kind == profilemgr.AuthKindChatGPT {
		quotaLines = m.renderQuotaLines(m.statusViewForProfile(p.Key))
		note = strings.TrimSpace(p.Note)
	} else {
		note = strings.TrimSpace(p.Note)
	}

	// Wide layout: note lives in a right-hand column, wrapped without a cap
	// so no note text is ever hidden.
	if layout == layoutWide && note != "" {
		// Right zone: state icon + " │ " separator + wrapped note.
		icon := m.renderCardStateIcon(p)
		sep := separatorStyle.Render(" │ ")
		rightPrefixW := lipgloss.Width(icon) + lipgloss.Width(sep)
		noteW := inner/4 + 4
		if noteW < 12 {
			noteW = 12
		}
		if max := inner - 56; noteW > max && max >= 12 {
			noteW = max
		}
		leftW := inner - rightPrefixW - noteW - 2 // 2 for the gap before the icon
		var noteLines []string
		for _, nl := range wrapWords(note, noteW, noteW) {
			noteLines = append(noteLines, m.renderNoteText(p, nl))
		}
		leftLines := []string{truncateCell(padRight(title, leftW), leftW)}
		for _, q := range quotaLines {
			leftLines = append(leftLines, "    "+truncateCell(q, leftW-4))
		}
		rightLines := make([]string, len(noteLines))
		for j, nl := range noteLines {
			if j == 0 {
				rightLines[j] = icon + sep + nl
			} else {
				rightLines[j] = strings.Repeat(" ", rightPrefixW) + nl
			}
		}
		rows := max(len(leftLines), len(rightLines))
		out := make([]string, 0, rows)
		for r := 0; r < rows; r++ {
			var l, rr string
			if r < len(leftLines) {
				l = padRight(leftLines[r], leftW)
			} else {
				l = strings.Repeat(" ", leftW)
			}
			if r < len(rightLines) {
				rr = rightLines[r]
			}
			out = append(out, truncateCell(l+"  "+rr, inner))
		}
		return out
	}

	lines := []string{truncateCell(m.placeCardState(title, p, inner), inner)}
	indent := ""
	if layout != layoutMini {
		indent = "    "
	}
	for _, q := range quotaLines {
		lines = append(lines, indent+truncateCell(q, inner-lipgloss.Width(indent)))
	}
	if note != "" {
		for _, nl := range wrapWords(note, inner-lipgloss.Width(indent), inner-lipgloss.Width(indent)) {
			lines = append(lines, indent+m.renderNoteText(p, nl))
		}
	}
	return lines
}

// renderCardTitle renders avatar + label + active dot + plan pill, without the
// trailing state icon.
func (m appModel) renderCardTitle(p profilemgr.ProfileSummary, layout layoutMode, inner int) string {
	var parts []string
	if layout != layoutMini {
		parts = append(parts, m.renderAvatar(p))
	}
	label := p.Label
	labelStyle := itemStyle
	if indexOfProfile(m.profiles, p.Key) == m.cursor {
		labelStyle = lipgloss.NewStyle().Bold(true)
	}
	parts = append(parts, labelStyle.Render(label))
	if p.Key == m.currentProfileKey && m.authActive {
		parts = append(parts, currentTag.Render(icons.activeDot))
	}
	parts = append(parts, renderPlan(p.Plan))
	title := strings.Join(parts, " ")
	if lipgloss.Width(title) > inner {
		// Truncate the label rather than the pill/state when cramped.
		// The label is right after the optional avatar part.
		idx := 0
		if layout != layoutMini {
			idx = 1
		}
		labelW := lipgloss.Width(label) - (lipgloss.Width(title) - inner)
		if labelW < 1 {
			labelW = 1
		}
		parts[idx] = labelStyle.Render(ansi.Truncate(label, labelW, "…"))
		title = strings.Join(parts, " ")
	}
	return title
}

// placeCardState right-aligns the ChatGPT state icon at the end of the title
// row for layouts without a dedicated note column.
func (m appModel) placeCardState(title string, p profilemgr.ProfileSummary, inner int) string {
	icon := m.renderCardStateIcon(p)
	gap := inner - lipgloss.Width(title) - lipgloss.Width(icon)
	if gap < 1 {
		gap = 1
	}
	return title + strings.Repeat(" ", gap) + icon
}

// renderCardStateIcon renders the ✓/?/✗ status icon for ChatGPT profiles and
// an empty-width string for other kinds.
func (m appModel) renderCardStateIcon(p profilemgr.ProfileSummary) string {
	if p.Kind != profilemgr.AuthKindChatGPT {
		return ""
	}
	state := statusDisplayStateFor(m.statusViewForProfile(p.Key))
	return state.style().Render(state.icon())
}

func (m appModel) renderAvatar(p profilemgr.ProfileSummary) string {
	selected := indexOfProfile(m.profiles, p.Key) == m.cursor
	if p.Kind == profilemgr.AuthKindAPIKey {
		return avatarAPIStyle.Width(3).Render(icons.apiAvatar)
	}
	initial := "?"
	for _, r := range p.Label {
		initial = strings.ToUpper(string(r))
		break
	}
	style := avatarStyle
	if selected {
		style = avatarSelectedStyle
	}
	return style.Width(3).Align(lipgloss.Center).Render(initial)
}

// renderQuotaLines renders one line per displayable quota, e.g.
// `5h 90% · 16:03`, heat-colored by remaining percent. Stale data renders
// faint, matching the previous table behavior.
func (m appModel) renderQuotaLines(view profileStatusView) []string {
	if view.status == nil || view.status.AuthStatus != profilemgr.ProfileAuthAuthenticated {
		return nil
	}
	quotas := profileQuotas(view.status)
	lines := make([]string, 0, len(quotas))
	for _, quota := range quotas {
		if !displayableProfileQuota(quota) {
			continue
		}
		remaining := max(0, min(100, quota.RemainingPercent))
		percent := remainingPercentStyle(remaining).Render(fmt.Sprintf("%d%%", remaining))
		line := fmt.Sprintf("%s %s · %s", profileQuotaLabel(quota), percent, profileQuotaReset(quota))
		if view.stale {
			line = mutedStatusStyle.Render(fmt.Sprintf("%s %d%% · %s", profileQuotaLabel(quota), remaining, profileQuotaReset(quota)))
		}
		lines = append(lines, line)
	}
	return lines
}

// renderNoteText styles one note line; stale ChatGPT data renders faint.
// Style after wrapping so ANSI sequences never split across lines.
func (m appModel) renderNoteText(p profilemgr.ProfileSummary, line string) string {
	if p.Kind == profilemgr.AuthKindChatGPT && m.profileStatuses[p.Key].stale {
		return mutedStatusStyle.Render(line)
	}
	return footerStyle.Render(line)
}

// padRight pads s with spaces to width cells.
func padRight(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

// truncateCell hard-cuts s to width cells (no ellipsis).
func truncateCell(s string, width int) string {
	if width < 1 {
		width = 1
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "")
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
		return icons.ok
	case statusDisplayLoading:
		return icons.unknown
	case statusDisplayFailed:
		return icons.bad
	default:
		return icons.bad
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

// profileStatusValues returns the quota summary and styled state icon for a
// profile key.
func (m appModel) profileStatusValues(key string) []string {
	view := m.statusViewForProfile(key)
	limits := "-"
	if view.status != nil && view.status.AuthStatus == profilemgr.ProfileAuthAuthenticated {
		limits = formatProfileQuotas(profileQuotas(view.status))
	}
	state := statusDisplayStateFor(view)
	return []string{limits, state.style().Render(state.icon())}
}

func remainingPercent(used int) int {
	return max(0, min(100, 100-used))
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

func renderPlan(plan profilemgr.Plan) string {
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
	return style.Render(label)
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

// ---------------------------------------------------------------------------
// Footer, diagnostics, status
// ---------------------------------------------------------------------------

func (m appModel) renderFooter() string {
	gridW := panelContentWidth(m.width)
	// Width is total (border 2 + padding 2): content width is gridW + 2.
	return footerPanelStyle.Width(gridW + 6).Render(m.renderFooterContent(gridW + 2))
}

func (m appModel) renderFooterContent(width int) string {
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

		uiLine := footerStyle.Render("UI: " + strings.Join(navigationCommands, " • "))
		manageLine := footerStyle.Render("Manage: " + strings.Join(profileCommands, " • "))
		globalLine := footerStyle.Render("Global: " + strings.Join(globalCommands, " • "))

		if m.width > 0 {
			gap := width - lipgloss.Width(uiLine) - lipgloss.Width(globalLine)
			if gap >= 4 {
				return uiLine + strings.Repeat(" ", gap) + globalLine + "\n" + manageLine
			}
		}
		// Mini terminals: wrap hint lines instead of dropping keybinds.
		// Styling falls back to plain footer text on wrapped lines.
		plain := []string{
			"UI: " + strings.Join(navigationCommands, " • "),
			"Manage: " + strings.Join(profileCommands, " • "),
			"Global: " + strings.Join(globalCommands, " • "),
		}
		var lines []string
		for i, line := range plain {
			if m.width > 0 && lipgloss.Width(line) > width-2 {
				for _, w := range wrapWords(line, width-2, width-2) {
					lines = append(lines, footerStyle.Render(w))
				}
			} else {
				lines = append(lines, []string{uiLine, manageLine, globalLine}[i])
			}
		}
		return strings.Join(lines, "\n")
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

	return panelStyle.Width(panelContentWidth(m.width) + panelStyle.GetHorizontalFrameSize()).Render(strings.Join(lines, "\n"))
}

func (m appModel) renderStatus() string {
	if m.errText != "" {
		return errorStyle.Render(icons.bad + " Error: " + m.errText)
	}
	if m.status == "" {
		return ""
	}
	style := statusStyle
	icon := icons.ok + " "
	if m.statusKind == statusInfo {
		style = infoStyle
		icon = icons.cursor + " "
	}
	s := style.Render(icon + m.status)
	if m.restartRequired {
		s += lipgloss.NewStyle().Foreground(mutedColor).Render(" (Restart Codex to apply)")
	}
	return s
}
