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

// stepTruncateLeft cuts text to the box's inner width from the left, keeping its end. A
// path is recognised by its last segments, so a long one gives up the directories it lives
// in rather than the name of the repository it points at.
func stepTruncateLeft(text string, innerWidth int) string {
	if innerWidth < 1 || runewidth.StringWidth(text) <= innerWidth {
		return text
	}

	runes := []rune(text)
	kept, i := 0, len(runes)
	for i > 0 {
		width := runewidth.RuneWidth(runes[i-1])
		if kept+width > innerWidth-1 {
			break
		}
		kept += width
		i--
	}
	return "…" + string(runes[i:])
}

// stepListWindow returns the half-open range of results a step should draw: at most
// maxVisible rows, scrolled to keep the cursor in view, so a long list leaves the box the
// same height as a short one.
func stepListWindow(count int, cursor int, maxVisible int) (start int, end int) {
	start = 0
	if cursor >= maxVisible {
		start = cursor - maxVisible + 1
	}
	end = start + maxVisible
	if end > count {
		end = count
	}
	return start, end
}

// stepDecorateLeft lays a prefix and suffix around text that is cut from the left, giving
// the text the room the decoration leaves it.
//
// A step that cuts from the right can hand stepTruncate its line already decorated, since
// cutting the end keeps the prefix. Cutting from the left would eat it instead, so the
// decoration has to stay outside the cut — and the arithmetic belongs here rather than at
// every call site subtracting a number for its own prefix.
func stepDecorateLeft(prefix string, text string, suffix string, innerWidth int) string {
	room := innerWidth - runewidth.StringWidth(prefix) - runewidth.StringWidth(suffix)
	return prefix + stepTruncateLeft(text, room) + suffix
}
