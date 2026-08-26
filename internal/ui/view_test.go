package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	lipgloss "charm.land/lipgloss/v2"

	profilemgr "codex-manage/internal/profiles"
)

// demoModel builds a model resembling a realistic mixed-profile setup.
func demoModel(width, height int) appModel {
	fiveHourReset := time.Date(2026, 8, 26, 16, 3, 0, 0, time.Local)
	weeklyReset := time.Date(2026, 9, 1, 16, 14, 0, 0, time.Local)
	m := appModel{
		width:             width,
		height:            height,
		authActive:        true,
		currentProfileKey: "mail3",
		currentAuth:       profilemgr.AuthSummary{Kind: profilemgr.AuthKindChatGPT, Label: "mail3@matejpesl.cz"},
		updateChecked:     true,
		browserAuthStatus: "Brave · isolated profiles",
		profiles: []profilemgr.ProfileSummary{
			{Key: "mail3", Label: "mail3@matejpesl.cz", Kind: profilemgr.AuthKindChatGPT, Plan: profilemgr.PlanPlus, Note: "tabbed cmg"},
			{Key: "xprod", Label: "matej.pesl@xproduction.cz", Kind: profilemgr.AuthKindChatGPT, Plan: profilemgr.PlanPlus, Note: "ends 16.9."},
			{Key: "gmail", Label: "matejpesl1@gmail.com", Kind: profilemgr.AuthKindChatGPT, Plan: profilemgr.PlanPlus},
			{Key: "mail2", Label: "mail2@matejpesl.cz", Kind: profilemgr.AuthKindChatGPT, Plan: profilemgr.PlanFree},
			{Key: "mail4", Label: "mail4@matejpesl.cz", Kind: profilemgr.AuthKindChatGPT, Plan: profilemgr.PlanFree, Note: "phone required"},
			{Key: "api", Label: "API key · f8f7c244", Kind: profilemgr.AuthKindAPIKey, Plan: profilemgr.PlanAPI},
		},
		profileStatuses: map[string]profileStatusView{
			"mail3": {status: &profilemgr.ProfileStatus{
				FetchedAt: fiveHourReset, AuthStatus: profilemgr.ProfileAuthAuthenticated,
				Quotas: []profilemgr.ProfileQuota{
					{WindowDurationMins: 300, RemainingPercent: 90, ResetsAt: fiveHourReset},
					{WindowDurationMins: 10080, RemainingPercent: 89, ResetsAt: weeklyReset},
				}}, phase: statusCached},
			"xprod": {status: &profilemgr.ProfileStatus{
				FetchedAt: fiveHourReset, AuthStatus: profilemgr.ProfileAuthAuthenticated,
				Quotas: []profilemgr.ProfileQuota{
					{WindowDurationMins: 300, RemainingPercent: 100, ResetsAt: fiveHourReset},
					{WindowDurationMins: 10080, RemainingPercent: 100, ResetsAt: weeklyReset},
				}}, phase: statusCached},
			"gmail": {status: &profilemgr.ProfileStatus{
				FetchedAt: fiveHourReset, AuthStatus: profilemgr.ProfileAuthAuthenticated,
				Quotas: []profilemgr.ProfileQuota{
					{WindowDurationMins: 300, RemainingPercent: 99, ResetsAt: fiveHourReset},
				}}, phase: statusCached},
			"mail2": {phase: statusFailed},
			"mail4": {status: &profilemgr.ProfileStatus{AuthStatus: profilemgr.ProfileAuthSignInRequired}, phase: statusCached},
		},
	}
	m.profileManager.ProfileDir = "/home/matej/.codex/auth_manager/profiles"
	return m
}

// TestViewNeverExceedsTerminalWidth is the regression guard for the original
// bug: at no terminal width may a rendered line overflow.
func TestViewNeverExceedsTerminalWidth(t *testing.T) {
	for _, width := range []int{140, 120, 100, 90, 80, 70, 60, 50, 44, 36, 30} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			m := demoModel(width, 50)
			view := m.View().Content
			for _, line := range strings.Split(view, "\n") {
				if got := lipgloss.Width(line); got > width {
					t.Fatalf("line exceeds width %d (got %d): %q", width, got, stripANSI(line))
				}
			}
		})
	}
}

