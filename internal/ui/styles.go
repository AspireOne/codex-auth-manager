package ui

import lipgloss "charm.land/lipgloss/v2"

var (
	accentColor  = lipgloss.Color("#8B5CF6")
	accentSoft   = lipgloss.Color("#C4B5FD")
	successColor = lipgloss.Color("#10B981")
	infoColor    = lipgloss.Color("#38BDF8")
	errorColor   = lipgloss.Color("#F87171")
	warningColor = lipgloss.Color("#FBBF24")
	proColor     = lipgloss.Color("#F59E0B")
	plusColor    = lipgloss.Color("#FBBF24")
	freeColor    = lipgloss.Color("#FDE047")
	mutedColor   = lipgloss.Color("#94A3B8")
	panelBorder  = lipgloss.Color("#A78BFA")
	headerTitle  = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	headerValue  = lipgloss.NewStyle().Bold(true).Foreground(accentSoft)
	currentTag   = lipgloss.NewStyle().Foreground(successColor).Bold(true)
	baseStyle    = lipgloss.NewStyle().
			Padding(1, 2)

	panelStyle = lipgloss.NewStyle().
			BorderForeground(panelBorder).
			Border(lipgloss.RoundedBorder()).
			Padding(1, 2)

	itemStyle = lipgloss.NewStyle()

	selectedItemStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(accentColor)

	proPlanStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(proColor)

	plusPlanStyle = lipgloss.NewStyle().
			Foreground(plusColor)

	freePlanStyle = lipgloss.NewStyle().
			Foreground(freeColor)

	footerStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	profileColumnSeparator = lipgloss.NewStyle().
				Foreground(mutedColor)

	mutedStatusStyle = lipgloss.NewStyle().
				Foreground(mutedColor).
				Faint(true)

	keyHintStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(mutedColor)

	statusStyle = lipgloss.NewStyle().
			Foreground(successColor).
			Bold(true)

	infoStyle = lipgloss.NewStyle().
			Foreground(infoColor).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(errorColor)

	warningStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(warningColor)

	emptyStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)
)
