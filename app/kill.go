package app

import (
	"claude-squad/session"
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// The one word that decides the fate of the branch is coloured the way a diff is, and with
// the same two colours as ui.AdditionStyle and ui.DeletionStyle: green is what survives the
// kill, red is what does not. Only that word is coloured, so it is the thing the eye lands
// on rather than one more sentence in a colour.
var (
	branchKeptStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	branchDeletedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
)

// killRefusal returns the error a kill is refused with, or nil when it may go ahead. A
// refused kill shows no confirmation at all: asking "are you sure?" about something that
// is going to be rejected anyway only wastes a keystroke.
//
// The reason is taken from the outcome rather than from a fixed template, so what the
// user is told is the obstacle that actually applies.
func killRefusal(title string, outcome session.KillOutcome) error {
	switch outcome.Kind {
	case session.KillBlockedByDirtyWorktree:
		// The escape offered is attaching and dealing with the changes there. 'c' would be
		// shorter, but it is the handoff key, not a save button: see ADR-0002.
		return fmt.Errorf(
			"cannot kill session '%s': its worktree has uncommitted changes. "+
				"Attach with 'o' and commit or discard them there", title)
	case session.KillBlockedByCheckout:
		return fmt.Errorf("cannot kill session '%s': branch %s is checked out at %s",
			title, outcome.Branch, outcome.CheckedOutAt)
	}
	return nil
}

// killConfirmation is what the confirmation asks: the question on one line, and what the
// kill would do to the branch on its own line below it. The two are separated because
// "the session leaves the list" and "the branch and its commits are gone" are too far
// apart to be chosen between blind, and a fate buried at the end of the question is read
// last, if at all.
func killConfirmation(title string, outcome session.KillOutcome) string {
	question := fmt.Sprintf("[!] Kill session '%s'?", title)
	if outcome.Branch == "" {
		return question
	}
	return question + "\n\n" + branchFate(outcome)
}

// branchFate says what becomes of the branch and why. The reason is what makes the answer
// checkable: "will be kept" is only reassuring to someone who can see it was reached for
// the right reason, and the two reasons for keeping one are not the same reason.
func branchFate(outcome session.KillOutcome) string {
	if outcome.Kind == session.KillDeletesBranch {
		return fmt.Sprintf("Branch %s will be %s, as nothing was committed to it",
			outcome.Branch, branchDeletedStyle.Render("deleted"))
	}
	reason := "as it has commits"
	if outcome.BranchIsExisting {
		reason = "as the session did not create it"
	}
	return fmt.Sprintf("Branch %s will be %s, %s",
		outcome.Branch, branchKeptStyle.Render("kept"), reason)
}