// TestViewKeepsAllInformationAtWideWidth asserts information parity with the
// old layout at a wide terminal width.
func TestViewKeepsAllInformationAtWideWidth(t *testing.T) {
	m := demoModel(130, 60)
	view := stripANSI(m.View().Content)
	for _, want := range []string{
		"Codex Auth Manager",
		"Current profile", "mail3@matejpesl.cz",
		"Profile status", "active",
		"State", "Cached • last refreshed at 26.08. 16:03",
		"Update", "up to date",
		"Browser auth", "Brave · isolated profiles",
		"Profile dir", "/home/matej/.codex/auth_manager/profiles",
		"Profiles",
		"mail3@matejpesl.cz", "Plus", "tabbed cmg",
		"5h 90% · 16:03", "Weekly 89% · 01.09. 16:14",
		"phone required",
		"API key · f8f7c244",
		"UI:", "Manage:", "Global:",
		"enter", "activate", "p", "cycle plan",
		"n", "edit note", "a", "authenticate", "d", "delete",
		"s", "save", "l", "logout", "ctrl+r", "refresh", "q", "quit",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

// TestViewKeepsAllInformationAtNarrowWidth asserts the same information is
// still present below both breakpoints, in collapsed form.
func TestViewKeepsAllInformationAtNarrowWidth(t *testing.T) {
	for _, width := range []int{70, 50} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			m := demoModel(width, 80)
			view := stripANSI(m.View().Content)
			for _, want := range []string{
				"mail3@matejpesl.cz", "Plus", "tabbed cmg",
				"5h 90% · 16:03", "Weekly 89% · 01.09. 16:14",
				"phone required", "API key · f8f7c244",
				"enter", "activate", "save", "quit",
			} {
				if !strings.Contains(view, want) {
					t.Fatalf("view missing %q at width %d:\n%s", want, width, view)
				}
			}
		})
	}
}

// TestCardWindowScrollsWhenHeightIsConstrained verifies the viewport behavior:
// cards that do not fit are replaced by scroll indicators while keeping all
// information reachable and never exceeding the terminal height.
func TestCardWindowScrollsWhenHeightIsConstrained(t *testing.T) {
	m := demoModel(120, 24)
	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "more") {
		t.Fatalf("short view missing scroll indicator:\n%s", view)
	}
	lines := strings.Split(m.View().Content, "\n")
	if got := len(lines); got > 24 {
		t.Fatalf("view height = %d rows, want at most 24:\n%s", got, strings.Join(lines, "\n"))
	}

	// Cursor at the last profile: the window must follow the cursor.
	m.cursor = len(m.profiles) - 1
	view = stripANSI(m.View().Content)
	if !strings.Contains(view, "API key · f8f7c244") {
		t.Fatalf("window did not follow cursor to last profile:\n%s", view)
	}
	if !strings.Contains(view, icons.moreUp) {
		t.Fatalf("window missing top scroll indicator:\n%s", view)
	}
}

func TestVisibleCardWindowKeepsCursorVisible(t *testing.T) {
	heights := []int{6, 6, 4, 4, 4, 4}
	tests := []struct {
		cursor int
		budget int
	}{
		{0, 100}, {5, 100}, {2, 11}, {5, 11}, {3, 7}, {0, 4}, {5, 4},
	}
	for _, test := range tests {
		start, end := visibleCardWindow(heights, test.cursor, test.budget)
		if test.cursor < start || test.cursor >= end {
			t.Fatalf("cursor %d outside window [%d,%d)", test.cursor, start, end)
		}
		total := 0
		for i := start; i < end; i++ {
			total += heights[i]
		}
		if start > 0 {
			total++
		}
		if end < len(heights) {
			total++
		}
		if end-start > 1 && total > test.budget {
			t.Fatalf("window [%d,%d) exceeds budget %d (used %d)", start, end, test.budget, total)
		}
	}
}

// TestMiniLayoutDropsAvatar documents the narrow behavior: below the compact
// breakpoint the avatar tile disappears to reclaim columns.
func TestMiniLayoutDropsAvatar(t *testing.T) {
	m := demoModel(50, 80)
	card := stripANSI(m.renderProfileCard(0, layoutMini, panelContentWidth(50)))
	if strings.Contains(card, " M ") {
		t.Fatalf("mini layout should not render the avatar tile:\n%s", card)
	}
	if !strings.Contains(card, "mail3@matejpesl.cz") {
		t.Fatalf("mini layout lost the label:\n%s", card)
	}
}

// TestWideLayoutPlacesNoteInRightColumn and its column alignment.
func TestWideLayoutPlacesNoteInRightColumn(t *testing.T) {
	m := demoModel(130, 80)
	rendered := stripANSI(m.renderList(panelContentWidth(130), 1<<30))
	var firstIdx, secondIdx = -1, -1
	for _, line := range strings.Split(rendered, "\n") {
		if idx := strings.Index(line, "tabbed cmg"); idx >= 0 {
			firstIdx = lipgloss.Width(line[:idx])
		}
		if idx := strings.Index(line, "ends 16.9."); idx >= 0 {
			secondIdx = lipgloss.Width(line[:idx])
		}
	}
	if firstIdx < 0 || secondIdx < 0 {
		t.Fatalf("wide layout missing right-column notes:\n%s", rendered)
	}
	if firstIdx != secondIdx {
		t.Fatalf("note columns misaligned: %d vs %d\n%s", firstIdx, secondIdx, rendered)
	}
	// The title line carries the note: the note is a column, not a sub-line.
	if strings.Contains(rendered, "    tabbed cmg") {
		t.Fatalf("wide layout rendered the note as a sub-line:\n%s", rendered)
	}
}
