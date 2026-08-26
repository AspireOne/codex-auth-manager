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
	successColor = lipgloss.Color("#10B981")
	infoColor    = lipgloss.Color("#38BDF8")
	errorColor   = lipgloss.Color("#F87171")
	warningColor = lipgloss.Color("#FBBF24")
	proColor     = lipgloss.Color("#F59E0B")
	plusColor    = lipgloss.Color("#FBBF24")
	freeColor    = lipgloss.Color("#FDE047")
	apiColor     = lipgloss.Color("#F5D0A9")
	mutedColor   = lipgloss.Color("#94A3B8")
	panelBorder  = lipgloss.Color("#A78BFA")

	cardBorderColor = lipgloss.Color("#475569")
	headerTitle     = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	headerValue     = lipgloss.NewStyle().Bold(true).Foreground(accentSoft)
	currentTag      = lipgloss.NewStyle().Foreground(successColor).Bold(true)
	baseStyle       = lipgloss.NewStyle().
			Padding(1, 2)

	panelStyle = lipgloss.NewStyle().
			BorderForeground(panelBorder).
			Border(lipgloss.RoundedBorder()).
			Padding(1, 2)

	itemStyle = lipgloss.NewStyle()

	pillFg = lipgloss.Color("#111827")

	proPlanStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(pillFg).
			Background(proColor).
			Padding(0, 1)

	plusPlanStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(pillFg).
			Background(plusColor).
			Padding(0, 1)

	freePlanStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(pillFg).
			Background(freeColor).
			Padding(0, 1)

	apiPlanStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(pillFg).
			Background(apiColor).
			Padding(0, 1)

	footerStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	chipLabelStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	chipValueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0"))

	separatorStyle = lipgloss.NewStyle().
			Foreground(cardBorderColor)

	dividerStyle = lipgloss.NewStyle().
			Foreground(cardBorderColor)

	sectionTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(accentColor)

	scrollIndicatorStyle = lipgloss.NewStyle().
				Foreground(mutedColor).
				Italic(true)

	avatarStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#E2E8F0")).
			Background(cardBorderColor)

	avatarSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(accentColor)

	avatarAPIStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E1B2E")).
			Background(accentSoft)

	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cardBorderColor).
			Padding(0, 1)

	selectedCardStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(accentColor).
				Padding(0, 1)

	outerPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(panelBorder).
			Padding(0, 2)

	footerPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(cardBorderColor).
				Padding(0, 1)

	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(mutedColor)

	mutedStatusStyle = lipgloss.NewStyle().
				Foreground(mutedColor).
				Faint(true)

	keyHintStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentSoft)

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

// icons centralizes every decoration glyph so the set can be swapped later.
// All glyphs come from widely available fonts (no Nerd Font dependency).
var icons = struct {
	current   string
	status    string
	update    string
	browser   string
	dir       string
	cursor    string
	activeDot string
	ok        string
	bad       string
	unknown   string
	moreUp    string
	moreDown  string
	apiAvatar string
}{
	current:   "@",
	status:    "●",
	update:    "▲",
	browser:   "◊",
	dir:       "►",
	cursor:    "»",
	activeDot: "●",
	ok:        "✓",
	bad:       "✗",
	unknown:   "?",
	moreUp:    "▲",
	moreDown:  "▼",
	apiAvatar: "</>",
}

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
