package overlay

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Styles shared by the overlays that make up the session creation flow, so every step
// looks like the same dialog.
var (
	stepBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(1, 2)

	stepTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("62")).
			Bold(true)

	stepValueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7"))

	stepSelectedStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("62")).
				Foreground(lipgloss.Color("0"))

	stepDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	stepBlockedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("203"))
)

// stepInnerWidth is the content width of a step's box, allowing for its border and
// padding. A box that has not been sized yet falls back to a readable default.
func stepInnerWidth(width int) int {
	inner := width - 6
	if inner < 1 {
		return 40
	}
	return inner
}

// stepDivider is the horizontal rule separating a step's sections.
func stepDivider(innerWidth int) string {
	return stepDimStyle.Render(strings.Repeat("─", innerWidth))
}

// stepTruncate cuts text to the box's inner width, so a long branch name or a long path
// cannot stretch the box wider than the dividers that frame it.
func stepTruncate(text string, innerWidth int) string {
	return runewidth.Truncate(text, innerWidth, "…")
}
