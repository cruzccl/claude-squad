package app

import (
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// Creating a session is one flow with three steps: the session title, the branch the
// session works on, and — only when there is more than one profile — the program to run.

// newSession returns the session the creation flow is building. It is the last one in
// the list: creation adds it up front so it shows while its title is typed.
//
// The locals it feeds are named sess, not session: the glossary's word is taken here by
// the package that defines the type.
func (m *home) newSession() *session.Instance {
	instances := m.list.GetInstances()
	return instances[len(instances)-1]
}

// handleTitleStep handles the first step of session creation: naming the session.
func (m *home) handleTitleStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	sess := m.newSession()
	switch msg.Type {
	case tea.KeyEnter:
		if len(sess.Title) == 0 {
			return m, m.handleError(fmt.Errorf("title cannot be empty"))
		}
		// Stored state is matched by title, so two sessions sharing one cannot be told
		// apart. Reject the duplicate here rather than corrupt the state later.
		for _, other := range m.list.GetInstances() {
			if other != sess && other.Title == sess.Title {
				return m, m.handleError(fmt.Errorf("a session titled %q already exists", sess.Title))
			}
		}
		return m.enterBranchStep(sess)
	case tea.KeyRunes:
		if runewidth.StringWidth(sess.Title) >= 32 {
			return m, m.handleError(fmt.Errorf("title cannot be longer than 32 characters"))
		}
		if err := sess.SetTitle(sess.Title + string(msg.Runes)); err != nil {
			return m, m.handleError(err)
		}
	case tea.KeyBackspace:
		runes := []rune(sess.Title)
		if len(runes) == 0 {
			return m, nil
		}
		if err := sess.SetTitle(string(runes[:len(runes)-1])); err != nil {
			return m, m.handleError(err)
		}
	case tea.KeySpace:
		if err := sess.SetTitle(sess.Title + " "); err != nil {
			return m, m.handleError(err)
		}
	}
	return m, nil
}

// enterBranchStep opens the branch step, pre-filled with the branch name suggested by the
// session title. The suggestion is computed here, once: editing the field never brings it
// back.
func (m *home) enterBranchStep(sess *session.Instance) (tea.Model, tea.Cmd) {
	m.branchOverlay = overlay.NewBranchInputOverlay(
		git.SuggestBranchName(m.appConfig.BranchPrefix, sess.Title))
	m.state = stateNewBranch

	// List every branch to start with, not the ones matching the suggestion: the
	// suggestion names a branch that does not exist yet, so filtering by it would open
	// the step on an empty list. Typing filters from there. Resolve the suggestion too,
	// so the step opens saying what it would do.
	return m, tea.Batch(
		tea.WindowSize(),
		m.runBranchSearch("", m.branchOverlay.FilterVersion()),
		m.runBranchOutcome(m.branchOverlay.Value(), m.branchOverlay.ValueVersion()),
	)
}

// handleBranchStep handles the second step of session creation: the branch the session
// works on.
func (m *home) handleBranchStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	event := m.branchOverlay.HandleKeyPress(msg)
	if !event.Submitted {
		var cmds []tea.Cmd
		if event.FilterChanged {
			cmds = append(cmds, debounce(branchSearchDebounceMsg{
				filter: m.branchOverlay.Filter(), version: m.branchOverlay.FilterVersion()}))
		}
		if event.ValueChanged {
			cmds = append(cmds, debounce(branchOutcomeDebounceMsg{
				name: m.branchOverlay.Value(), version: m.branchOverlay.ValueVersion()}))
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)
	}

	// The name is validated, never rewritten: the user owns it. The same lookup that
	// labels the field decides what happens, so the step cannot promise one thing and do
	// another.
	sess := m.newSession()
	branch := m.branchOverlay.Value()
	outcome := resolveBranchForSession(sess.Path, branch, m.branchesTakenBySessions())
	switch outcome.Kind {
	case git.BranchInvalid:
		// Ask git why: an empty field and a malformed name are different mistakes, and
		// its message says which. It answers the same question the resolve just asked, so
		// it cannot disagree — but do not hand handleError a nil if it ever does.
		if err := git.ValidateBranchName(branch); err != nil {
			return m, m.handleError(err)
		}
		return m, m.handleError(fmt.Errorf("branch %s is not a valid branch name", branch))
	case git.BranchCheckedOut:
		// A branch can only be checked out in one worktree, and setting one up would
		// take over the directory the other is using.
		return m, m.handleError(fmt.Errorf("branch %s is %s", branch, checkedOutReason(outcome)))
	}

	// An existing branch means "work on this branch", whether it was picked from the list
	// or typed. The distinction is what keeps the branch out of cleanup when the session
	// is closed.
	sess.SetBranch(branch, outcome.Kind == git.BranchExisting)
	m.branchOverlay = nil

	if profiles := m.appConfig.GetProfiles(); len(profiles) > 1 {
		m.profileOverlay = overlay.NewProfileOverlay(profiles)
		m.state = stateNewProgram
		return m, tea.WindowSize()
	}
	return m.startNewSession(sess)
}

// handleProgramStep handles the third step of session creation: which program to run.
func (m *home) handleProgramStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.profileOverlay.HandleKeyPress(msg) {
		return m, nil
	}

	sess := m.newSession()
	sess.Program = m.profileOverlay.SelectedProgram()
	m.profileOverlay = nil
	return m.startNewSession(sess)
}

