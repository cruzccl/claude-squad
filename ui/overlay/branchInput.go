package overlay

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// maxVisibleBranches is how many of the repository's branches the list shows at once.
const maxVisibleBranches = 5

// BranchInputOverlay is the branch step of session creation: an editable branch name,
// pre-filled with a suggestion, with the repository's branches listed underneath and
// filtered by what has been typed. It does not hold the full branch list — results are
// provided asynchronously via SetResults after each debounced search.
type BranchInputOverlay struct {
	// typed is what the user has typed, and the filter the branch list is searched with.
	typed string
	// version increments on every edit, so results for an earlier filter can be dropped.
	version uint64
	// valueVersion increments whenever the branch this step would submit changes —
	// by an edit or by picking a different branch — so a stale outcome can be dropped.
	valueVersion uint64
	// results are the branches matching the last search.
	results []string
	// cursor selects one of the results, or -1 for the typed name.
	cursor int
	// outcome says what starting a session on the current name would do. Empty until it
	// has been resolved against the repository.
	outcome string
	// outcomeBlocks is true when the outcome is a reason the name cannot be used.
	outcomeBlocks bool
	width         int
}

// BranchInputEvent is what a key press did to the branch step.
type BranchInputEvent struct {
	// Submitted is true when the user accepted what the field holds.
	Submitted bool
	// FilterChanged is true when the branch list has to be searched again.
	FilterChanged bool
	// ValueChanged is true when the step would now submit a different branch, so what
	// that branch would do has to be resolved again.
	ValueChanged bool
}

// NewBranchInputOverlay creates the branch step with the field pre-filled with the
// suggested branch name. The suggestion is computed by the caller, once: editing the
// field never brings it back.
func NewBranchInputOverlay(suggestion string) *BranchInputOverlay {
	return &BranchInputOverlay{typed: suggestion, cursor: -1}
}

// Value returns the branch name the step will submit: a selected branch if one is
// highlighted in the list, otherwise the typed name.
func (b *BranchInputOverlay) Value() string {
	if b.cursor >= 0 && b.cursor < len(b.results) {
		return b.results[b.cursor]
	}
	return b.typed
}

// SetWidth sets the rendering width.
func (b *BranchInputOverlay) SetWidth(width int) {
	b.width = width
}

// Filter returns the text the branch list is filtered by.
func (b *BranchInputOverlay) Filter() string {
	return b.typed
}

// FilterVersion returns a version that changes on every edit of the field.
func (b *BranchInputOverlay) FilterVersion() uint64 {
	return b.version
}

// ValueVersion returns a version that changes whenever Value does.
func (b *BranchInputOverlay) ValueVersion() uint64 {
	return b.valueVersion
}

// SetOutcome records what starting a session on the current branch name would do.
// An outcome resolved for an older value is stale and is dropped.
func (b *BranchInputOverlay) SetOutcome(outcome string, blocks bool, version uint64) {
	if version != b.valueVersion {
		return
	}
	b.outcome = outcome
	b.outcomeBlocks = blocks
}

// SetResults replaces the listed branches. Results carrying an older version are stale —
// the field has been edited since the search was issued — and are dropped, so a slow
// search on a large repository cannot overwrite a newer list.
//
// It reports whether the branch the step would submit changed, in which case the caller
// has to resolve what the new one would do: nothing else will, because no key was pressed.
func (b *BranchInputOverlay) SetResults(branches []string, version uint64) bool {
	if version != b.version {
		return false
	}
	// Keep the highlight on the branch that was picked rather than on its old index:
	// a batch arriving after a selection would otherwise leave it on a different branch.
	selected := b.Value()
	hadSelection := b.cursor >= 0

	b.results = branches
	b.cursor = -1
	if hadSelection {
		for i, branch := range branches {
			if branch == selected {
				b.cursor = i
				break
			}
		}
	}

	// A picked branch that is no longer listed drops the step back to the typed name,
	// which is a different branch: what the old one would have done no longer applies.
	if b.Value() != selected {
		b.clearOutcome()
		return true
	}
	return false
}

