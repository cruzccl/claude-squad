package app

import (
	"claude-squad/session"
	"claude-squad/session/tmux"
	"claude-squad/ui"

	tea "github.com/charmbracelet/bubbletea"
)

// attachSide is the side of a session the terminal is handed to: the agent the session
// runs, or the shell sitting in its worktree. A session has both at once, and attaching
// means picking one of them.
type attachSide int

const (
	attachAgent attachSide = iota
	attachShell
)

// other is the side a switch goes to.
func (s attachSide) other() attachSide {
	if s == attachAgent {
		return attachShell
	}
	return attachAgent
}

// tab is the tab that shows this side when the terminal comes back to the layout.
func (s attachSide) tab() int {
	if s == attachShell {
		return ui.TerminalTab
	}
	return ui.PreviewTab
}

// attachSideFor says which side the user is asking for, which is the one the tab they
// pressed from is already showing.
func attachSideFor(activeTab int) attachSide {
	if activeTab == ui.TerminalTab {
		return attachShell
	}
	return attachAgent
}

// attach hands the terminal to one side of a session and keeps handing it back and forth
// for as long as the user asks to switch sides, so that reaching the shell of the session
// they are working in costs one keystroke rather than a trip through the session list.
// It returns when the user detaches, which is the only way back to that list, and leaves
// the window showing the side they left from.
//
// It blocks: while a session is attached the TUI is not drawing, and the attached tmux
// owns the screen.
func (m *home) attach(instance *session.Instance, side attachSide) tea.Cmd {
	// opened is the side the user went in on, shown the last one they actually saw. They
	// differ when an attachment fails: the side that was asked for was never on screen.
	opened, shown := side, side
	var cmds []tea.Cmd
	for {
		ch, err := m.attachTo(instance, side)
		if err != nil {
			cmds = append(cmds, m.handleError(err))
			break
		}
		shown = side
		<-ch

		if m.detachReason(instance, side) != tmux.DetachedToSwitch {
			break
		}

		// A shell is disposable: one that was never opened, or that has died since it was
		// last used, is made again on the way in. An agent is not - reviving one is what
		// resuming a session does, worktree and all - so asking for an agent that is gone
		// keeps the user in the shell instead of moving them somewhere they did not ask
		// for.
		next := side.other()
		if next == attachAgent && !instance.TmuxAlive() {
			continue
		}
		side = next
	}

	// The window follows the user rather than the other way round: switching sides and
	// then detaching comes back to the side they ended on. Detaching from the side they
	// went in on leaves the tab alone, so that going into the agent from the diff comes
	// back to the diff.
	if shown != opened {
		m.tabbedWindow.SetActiveTab(shown.tab())
		m.menu.SetActiveTab(shown.tab())
	}
	m.state = stateDefault
	cmds = append(cmds, m.instanceChanged())
	return tea.Batch(cmds...)
}

// attachTo hands the terminal to one side and returns the channel that closes when that
// side gives it back.
func (m *home) attachTo(instance *session.Instance, side attachSide) (chan struct{}, error) {
	if side == attachShell {
		if err := m.tabbedWindow.EnsureTerminalSession(instance); err != nil {
			return nil, err
		}
		return m.tabbedWindow.AttachTerminal()
	}
	return instance.Attach()
}

// detachReason asks the side that just gave the terminal back what the user wanted.
func (m *home) detachReason(instance *session.Instance, side attachSide) tmux.DetachReason {
	if side == attachShell {
		return m.tabbedWindow.TerminalDetachReason()
	}
	return instance.DetachReason()
}
