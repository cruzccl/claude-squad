package overlay

import (
	"claude-squad/config"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ProfileOverlay is the program step of session creation: the profile picker in the same
// dialog as the other steps. The step is only shown when there is more than one profile,
// so it never asks a question with a single answer.
type ProfileOverlay struct {
	picker *ProfilePicker
	width  int
}

// NewProfileOverlay creates the program step over the given profiles.
func NewProfileOverlay(profiles []config.Profile) *ProfileOverlay {
	picker := NewProfilePicker(profiles)
	picker.Focus()
	return &ProfileOverlay{picker: picker}
}

// SetWidth sets the rendering width.
func (p *ProfileOverlay) SetWidth(width int) {
	p.width = width
	p.picker.SetWidth(width - 6)
}

// HandleKeyPress processes a key press and reports whether the step was submitted.
func (p *ProfileOverlay) HandleKeyPress(msg tea.KeyMsg) (submitted bool) {
	if msg.Type == tea.KeyEnter {
		return true
	}
	p.picker.HandleKeyPress(msg)
	return false
}

// SelectedProgram returns the program of the selected profile.
func (p *ProfileOverlay) SelectedProgram() string {
	return p.picker.GetSelectedProfile().Program
}

// Render renders the program step.
func (p *ProfileOverlay) Render() string {
	var s strings.Builder
	s.WriteString(p.picker.Render())
	s.WriteString("\n\n" + stepDivider(stepInnerWidth(p.width)) + "\n\n")
	s.WriteString(stepDimStyle.Render("←/→ pick a program • enter start • esc cancel"))

	return stepBoxStyle.Render(s.String())
}
