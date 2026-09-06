package app

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// Creating a session is one flow with four steps: the repository the session is created
// in, the session title, the branch the session works on, and — only when there is more
// than one profile — the program to run.

// maxKnownRepos is how many chosen repositories are remembered between runs. The list is
// there to save typing, not to be a record: past it, the oldest is forgotten.
const maxKnownRepos = 10

// enterRepoStep opens the repository step, pre-filled with the active repository. It is
// the first step of creation and the only one that runs before there is a session to
// build: the repository decides where that session is created, so nothing can be created
// until it is settled.
func (m *home) enterRepoStep() (tea.Model, tea.Cmd) {
	m.repoOverlay = overlay.NewRepoInputOverlay(m.repoPath, m.knownRepos(), func(typed string) string {
		return m.repoChoiceFor(typed).expand()
	})
	m.refreshRepoOutcome()
	m.state = stateNewRepo
	return m, tea.WindowSize()
}

// handleRepoStep handles the first step of session creation: the repository the session is
// created in.
func (m *home) handleRepoStep(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	event := m.repoOverlay.HandleKeyPress(msg)
	if event.Complete {
		completion := m.repoChoiceFor(m.repoOverlay.Value()).complete()
		// Tab with nothing left to add is the moment the step has to show its work:
		// several directories match and they share no more than what is already typed, so
		// the only way forward is to offer them. Typing again goes back to the known
		// repositories, which is the list that answers a different question.
		if completion.path == m.repoOverlay.Value() && len(completion.candidates) > 1 {
			m.repoOverlay.ShowCandidates(completion.dir, completion.candidates)
		} else {
			m.repoOverlay.SetValue(completion.path)
		}
	}
	if !event.Submitted {
		if event.ValueChanged || event.Complete {
			m.refreshRepoOutcome()
		}
		return m, nil
	}

	// The same lookup that labels the field decides what happens, so the step cannot
	// promise one thing and do another.
	typed := m.repoOverlay.Value()
	outcome := m.repoChoiceFor(typed).resolve()
	if outcome.blocks {
		if strings.TrimSpace(typed) == "" {
			return m, m.handleError(fmt.Errorf("repository path cannot be empty"))
		}
		return m, m.handleError(fmt.Errorf("%s: %s", typed, strings.ToLower(outcome.text)))
	}
	return m.enterTitleStep(outcome.root)
}

// enterTitleStep makes the chosen repository the active one and opens the title step on a
// session created there. The session is built here rather than when creation started, so
// its path is right from the moment it exists and never has to be corrected.
func (m *home) enterTitleStep(repoPath string) (tea.Model, tea.Cmd) {
	sess, err := session.NewInstance(session.InstanceOptions{
		Title:   "",
		Path:    repoPath,
		Program: m.program,
	})
	if err != nil {
		return m, m.handleError(err)
	}

	m.repoPath = repoPath
	m.rememberRepo(repoPath)
	m.repoOverlay = nil

	m.newInstanceFinalizer = m.list.AddInstance(sess)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	m.state = stateNewTitle
	m.menu.SetState(ui.StateNewInstance)

	// Fetch in the background so the branch step lists up-to-date branches. This is the
	// first moment there is a repository to fetch from.
	return m, tea.Batch(tea.WindowSize(), func() tea.Msg {
		git.FetchBranches(repoPath)
		return nil
	})
}

// abandonRepoStep leaves the repository step. Nothing is discarded because nothing has
// been created yet: the session that creation builds does not exist until the repository
// is settled.
func (m *home) abandonRepoStep() tea.Cmd {
	m.repoOverlay = nil
	m.state = stateDefault
	return tea.WindowSize()
}

// refreshRepoOutcome tells the step what the path it holds would do. It is resolved on
// every edit rather than debounced like the branch step: a path that is half-typed does
// not exist, and saying so costs one stat.
func (m *home) refreshRepoOutcome() {
	outcome := m.repoChoiceFor(m.repoOverlay.Value()).resolve()
	m.repoOverlay.SetOutcome(outcome.text, outcome.blocks)
}

