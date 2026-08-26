package ui

import (
	"image/color"

	lipgloss "charm.land/lipgloss/v2"
)

var (
	remainingEmptyColor   = color.RGBA{R: 255, G: 144, B: 144, A: 255}
	remainingCautionColor = color.RGBA{R: 254, G: 215, B: 170, A: 255}
	remainingFullColor    = color.RGBA{R: 187, G: 247, B: 208, A: 255}
)

var (
	accentColor  = lipgloss.Color("#8B5CF6")
	accentSoft   = lipgloss.Color("#C4B5FD")
	successColor = lipgloss.Color("#34D399")
	infoColor    = lipgloss.Color("#38BDF8")
	errorColor   = lipgloss.Color("#FB7185")
	warningColor = lipgloss.Color("#FBBF24")
	proColor     = lipgloss.Color("#F59E0B")
	plusColor    = lipgloss.Color("#FBBF24")
	freeColor    = lipgloss.Color("#E6DFA9")
	apiColor     = lipgloss.Color("#F5D0A9")
	mutedColor   = lipgloss.Color("#94A3B8")
	brightColor  = lipgloss.Color("#E2E8F0")

	headerBorderColor      = lipgloss.Color("#A78BFA")
	listBorderColor        = lipgloss.Color("#6366F1")
	diagnosticsBorderColor = warningColor

	headerTitle     = lipgloss.NewStyle().Bold(true).Foreground(accentSoft)
	headerLabel     = lipgloss.NewStyle().Foreground(mutedColor)
	headerValue     = lipgloss.NewStyle().Bold(true).Foreground(accentSoft)
	headerDetail    = lipgloss.NewStyle().Foreground(mutedColor)
	versionStyle    = lipgloss.NewStyle().Foreground(mutedColor)
	dividerStyle    = lipgloss.NewStyle().Foreground(mutedColor).Faint(true)
	stateLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(brightColor)
	currentTag      = lipgloss.NewStyle().Foreground(successColor).Bold(true)
	inactiveTag     = lipgloss.NewStyle().Foreground(mutedColor)
	baseStyle       = lipgloss.NewStyle().
			Padding(1, 2)

	headerPanelStyle = lipgloss.NewStyle().
				BorderForeground(headerBorderColor).
				Border(lipgloss.RoundedBorder()).
				Padding(1, 2)

	panelStyle = lipgloss.NewStyle().
			BorderForeground(listBorderColor).
			Border(lipgloss.RoundedBorder()).
			Padding(1, 2)

	diagnosticsPanelStyle = lipgloss.NewStyle().
				BorderForeground(diagnosticsBorderColor).
				Border(lipgloss.RoundedBorder()).
				Padding(1, 2)

	itemStyle = lipgloss.NewStyle()

	selectedItemStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(accentColor)

	// Windows Terminal renders uncoloured bold text at a brighter intensity.
	// Preserve its Campbell default foreground while using the bold font face.
	activeItemStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#CCCCCC"))

	proPlanStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(proColor)

	plusPlanStyle = lipgloss.NewStyle().
			Foreground(plusColor)

	freePlanStyle = lipgloss.NewStyle().
			Foreground(freeColor)

	apiPlanStyle = lipgloss.NewStyle().
			Foreground(apiColor)

	footerStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	footerGroupStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(mutedColor)

	profileColumnSeparator = lipgloss.NewStyle().
				Foreground(mutedColor)

	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Underline(true).
				Foreground(mutedColor)

	stateHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Underline(true).
				Foreground(mutedColor)

	noteStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)

	mutedStatusStyle = lipgloss.NewStyle().
				Foreground(mutedColor).
				Faint(true)

	keyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(brightColor)

	confirmStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(warningColor)

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

func remainingPercentColor(percent int) color.RGBA {
	percent = max(0, min(100, percent))
	if percent <= 50 {
		return interpolateColor(remainingEmptyColor, remainingCautionColor, percent, 50)
	}
	return interpolateColor(remainingCautionColor, remainingFullColor, percent-50, 50)
}

func interpolateColor(start, end color.RGBA, position, span int) color.RGBA {
	return color.RGBA{
		R: interpolateColorChannel(start.R, end.R, position, span),
		G: interpolateColorChannel(start.G, end.G, position, span),
		B: interpolateColorChannel(start.B, end.B, position, span),
		A: 255,
	}
}

func interpolateColorChannel(start, end uint8, position, span int) uint8 {
	value := (int(start)*(span-position) + int(end)*position + span/2) / span
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return uint8(value)
}