// startNewSession finalizes the session into the list and starts it in the background.
func (m *home) startNewSession(sess *session.Instance) (tea.Model, tea.Cmd) {
	sess.SetStatus(session.Loading)
	m.newInstanceFinalizer()
	m.state = stateDefault
	m.menu.SetState(ui.StateDefault)

	startCmd := func() tea.Msg {
		err := sess.Start(true)
		return instanceStartedMsg{instance: sess, err: err}
	}

	return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
}

// abandonNewSession discards the half-created session and returns to the list.
func (m *home) abandonNewSession() tea.Cmd {
	m.list.Kill()
	m.branchOverlay = nil
	m.profileOverlay = nil
	m.state = stateDefault

	cmds := []tea.Cmd{
		tea.WindowSize(),
		func() tea.Msg {
			m.menu.SetState(ui.StateDefault)
			return nil
		},
	}
	if cmd := m.instanceChanged(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Sequence(cmds...)
}

// branchSearchDebounceMsg fires after the debounce interval to trigger a search.
type branchSearchDebounceMsg struct {
	filter  string
	version uint64
}

// branchSearchResultMsg carries search results back to Update.
type branchSearchResultMsg struct {
	branches []string
	version  uint64
}

const branchStepDebounce = 150 * time.Millisecond

// debounce returns a tea.Cmd that waits out the debounce interval and then emits msg. Both
// of the branch step's queries go through it, so a fast typist issues one of each rather
// than one per keystroke. Waiting is all it does: dropping a query the field has already
// outrun is the version on the message, checked when it comes back.
func debounce(msg tea.Msg) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(branchStepDebounce)
		return msg
	}
}

// branchesTakenBySessions maps each branch this application already has a session on to
// that session's title. Pausing a session removes its worktree, so git stops reporting
// its branch as checked out — but the branch is still hers, and the directory is the one
// she needs to come back to.
func (m *home) branchesTakenBySessions() map[string]string {
	taken := make(map[string]string)
	for _, sess := range m.list.GetInstances() {
		// The session being created has no branch yet, so it cannot collide with itself.
		if sess.Branch != "" {
			taken[sess.Branch] = sess.Title
		}
	}
	return taken
}

// resolveBranchForSession reports what starting a session on this name would do. It is the
// repository's answer, overridden by the sessions this application knows about, which git
// cannot see once one is paused.
func resolveBranchForSession(repoPath, name string, taken map[string]string) git.BranchOutcome {
	outcome := git.ResolveBranch(repoPath, name)
	if outcome.Kind == git.BranchInvalid {
		return outcome
	}
	if title, held := taken[name]; held {
		return git.BranchOutcome{Kind: git.BranchCheckedOut, HeldBy: title}
	}
	return outcome
}

// checkedOutReason says who holds a branch that is already checked out, as a fragment both
// the step's line and the error on submit read into their own sentence. Which blocker to
// name is decided here alone, so the line cannot describe one and the error another.
func checkedOutReason(outcome git.BranchOutcome) string {
	if outcome.HeldBy != "" {
		return fmt.Sprintf("already used by session %q", outcome.HeldBy)
	}
	return fmt.Sprintf("already checked out at %s", outcome.CheckedOutAt)
}

// upperFirst starts a fragment as its own sentence.
func upperFirst(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return s
	}
	return string(unicode.ToUpper(runes[0])) + string(runes[1:])
}

// branchOutcomeDebounceMsg fires after the debounce interval to resolve what the branch
// in the field would do.
type branchOutcomeDebounceMsg struct {
	name    string
	version uint64
}

// branchOutcomeMsg carries a resolved outcome back to Update.
type branchOutcomeMsg struct {
	outcome string
	blocks  bool
	version uint64
}

// runBranchOutcome returns a tea.Cmd that asks the repository what starting a session on
// this branch would do, and phrases the answer for the branch step.
func (m *home) runBranchOutcome(name string, version uint64) tea.Cmd {
	repoPath := m.repoPath
	// Snapshot the sessions here, on the main thread: the lookup itself runs in the
	// background.
	taken := m.branchesTakenBySessions()
	return func() tea.Msg {
		if strings.TrimSpace(name) == "" {
			return branchOutcomeMsg{version: version}
		}

		outcome := resolveBranchForSession(repoPath, name, taken)
		var text string
		var blocks bool
		switch outcome.Kind {
		case git.BranchInvalid:
			text, blocks = "Not a valid branch name", true
		case git.BranchCheckedOut:
			text, blocks = upperFirst(checkedOutReason(outcome)), true
		case git.BranchExisting:
			text = "Works on the existing branch"
			if outcome.LastCommit != "" {
				text = fmt.Sprintf("Works on the existing branch (last commit: %s)", outcome.LastCommit)
			}
		default:
			text = "Creates a new branch from HEAD"
		}
		return branchOutcomeMsg{outcome: text, blocks: blocks, version: version}
	}
}

// runBranchSearch returns a tea.Cmd that performs the git search in the background.
func (m *home) runBranchSearch(filter string, version uint64) tea.Cmd {
	repoPath := m.repoPath
	return func() tea.Msg {
		branches, err := git.SearchBranches(repoPath, filter)
		if err != nil {
			log.WarningLog.Printf("branch search failed: %v", err)
			return nil
		}
		return branchSearchResultMsg{branches: branches, version: version}
	}
}