// repoChoice is the question the repository step asks of a path: what is in the field, and
// the active repository a relative path is read from. The two are one question — what a
// typed path names depends on where it is read from — so they travel as one value rather
// than as a pair every caller has to remember to keep together.
type repoChoice struct {
	// typed is the path being asked about.
	typed string
	// activeRepo is the repository a relative path is read from.
	activeRepo string
}

// repoChoiceFor asks the repository step's question of a path, against the repository that
// is active now.
func (m *home) repoChoiceFor(typed string) repoChoice {
	return repoChoice{typed: typed, activeRepo: m.repoPath}
}

// of returns the same question asked of another path. Which repository a relative path is
// read from does not change while the step is open.
func (c repoChoice) of(typed string) repoChoice {
	c.typed = typed
	return c
}

// repoOutcome reports what choosing a path in the repository step would do.
type repoOutcome struct {
	// root is the repository the path resolves to, empty when it resolves to none.
	root string
	// text says what choosing it would do, phrased for the step.
	text string
	// blocks is true when the text is a reason the path cannot be used.
	blocks bool
}

// resolve reports what creating sessions in the chosen path would do.
func (c repoChoice) resolve() repoOutcome {
	path := c.expand()
	if path == "" {
		return repoOutcome{blocks: true}
	}

	info, err := os.Stat(path)
	if err != nil {
		return repoOutcome{text: "Path does not exist", blocks: true}
	}
	if !info.IsDir() {
		return repoOutcome{text: "Not a directory", blocks: true}
	}

	// Resolving to the root is what makes a directory inside a repository name the
	// repository, and it is done before the answer is phrased so the step says which
	// repository it means.
	root, err := git.RepoRoot(path)
	if err != nil {
		return repoOutcome{text: "Not a git repository", blocks: true}
	}
	if root == c.activeRepo {
		return repoOutcome{root: root, text: "Active repository"}
	}
	return repoOutcome{root: root, text: "New sessions in " + root}
}

// expand turns what was typed into a path to look at: ~ is the home directory, and a
// relative path is read from the active repository rather than from the directory cs was
// launched in, which stops being visible the moment the repository changes.
func (c repoChoice) expand() string {
	path := strings.TrimSpace(c.typed)
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path[1:], string(filepath.Separator)))
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.activeRepo, path)
	}
	return filepath.Clean(path)
}

// repoCompletion is what tab found: the typed path with everything unambiguous filled in,
// and the directories it matched on the way.
type repoCompletion struct {
	// path is the typed path extended as far as the candidates leave no choice. It is
	// what was typed when there was nothing to add.
	path string
	// candidates are the directories that matched, in the shape the path was typed in, so
	// picking one keeps the ~ or the relative start the user wrote.
	candidates []string
	// dir is the directory they were read from, resolved, for saying where they are.
	dir string
}

// complete extends the typed path as far as the filesystem leaves no choice: to the
// longest prefix the candidates share, and with a trailing separator when there is only
// one. It never cycles through the candidates — it reports them instead, and the step
// lists them — and it hands back what was typed when there is nothing to add.
func (c repoChoice) complete() repoCompletion {
	typed := strings.TrimSpace(c.typed)
	if typed == "" {
		return repoCompletion{path: c.typed}
	}

	sep := string(filepath.Separator)
	prefix, base := "", typed
	if idx := strings.LastIndex(typed, sep); idx >= 0 {
		prefix, base = typed[:idx+1], typed[idx+1:]
	}
	dir := prefix
	if dir == "" {
		dir = "."
	}
	dirPath := c.of(dir).expand()
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return repoCompletion{path: c.typed}
	}

	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		// A hidden directory is only offered when it is what the user started to type.
		if !strings.HasPrefix(name, base) {
			continue
		}
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		// Only a directory can be a repository. The question is asked of the path rather
		// than of the entry, which describes the name and not what it points at: a
		// repository reached through a symlink is still a repository, and completion has
		// to agree with the resolve that accepts one once it is typed out in full.
		if !isLiveDirectory(filepath.Join(dirPath, name)) {
			continue
		}
		matches = append(matches, name)
	}
	if len(matches) == 0 {
		return repoCompletion{path: c.typed}
	}

	candidates := make([]string, 0, len(matches))
	common := matches[0]
	for _, name := range matches {
		common = commonPrefix(common, name)
		candidates = append(candidates, prefix+name)
	}
	if len(matches) == 1 {
		return repoCompletion{path: prefix + common + sep, candidates: candidates, dir: dirPath}
	}
	return repoCompletion{path: prefix + common, candidates: candidates, dir: dirPath}
}