// clearOutcome drops a resolved outcome that no longer describes the current name, and
// discards any resolution still in flight for it.
func (b *BranchInputOverlay) clearOutcome() {
	b.outcome = ""
	b.outcomeBlocks = false
	b.valueVersion++
}

// HandleKeyPress processes a key press. It reports whether the step was submitted, and
// whether the filter changed and a new search is needed.
func (b *BranchInputOverlay) HandleKeyPress(msg tea.KeyMsg) BranchInputEvent {
	switch msg.Type {
	case tea.KeyEnter:
		return BranchInputEvent{Submitted: true}
	case tea.KeyUp:
		if b.cursor >= 0 {
			b.cursor--
			b.clearOutcome()
			return BranchInputEvent{ValueChanged: true}
		}
		return BranchInputEvent{}
	case tea.KeyDown:
		if b.cursor < len(b.results)-1 {
			b.cursor++
			b.clearOutcome()
			return BranchInputEvent{ValueChanged: true}
		}
		return BranchInputEvent{}
	case tea.KeyBackspace:
		if b.Value() == "" {
			return BranchInputEvent{}
		}
		return b.edit(func(value string) string {
			runes := []rune(value)
			return string(runes[:len(runes)-1])
		})
	case tea.KeySpace:
		return b.edit(func(value string) string { return value + " " })
	case tea.KeyRunes:
		return b.edit(func(value string) string { return value + string(msg.Runes) })
	}
	return BranchInputEvent{}
}

// edit replaces the field's contents. A branch highlighted in the list is the starting
// point, so typing continues from whatever is on screen.
func (b *BranchInputOverlay) edit(change func(string) string) BranchInputEvent {
	b.typed = change(b.Value())
	b.cursor = -1
	b.version++
	b.clearOutcome()
	return BranchInputEvent{FilterChanged: true, ValueChanged: true}
}

// Render renders the branch step.
func (b *BranchInputOverlay) Render() string {
	innerWidth := stepInnerWidth(b.width)
	divider := stepDivider(innerWidth)

	var s strings.Builder
	s.WriteString(stepTitleStyle.Render("Branch name") + "\n\n")
	if b.cursor >= 0 {
		s.WriteString(stepValueStyle.Render(stepTruncate(b.Value(), innerWidth)))
	} else {
		s.WriteString(stepValueStyle.Render(stepTruncate(b.typed, innerWidth-1) + "█"))
	}
	if b.outcome != "" {
		style := stepDimStyle
		if b.outcomeBlocks {
			style = stepBlockedStyle
		}
		s.WriteString("\n" + style.Render(stepTruncate("→ "+b.outcome, innerWidth)))
	}
	s.WriteString("\n\n" + divider + "\n\n")

	s.WriteString(stepTitleStyle.Render("Existing branches") + "\n\n")
	s.WriteString(b.renderResults(innerWidth))
	s.WriteString("\n\n" + divider + "\n\n")
	s.WriteString(stepDimStyle.Render("↑/↓ pick a branch • enter continue • esc cancel"))

	return stepBoxStyle.Render(s.String())
}

func (b *BranchInputOverlay) renderResults(innerWidth int) string {
	if len(b.results) == 0 {
		return stepDimStyle.Render("  No matching branches")
	}

	// Window the list around the cursor so a long list stays a fixed height.
	start := 0
	if b.cursor >= maxVisibleBranches {
		start = b.cursor - maxVisibleBranches + 1
	}
	end := start + maxVisibleBranches
	if end > len(b.results) {
		end = len(b.results)
	}

	var s strings.Builder
	for i := start; i < end; i++ {
		if i == b.cursor {
			s.WriteString(stepSelectedStyle.Render(stepTruncate("> "+b.results[i], innerWidth)))
		} else {
			s.WriteString(stepDimStyle.Render(stepTruncate("  "+b.results[i], innerWidth)))
		}
		if i < end-1 {
			s.WriteString("\n")
		}
	}
	return s.String()
}