// commonPrefix returns the longest prefix both names share.
func commonPrefix(a string, b string) string {
	runesA, runesB := []rune(a), []rune(b)
	i := 0
	for i < len(runesA) && i < len(runesB) && runesA[i] == runesB[i] {
		i++
	}
	return string(runesA[:i])
}

// isLiveDirectory reports whether the path is a directory that is still there. A
// repository that has been deleted or moved is neither offered nor kept: it is not
// somewhere a session can be created.
func isLiveDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// knownRepos returns the repositories the step offers by name: the active one, then the
// ones chosen before, then the ones this application's sessions live in. A repository that
// is no longer on disk is left out — it is not somewhere a session can be created — and is
// dropped from what is stored the next time the list is written.
func (m *home) knownRepos() []string {
	var repos []string
	seen := make(map[string]bool)
	add := func(path string) {
		if path == "" || seen[path] || !isLiveDirectory(path) {
			return
		}
		seen[path] = true
		repos = append(repos, path)
	}

	add(m.repoPath)
	for _, path := range m.appState.GetKnownRepos() {
		add(path)
	}
	for _, sess := range m.list.GetInstances() {
		add(sess.RepoPath())
	}
	return repos
}

// rememberRepo records a repository as one to offer next time. It is recorded when the
// step is left, not when a session finally starts: a repository worth opening the step on
// is worth offering again, whether or not the creation it began was seen through.
func (m *home) rememberRepo(repoPath string) {
	live := make([]string, 0, maxKnownRepos)
	for _, path := range m.appState.GetKnownRepos() {
		if isLiveDirectory(path) {
			live = append(live, path)
		}
	}
	if err := m.appState.SetKnownRepos(config.PromoteKnownRepo(live, repoPath, maxKnownRepos)); err != nil {
		log.WarningLog.Printf("failed to remember repository %s: %v", repoPath, err)
	}
}

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
		//
		// The whole path is named, not the repository's directory name: two checkouts of
		// the same project are exactly the pair that collides here, and their names are
		// identical. Where to look is the only thing this message is for.
		for _, other := range m.list.GetInstances() {
			if other != sess && other.Title == sess.Title {
				return m, m.handleError(fmt.Errorf("a session titled %q already exists in %s",
					sess.Title, other.RepoPath()))
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

// branchKey identifies a branch within the repository it belongs to. Two repositories can
// each have a branch of the same name, and a session on one of them says nothing about the
// other, so the name alone is not an identity.
type branchKey struct {
	repoPath string
	branch   string
}

// branchesTakenBySessions maps each branch this application already has a session on to
// that session's title. Pausing a session removes its worktree, so git stops reporting
// its branch as checked out — but the branch is still hers, and the directory is the one
// she needs to come back to.
func (m *home) branchesTakenBySessions() map[branchKey]string {
	taken := make(map[branchKey]string)
	for _, sess := range m.list.GetInstances() {
		// The session being created has no branch yet, so it cannot collide with itself.
		if sess.Branch != "" {
			taken[branchKey{repoPath: sess.RepoPath(), branch: sess.Branch}] = sess.Title
		}
	}
	return taken
}

// resolveBranchForSession reports what starting a session on this name would do. It is the
// repository's answer, overridden by the sessions this application knows about, which git
// cannot see once one is paused.
func resolveBranchForSession(repoPath, name string, taken map[branchKey]string) git.BranchOutcome {
	outcome := git.ResolveBranch(repoPath, name)
	if outcome.Kind == git.BranchInvalid {
		return outcome
	}
	if title, held := taken[branchKey{repoPath: repoPath, branch: name}]; held {
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
