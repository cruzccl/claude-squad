package app

import (
	"claude-squad/config"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/ui"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// fakeAppState is the saved state the creation flow writes to, kept in memory so a test
// never touches the state file of the machine it runs on.
type fakeAppState struct {
	helpScreensSeen uint32
	knownRepos      []string
}

func (f *fakeAppState) GetHelpScreensSeen() uint32           { return f.helpScreensSeen }
func (f *fakeAppState) SetHelpScreensSeen(seen uint32) error { f.helpScreensSeen = seen; return nil }
func (f *fakeAppState) GetKnownRepos() []string              { return f.knownRepos }
func (f *fakeAppState) SetKnownRepos(repos []string) error   { f.knownRepos = repos; return nil }

// newTestHome builds a home model wired up enough to drive the creation flow. The start
// command it produces is returned rather than run, so no tmux session and no worktree is
// ever created.
func newTestHome(t *testing.T, cfg *config.Config, repoPath string) *home {
	t.Helper()
	h := &home{
		ctx:          context.Background(),
		state:        stateDefault,
		appConfig:    cfg,
		program:      "claude",
		repoPath:     repoPath,
		menu:         ui.NewMenu(),
		errBox:       ui.NewErrBox(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		spinner:      spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		appState:     &fakeAppState{},
	}
	h.list = ui.NewList(&h.spinner, false)
	h.errBox.SetSize(200, 1)
	return h
}

func testConfig(profiles ...config.Profile) *config.Config {
	return &config.Config{
		DefaultProgram: "claude",
		BranchPrefix:   "carlos/",
		Profiles:       profiles,
	}
}

// press feeds a key to the model the way the event loop does. A key the menu highlights
// is swallowed once and re-sent, so feed it a second time.
func press(t *testing.T, h *home, msg tea.KeyMsg) tea.Cmd {
	t.Helper()
	_, cmd := h.handleKeyPress(msg)
	if h.keySent {
		_, cmd = h.handleKeyPress(msg)
	}
	return cmd
}

// runCmd executes a command the way the event loop does, feeding every message it
// produces back into the model. Batches are unpacked; the start command is never among
// them, because the flow returns it rather than running it.
func runCmd(t *testing.T, h *home, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runCmd(t, h, c)
		}
	case nil:
	default:
		h.Update(msg)
	}
}

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeRunes(t *testing.T, h *home, text string) {
	t.Helper()
	for _, r := range text {
		if r == ' ' {
			press(t, h, tea.KeyMsg{Type: tea.KeySpace})
			continue
		}
		press(t, h, key(string(r)))
	}
}

// startTitleStep presses n, accepts the active repository, and types a title, leaving the
// flow on the title step.
func startTitleStep(t *testing.T, h *home, title string) {
	t.Helper()
	press(t, h, key("n"))
	require.Equal(t, stateNewRepo, h.state)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, stateNewTitle, h.state)
	typeRunes(t, h, title)
	require.Equal(t, title, h.list.GetSelectedInstance().Title)
}

// newSession returns the instance the flow is building.
func newSession(h *home) *session.Instance {
	instances := h.list.GetInstances()
	return instances[len(instances)-1]
}

func TestCreationFlow_TitleThenBranchStartsTheSession(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))

	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateNewBranch, h.state, "the title step leads to the branch step")
	require.Equal(t, "carlos/new-feature", h.branchOverlay.Value(),
		"the branch field arrives pre-filled with the prefix and the sanitised title")

	instance := newSession(h)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateDefault, h.state, "submitting the branch starts the session")
	require.Equal(t, "carlos/new-feature", instance.Branch)
	require.False(t, instance.UsesExistingBranch())
	require.Equal(t, session.Loading, instance.Status)
}

func TestCreationFlow_TitleThatSanitisesToNothingStillSuggestsABranch(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))

	startTitleStep(t, h, "🚀🚀")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, "carlos/session", h.branchOverlay.Value())
}

func TestCreationFlow_TheSuggestionIsNotRecomputedAfterAnEdit(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))

	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	// Clear the field and type an unrelated branch name.
	for range "carlos/new-feature" {
		press(t, h, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	require.Equal(t, "", h.branchOverlay.Value())
	typeRunes(t, h, "hotfix/login")

	// Results arriving for an earlier filter must not disturb what was typed.
	h.Update(branchSearchResultMsg{branches: []string{"main"}, version: 0})
	require.Equal(t, "hotfix/login", h.branchOverlay.Value())

	instance := newSession(h)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, "hotfix/login", instance.Branch, "the typed name is what gets created")
}

func TestCreationFlow_InvalidBranchNameIsRejectedAndNeverRewritten(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))

	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	typeRunes(t, h, " oops")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateNewBranch, h.state, "an invalid name keeps the user on the branch step")
	require.Equal(t, "carlos/new-feature oops", h.branchOverlay.Value(), "the name is not rewritten")
	require.Contains(t, h.errBox.String(), "not a valid branch name")
	require.False(t, newSession(h).Started())
}

func TestCreationFlow_EmptyBranchNameIsRejected(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))

	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	for range "carlos/new-feature" {
		press(t, h, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateNewBranch, h.state)
	require.Contains(t, h.errBox.String(), "empty")
}

func TestCreationFlow_DuplicateTitleIsRejected(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))

	existing, err := session.NewInstance(session.InstanceOptions{
		Title: "New feature", Path: initTestRepoNamed(t, "acme-web"), Program: "claude"})
	require.NoError(t, err)
	h.list.AddInstance(existing)

	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateNewTitle, h.state, "a duplicate title keeps the user on the title step")
	require.Contains(t, h.errBox.String(), "already")
	require.Contains(t, h.errBox.String(), existing.RepoPath(),
		"the message names the repository the session that holds the title lives in, in "+
			"full: two checkouts of one project have the same directory name")
}

func TestCreationFlow_EmptyTitleIsRejected(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))

	press(t, h, key("n"))
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateNewTitle, h.state)
	require.Contains(t, h.errBox.String(), "empty")
}

func TestCreationFlow_AbandonedAtEveryStep(t *testing.T) {
	cancels := []struct {
		name string
		msg  tea.KeyMsg
	}{
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}},
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}},
	}
	steps := []struct {
		name string
		toIt func(t *testing.T, h *home)
	}{
		{
			name: "repository step",
			toIt: func(t *testing.T, h *home) {
				press(t, h, key("n"))
				require.Equal(t, stateNewRepo, h.state)
			},
		},
		{
			name: "title step",
			toIt: func(t *testing.T, h *home) { startTitleStep(t, h, "New feature") },
		},
		{
			name: "branch step",
			toIt: func(t *testing.T, h *home) {
				startTitleStep(t, h, "New feature")
				press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
				require.Equal(t, stateNewBranch, h.state)
			},
		},
		{
			name: "program step",
			toIt: func(t *testing.T, h *home) {
				startTitleStep(t, h, "New feature")
				press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
				press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
				require.Equal(t, stateNewProgram, h.state)
			},
		},
	}

	for _, step := range steps {
		for _, cancel := range cancels {
			t.Run(step.name+" with "+cancel.name, func(t *testing.T) {
				cfg := testConfig()
				if step.name == "program step" {
					cfg = testConfig(
						config.Profile{Name: "claude", Program: "claude"},
						config.Profile{Name: "aider", Program: "aider"},
					)
				}
				h := newTestHome(t, cfg, initTestRepo(t))
				step.toIt(t, h)

				press(t, h, cancel.msg)

				require.Equal(t, stateDefault, h.state)
				require.Equal(t, 0, h.list.NumInstances(), "the half-created session is discarded")
				require.Nil(t, h.repoOverlay)
				require.Nil(t, h.branchOverlay)
				require.Nil(t, h.profileOverlay)
			})
		}
	}
}

func TestCreationFlow_ProgramStepOnlyAppearsWithSeveralProfiles(t *testing.T) {
	t.Run("a single profile skips the step", func(t *testing.T) {
		h := newTestHome(t, testConfig(config.Profile{Name: "claude", Program: "claude --verbose"}), initTestRepo(t))

		startTitleStep(t, h, "Solo")
		press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
		instance := newSession(h)
		press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

		require.Equal(t, stateDefault, h.state)
		require.Equal(t, "claude", instance.Program, "the session runs the configured default program")
	})

	t.Run("several profiles offer a choice", func(t *testing.T) {
		h := newTestHome(t, testConfig(
			config.Profile{Name: "claude", Program: "claude"},
			config.Profile{Name: "aider", Program: "aider --model gpt"},
		), initTestRepo(t))

		startTitleStep(t, h, "Duo")
		press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
		instance := newSession(h)
		press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

		require.Equal(t, stateNewProgram, h.state)
		press(t, h, tea.KeyMsg{Type: tea.KeyRight})
		press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

		require.Equal(t, stateDefault, h.state)
		require.Equal(t, "aider --model gpt", instance.Program, "the chosen profile's program is what runs")
	})
}

func TestCreationFlow_ShiftNStartsNothing(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))

	press(t, h, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})

	require.Equal(t, stateDefault, h.state)
	require.Equal(t, 0, h.list.NumInstances())
}

func TestBranchStep_ListsAndFiltersTheRepositoryBranches(t *testing.T) {
	repo := initTestRepo(t)
	runGit(t, repo, "branch", "carlos/login-fix")
	runGit(t, repo, "branch", "carlos/logout-fix")
	runGit(t, repo, "branch", "release/1.0")

	h := newTestHome(t, testConfig(), repo)
	startTitleStep(t, h, "New feature")
	// The step opens by listing the repository's branches.
	runCmd(t, h, press(t, h, tea.KeyMsg{Type: tea.KeyEnter}))
	rendered := h.branchOverlay.Render()
	require.Contains(t, rendered, "carlos/login-fix")
	require.Contains(t, rendered, "release/1.0")

	// Typing filters them.
	for range "carlos/new-feature" {
		press(t, h, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	typeRunes(t, h, "logout")
	runCmd(t, h, h.runBranchSearch(h.branchOverlay.Filter(), h.branchOverlay.FilterVersion()))

	rendered = h.branchOverlay.Render()
	require.Contains(t, rendered, "carlos/logout-fix")
	require.NotContains(t, rendered, "release/1.0")
}

func TestBranchStep_SelectingABranchWorksOnIt(t *testing.T) {
	repo := initTestRepo(t)
	runGit(t, repo, "branch", "carlos/already-here")

	h := newTestHome(t, testConfig(), repo)
	startTitleStep(t, h, "New feature")
	runCmd(t, h, press(t, h, tea.KeyMsg{Type: tea.KeyEnter}))

	// Move onto the listed branch: the field takes its name.
	press(t, h, tea.KeyMsg{Type: tea.KeyDown})
	require.Equal(t, "carlos/already-here", h.branchOverlay.Value())

	instance := newSession(h)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, "carlos/already-here", instance.Branch)
	require.True(t, instance.UsesExistingBranch(), "the session works on the branch rather than creating it")
}

func TestBranchStep_TypingAnExistingBranchIsTheSameAsSelectingIt(t *testing.T) {
	repo := initTestRepo(t)
	runGit(t, repo, "branch", "carlos/already-here")

	h := newTestHome(t, testConfig(), repo)
	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	for range "carlos/new-feature" {
		press(t, h, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	typeRunes(t, h, "carlos/already-here")

	instance := newSession(h)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, "carlos/already-here", instance.Branch)
	require.True(t, instance.UsesExistingBranch())
}

func TestBranchStep_ANameThatMatchesNothingCreatesANewBranch(t *testing.T) {
	repo := initTestRepo(t)

	h := newTestHome(t, testConfig(), repo)
	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	instance := newSession(h)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, "carlos/new-feature", instance.Branch)
	require.False(t, instance.UsesExistingBranch())
}

func TestBranchStep_StaleSearchResultsAreIgnored(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))
	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	stale := h.branchOverlay.FilterVersion()
	typeRunes(t, h, "x")

	h.Update(branchSearchResultMsg{branches: []string{"stale/branch"}, version: stale})
	require.NotContains(t, h.branchOverlay.Render(), "stale/branch")

	h.Update(branchSearchResultMsg{branches: []string{"fresh/branch"}, version: h.branchOverlay.FilterVersion()})
	require.Contains(t, h.branchOverlay.Render(), "fresh/branch")
}

func TestBranchStep_EditsAreDebouncedBeforeLookingAnythingUp(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))
	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	msgs := collect(t, press(t, h, key("x")))

	var searched, resolved bool
	for _, msg := range msgs {
		switch msg := msg.(type) {
		case branchSearchDebounceMsg:
			searched = true
			require.Equal(t, h.branchOverlay.Filter(), msg.filter)
			require.Equal(t, h.branchOverlay.FilterVersion(), msg.version)
		case branchOutcomeDebounceMsg:
			resolved = true
			require.Equal(t, h.branchOverlay.Value(), msg.name)
			require.Equal(t, h.branchOverlay.ValueVersion(), msg.version)
		}
	}
	require.True(t, searched, "an edit schedules a debounced search")
	require.True(t, resolved, "an edit schedules a debounced resolve of what the name would do")
}

// collect runs a command and returns the messages it produces, unpacking batches. Unlike
// runCmd it does not feed them back into the model.
func collect(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var msgs []tea.Msg
		for _, c := range msg {
			msgs = append(msgs, collect(t, c)...)
		}
		return msgs
	case nil:
		return nil
	default:
		return []tea.Msg{msg}
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	return initTestRepoNamed(t, "acme-tools")
}

func initTestRepoNamed(t *testing.T, name string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), name)
	runGit(t, "", "init", repo)
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0644))
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "initial")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
}

func TestBranchStep_ABranchThatExistsOnlyOnTheRemoteIsWorkedOn(t *testing.T) {
	origin := initTestRepo(t)
	runGit(t, origin, "branch", "carlos/remote-only")

	clone := filepath.Join(t.TempDir(), "acme-tools")
	runGit(t, "", "clone", origin, clone)

	h := newTestHome(t, testConfig(), clone)
	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	for range "carlos/new-feature" {
		press(t, h, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	typeRunes(t, h, "carlos/remote-only")

	instance := newSession(h)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, "carlos/remote-only", instance.Branch)
	require.True(t, instance.UsesExistingBranch(),
		"a branch that exists only on the remote is worked on, not created")
}

func TestBranchStep_APickedBranchSurvivesLaterSearchResults(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))
	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	version := h.branchOverlay.FilterVersion()
	h.Update(branchSearchResultMsg{branches: []string{"carlos/one", "carlos/two"}, version: version})
	press(t, h, tea.KeyMsg{Type: tea.KeyDown})
	press(t, h, tea.KeyMsg{Type: tea.KeyDown})
	require.Equal(t, "carlos/two", h.branchOverlay.Value())

	// Results arriving after the pick must not leave the highlight on another branch.
	h.Update(branchSearchResultMsg{branches: []string{"carlos/two", "carlos/one"}, version: version})
	require.Equal(t, "carlos/two", h.branchOverlay.Value())

	// If the picked branch is gone, the field falls back to the typed name.
	h.Update(branchSearchResultMsg{branches: []string{"carlos/one"}, version: version})
	require.Equal(t, "carlos/new-feature", h.branchOverlay.Value())
}

// Losing the picked branch is the one way the name changes without a key press, so it is
// the one way the outcome line could be left blank with nothing on its way to refill it.
func TestBranchStep_LosingThePickedBranchSaysWhatTheTypedNameWouldDo(t *testing.T) {
	repo := initTestRepo(t)
	runGit(t, repo, "branch", "carlos/already-here")

	h := newTestHome(t, testConfig(), repo)
	startTitleStep(t, h, "New feature")
	runCmd(t, h, press(t, h, tea.KeyMsg{Type: tea.KeyEnter}))
	h.branchOverlay.SetWidth(90) // the app sizes it from the window; unsized it truncates

	version := h.branchOverlay.FilterVersion()
	h.Update(branchSearchResultMsg{branches: []string{"carlos/already-here"}, version: version})
	press(t, h, tea.KeyMsg{Type: tea.KeyDown})
	runCmd(t, h, h.runBranchOutcome(h.branchOverlay.Value(), h.branchOverlay.ValueVersion()))
	require.Contains(t, h.branchOverlay.Render(), "Works on the existing branch")

	// The branch drops out of the list, so the step is back on the typed name and must
	// say what that one would do rather than nothing at all.
	_, cmd := h.Update(branchSearchResultMsg{branches: []string{"carlos/other"}, version: version})
	require.Equal(t, "carlos/new-feature", h.branchOverlay.Value())
	require.NotContains(t, h.branchOverlay.Render(), "Works on the existing branch")

	runCmd(t, h, cmd)
	require.Contains(t, h.branchOverlay.Render(), "Creates a new branch from HEAD")
}

func TestBranchStep_ABranchAlreadyCheckedOutIsRejected(t *testing.T) {
	repo := initTestRepo(t)
	runGit(t, repo, "worktree", "add", "-b", "carlos/busy", filepath.Join(t.TempDir(), "busy"))

	h := newTestHome(t, testConfig(), repo)
	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	for range "carlos/new-feature" {
		press(t, h, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	typeRunes(t, h, "carlos/busy")

	instance := newSession(h)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateNewBranch, h.state, "the failure arrives before the session is created")
	require.Contains(t, h.errBox.String(), "already checked out")
	require.Empty(t, instance.Branch)
	require.False(t, instance.Started())
}

func TestBranchStep_SaysWhatTheNameWouldDo(t *testing.T) {
	repo := initTestRepo(t)
	runGit(t, repo, "branch", "carlos/already-here")
	runGit(t, repo, "worktree", "add", "-b", "carlos/busy", filepath.Join(t.TempDir(), "busy"))

	h := newTestHome(t, testConfig(), repo)
	startTitleStep(t, h, "New feature")

	// The step opens already saying what the suggestion would do.
	runCmd(t, h, press(t, h, tea.KeyMsg{Type: tea.KeyEnter}))
	h.branchOverlay.SetWidth(90) // the app sizes it from the window; unsized it truncates
	require.Contains(t, h.branchOverlay.Render(), "Creates a new branch from HEAD")

	retype := func(name string) {
		for range h.branchOverlay.Value() {
			press(t, h, tea.KeyMsg{Type: tea.KeyBackspace})
		}
		typeRunes(t, h, name)
		runCmd(t, h, h.runBranchOutcome(h.branchOverlay.Value(), h.branchOverlay.ValueVersion()))
	}

	retype("carlos/already-here")
	require.Contains(t, h.branchOverlay.Render(), "Works on the existing branch")
	require.Contains(t, h.branchOverlay.Render(), "last commit:")

	retype("carlos/busy")
	require.Contains(t, h.branchOverlay.Render(), "Already checked out at")

	retype("bad branch")
	require.Contains(t, h.branchOverlay.Render(), "Not a valid branch name")
}

func TestBranchStep_PickingABranchResolvesThatBranch(t *testing.T) {
	repo := initTestRepo(t)
	runGit(t, repo, "branch", "carlos/already-here")

	h := newTestHome(t, testConfig(), repo)
	startTitleStep(t, h, "New feature")
	runCmd(t, h, press(t, h, tea.KeyMsg{Type: tea.KeyEnter}))
	require.Contains(t, h.branchOverlay.Render(), "Creates a new branch from HEAD")

	// Moving onto a listed branch asks what that branch would do, not what was typed.
	cmd := press(t, h, tea.KeyMsg{Type: tea.KeyDown})
	require.Equal(t, "carlos/already-here", h.branchOverlay.Value())
	require.NotContains(t, h.branchOverlay.Render(), "Creates a new branch",
		"the outcome of the name that is no longer in the field is dropped at once")

	msgs := collect(t, cmd)
	require.Len(t, msgs, 1)
	debounce, ok := msgs[0].(branchOutcomeDebounceMsg)
	require.True(t, ok)
	require.Equal(t, "carlos/already-here", debounce.name)

	h.Update(debounce)
	runCmd(t, h, h.runBranchOutcome(debounce.name, debounce.version))
	require.Contains(t, h.branchOverlay.Render(), "Works on the existing branch")
}

// A paused session has no worktree, so git no longer reports its branch as checked out —
// but the branch is still hers, and taking it leaves her unable to resume.
func TestBranchStep_ABranchAPausedSessionHoldsIsRejected(t *testing.T) {
	repo := initTestRepo(t)
	runGit(t, repo, "branch", "carlos/paused-work")

	h := newTestHome(t, testConfig(), repo)
	paused, err := session.NewInstance(session.InstanceOptions{
		Title: "Paused work", Path: repo, Program: "claude",
	})
	require.NoError(t, err)
	paused.SetBranch("carlos/paused-work", true)
	paused.SetStatus(session.Paused)
	h.list.AddInstance(paused)

	// git sees nothing wrong with the branch: nobody has it checked out.
	_, checkedOut := git.BranchCheckedOutAt(repo, "carlos/paused-work")
	require.False(t, checkedOut)

	startTitleStep(t, h, "New feature")
	runCmd(t, h, press(t, h, tea.KeyMsg{Type: tea.KeyEnter}))
	h.branchOverlay.SetWidth(90)
	for range h.branchOverlay.Value() {
		press(t, h, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	typeRunes(t, h, "carlos/paused-work")
	runCmd(t, h, h.runBranchOutcome(h.branchOverlay.Value(), h.branchOverlay.ValueVersion()))

	require.Contains(t, h.branchOverlay.Render(), `Already used by session "Paused work"`,
		"the step names the session standing in the way, not a path")

	instance := newSession(h)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateNewBranch, h.state)
	require.Contains(t, h.errBox.String(), "Paused work")
	require.Empty(t, instance.Branch)
}

func TestRepoStep_WhatChoosingAPathWouldDo(t *testing.T) {
	repo := initTestRepo(t)
	nested := filepath.Join(repo, "cmd", "tool")
	require.NoError(t, os.MkdirAll(nested, 0755))
	active := initTestRepo(t)

	tests := []struct {
		name   string
		input  string
		root   string
		text   string
		blocks bool
	}{
		{
			name:  "a repository other than the active one",
			input: repo,
			root:  repo,
			text:  "New sessions in " + repo,
		},
		{
			name:  "a directory inside a repository resolves to its root",
			input: nested,
			root:  repo,
			text:  "New sessions in " + repo,
		},
		{
			name:  "the active repository",
			input: active,
			root:  active,
			text:  "Active repository",
		},
		{
			name:   "a directory that is not a repository",
			input:  filepath.Dir(repo),
			text:   "Not a git repository",
			blocks: true,
		},
		{
			name:   "a path that is not there",
			input:  filepath.Join(repo, "nowhere"),
			text:   "Path does not exist",
			blocks: true,
		},
		{
			name:   "a file",
			input:  filepath.Join(repo, "README.md"),
			text:   "Not a directory",
			blocks: true,
		},
		{
			name:   "nothing typed",
			input:  "   ",
			blocks: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome := repoChoice{typed: test.input, activeRepo: active}.resolve()
			require.Equal(t, test.root, outcome.root)
			require.Equal(t, test.text, outcome.text)
			require.Equal(t, test.blocks, outcome.blocks)
		})
	}
}

func TestRepoStep_HomeRelativeAndRepositoryRelativePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := initTestRepo(t)
	sibling := filepath.Join(filepath.Dir(repo), "other-tools")
	runGit(t, "", "init", sibling)

	t.Run("~ means the home directory", func(t *testing.T) {
		require.Equal(t, filepath.Join(home, "projects"), repoChoice{typed: "~/projects", activeRepo: repo}.expand())
	})

	t.Run("a relative path is read from the active repository", func(t *testing.T) {
		outcome := repoChoice{typed: "../other-tools", activeRepo: repo}.resolve()
		require.Equal(t, sibling, outcome.root, "not from the directory cs was launched in")
		require.False(t, outcome.blocks)
	})
}

// clearRepoField empties the repository field, whatever it was pre-filled with.
func clearRepoField(t *testing.T, h *home) {
	t.Helper()
	press(t, h, tea.KeyMsg{Type: tea.KeyCtrlU})
	require.Equal(t, "", h.repoOverlay.Value())
}

func TestRepoStep_OpensOnTheActiveRepository(t *testing.T) {
	repo := initTestRepo(t)
	h := newTestHome(t, testConfig(), repo)

	press(t, h, key("n"))

	require.Equal(t, stateNewRepo, h.state, "creation opens on the repository step")
	require.Equal(t, repo, h.repoOverlay.Value(), "pre-filled with the active repository")
	require.Contains(t, h.repoOverlay.Render(), "Active repository")
	require.Equal(t, 0, h.list.NumInstances(), "no session exists until the repository is settled")
}

func TestRepoStep_ChoosingAnotherRepositoryMovesCreationThere(t *testing.T) {
	active := initTestRepoNamed(t, "acme-tools")
	other := initTestRepoNamed(t, "acme-web")
	h := newTestHome(t, testConfig(), active)

	press(t, h, key("n"))
	clearRepoField(t, h)
	typeRunes(t, h, other)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateNewTitle, h.state)
	require.Equal(t, other, h.repoPath, "the chosen repository becomes the active one")
	require.Equal(t, other, newSession(h).Path, "the session is created in it")

	// And it is the one offered from now on.
	press(t, h, tea.KeyMsg{Type: tea.KeyEsc})
	press(t, h, key("n"))
	require.Equal(t, other, h.repoOverlay.Value())
}

func TestRepoStep_ADirectoryInsideARepositoryNamesTheRepository(t *testing.T) {
	repo := initTestRepo(t)
	nested := filepath.Join(repo, "cmd", "tool")
	require.NoError(t, os.MkdirAll(nested, 0755))
	h := newTestHome(t, testConfig(), initTestRepoNamed(t, "acme-web"))

	press(t, h, key("n"))
	clearRepoField(t, h)
	typeRunes(t, h, nested)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, repo, h.repoPath, "the path is resolved to the repository root")
	require.Equal(t, repo, newSession(h).Path)
}

func TestRepoStep_APathThatIsNoRepositoryIsRefused(t *testing.T) {
	active := initTestRepo(t)
	h := newTestHome(t, testConfig(), active)

	press(t, h, key("n"))
	clearRepoField(t, h)
	typeRunes(t, h, t.TempDir())
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateNewRepo, h.state, "the step keeps the user until the path is one")
	require.Contains(t, h.errBox.String(), "not a git repository")
	require.Equal(t, active, h.repoPath, "the active repository is left alone")
	require.Equal(t, 0, h.list.NumInstances())
}

func TestRepoStep_AnEmptyPathIsRefused(t *testing.T) {
	h := newTestHome(t, testConfig(), initTestRepo(t))

	press(t, h, key("n"))
	clearRepoField(t, h)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateNewRepo, h.state)
	require.Contains(t, h.errBox.String(), "empty")
}

// Launching cs outside a repository used to fail when the worktree was created, several
// steps later. The step is where it is caught now.
func TestRepoStep_LaunchedOutsideARepositoryCreationCannotGoOn(t *testing.T) {
	h := newTestHome(t, testConfig(), t.TempDir())

	press(t, h, key("n"))
	require.Contains(t, h.repoOverlay.Render(), "Not a git repository")

	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, stateNewRepo, h.state)
	require.Equal(t, 0, h.list.NumInstances())
}

func TestRepoStep_OffersTheActiveOneThenTheChosenThenTheOnesWithSessions(t *testing.T) {
	active := initTestRepoNamed(t, "acme-tools")
	chosen := initTestRepoNamed(t, "acme-web")
	withSession := initTestRepoNamed(t, "zeta-api")
	gone := filepath.Join(t.TempDir(), "deleted")

	h := newTestHome(t, testConfig(), active)
	h.appState.(*fakeAppState).knownRepos = []string{chosen, gone}
	sess, err := session.NewInstance(session.InstanceOptions{
		Title: "Elsewhere", Path: withSession, Program: "claude"})
	require.NoError(t, err)
	h.list.AddInstance(sess)

	require.Equal(t, []string{active, chosen, withSession}, h.knownRepos(),
		"a repository that is no longer on disk is not somewhere a session can be created")
}

func TestRepoStep_PickingFromTheListAndFilteringIt(t *testing.T) {
	active := initTestRepoNamed(t, "acme-tools")
	chosen := initTestRepoNamed(t, "zeta-api")
	h := newTestHome(t, testConfig(), active)
	h.appState.(*fakeAppState).knownRepos = []string{chosen}

	press(t, h, key("n"))
	require.Contains(t, h.repoOverlay.Render(), "zeta-api")

	// Typing filters the list, and the filtered repository can be picked.
	clearRepoField(t, h)
	typeRunes(t, h, "zeta")
	require.NotContains(t, h.repoOverlay.Render(), "acme-tools")
	press(t, h, tea.KeyMsg{Type: tea.KeyDown})
	require.Equal(t, chosen, h.repoOverlay.Value())

	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, chosen, h.repoPath)
}

// The field opens holding a path nobody typed, so the list opens unfiltered. Once the
// field has been edited the list is whatever the field says, and clearing the field is the
// way back: a filter of nothing matches every known repository.
func TestRepoStep_ClearingTheFieldBringsTheWholeListBack(t *testing.T) {
	active := initTestRepoNamed(t, "acme-tools")
	chosen := initTestRepoNamed(t, "zeta-api")
	h := newTestHome(t, testConfig(), active)
	h.appState.(*fakeAppState).knownRepos = []string{chosen}

	press(t, h, key("n"))
	require.Contains(t, h.repoOverlay.Render(), "zeta-api", "the step opens on the whole list")

	// Typing onto the pre-filled path narrows the list to nothing, which is honest: no
	// known repository is called that.
	typeRunes(t, h, "x")
	require.Contains(t, h.repoOverlay.Render(), "No matching repositories")

	press(t, h, tea.KeyMsg{Type: tea.KeyCtrlU})
	require.Equal(t, "", h.repoOverlay.Value())
	require.Contains(t, h.repoOverlay.Render(), "zeta-api", "the list the step opened with")
	require.Contains(t, h.repoOverlay.Render(), "acme-tools")
}

// ctrl+w walks back up a typed path a segment at a time, so a wrong turn costs one key
// rather than one per character of the directory that was wrong.
func TestRepoStep_CtrlWDropsTheLastPathSegment(t *testing.T) {
	active := initTestRepoNamed(t, "acme-tools")
	h := newTestHome(t, testConfig(), active)
	sep := string(filepath.Separator)

	press(t, h, key("n"))
	press(t, h, tea.KeyMsg{Type: tea.KeyCtrlW})

	require.Equal(t, filepath.Dir(active)+sep, h.repoOverlay.Value())
}

func TestRepoStep_AChosenRepositoryIsRememberedForNextTime(t *testing.T) {
	active := initTestRepoNamed(t, "acme-tools")
	other := initTestRepoNamed(t, "acme-web")
	gone := filepath.Join(t.TempDir(), "deleted")

	h := newTestHome(t, testConfig(), active)
	state := h.appState.(*fakeAppState)
	state.knownRepos = []string{gone}

	press(t, h, key("n"))
	clearRepoField(t, h)
	typeRunes(t, h, other)
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, []string{other}, state.knownRepos,
		"the chosen repository is remembered and the one that is gone is dropped")

	// Abandoning the rest of the flow does not unremember it: opening the step is what
	// says a repository is worth offering.
	press(t, h, tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, []string{other}, state.knownRepos)
}

func TestRepoStep_TabCompletesAsFarAsThereIsOnlyOneAnswer(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"acme-tools", "acme-web", "zeta-api", ".hidden"} {
		require.NoError(t, os.MkdirAll(filepath.Join(base, name), 0755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(base, "notes.md"), []byte("x"), 0644))
	require.NoError(t, os.Symlink(filepath.Join(base, "zeta-api"), filepath.Join(base, "linked-repo")))
	require.NoError(t, os.Symlink(filepath.Join(base, "nowhere"), filepath.Join(base, "broken-link")))
	sep := string(filepath.Separator)

	tests := []struct {
		name  string
		typed string
		want  string
	}{
		{
			name:  "several candidates complete to what they share",
			typed: filepath.Join(base, "ac"),
			want:  filepath.Join(base, "acme-"),
		},
		{
			name:  "a single candidate completes to it, ready for the next segment",
			typed: filepath.Join(base, "z"),
			want:  filepath.Join(base, "zeta-api") + sep,
		},
		{
			name:  "a hidden directory is only offered when it is what was typed",
			typed: base + sep + ".",
			want:  base + sep + ".hidden" + sep,
		},
		{
			name:  "a symlink to a directory is a directory",
			typed: filepath.Join(base, "link"),
			want:  filepath.Join(base, "linked-repo") + sep,
		},
		{
			name:  "a symlink pointing nowhere is not",
			typed: filepath.Join(base, "broken"),
			want:  filepath.Join(base, "broken"),
		},
		{
			name:  "a file is not a repository and is not completed to",
			typed: filepath.Join(base, "not"),
			want:  filepath.Join(base, "not"),
		},
		{
			name:  "nothing matching hands back what was typed",
			typed: filepath.Join(base, "qqq"),
			want:  filepath.Join(base, "qqq"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, repoChoice{typed: test.typed, activeRepo: base}.complete().path)
		})
	}
}

// Tab that cannot extend the field has one thing left to offer: the directories it
// matched. Without it the step goes silent at exactly the moment the user has no way of
// knowing what is there — a repository they have never opened is in no list.
func TestRepoStep_TabWithNothingToAddListsWhatItMatched(t *testing.T) {
	active := initTestRepoNamed(t, "acme-tools")
	parent := filepath.Dir(active)
	for _, name := range []string{"btrfs-tools", "buttersync"} {
		require.NoError(t, os.MkdirAll(filepath.Join(parent, name), 0755))
	}
	h := newTestHome(t, testConfig(), active)

	press(t, h, key("n"))
	clearRepoField(t, h)
	typeRunes(t, h, filepath.Join(parent, "b"))
	press(t, h, tea.KeyMsg{Type: tea.KeyTab})

	require.Equal(t, filepath.Join(parent, "b"), h.repoOverlay.Value(),
		"the two share no more than the b that is already typed")
	require.Contains(t, h.repoOverlay.Render(), "Directories in")
	require.Contains(t, h.repoOverlay.Render(), "btrfs-tools")
	require.Contains(t, h.repoOverlay.Render(), "buttersync")

	// A candidate is picked the way a known repository is, and is what submitting uses.
	press(t, h, tea.KeyMsg{Type: tea.KeyDown})
	require.Equal(t, filepath.Join(parent, "btrfs-tools"), h.repoOverlay.Value())

	// Editing puts the known repositories back: the candidates answered a question about
	// one directory, and the field no longer holds that question.
	press(t, h, tea.KeyMsg{Type: tea.KeyCtrlU})
	require.Contains(t, h.repoOverlay.Render(), "Known repositories")
	require.NotContains(t, h.repoOverlay.Render(), "Directories in")
}

// The field is a name to search for and a path to a place, and which one it is cannot be
// told from the text. Both are matched, so neither costs the other.
func TestRepoStep_KnownRepositoriesMatchTheirNameAndTheirPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	active := initTestRepoNamed(t, "acme-tools")
	underHome := filepath.Join(home, "git", "zeta-api")
	require.NoError(t, os.MkdirAll(filepath.Dir(underHome), 0755))
	runGit(t, "", "init", underHome)
	sibling := filepath.Join(filepath.Dir(active), "acme-web")
	runGit(t, "", "init", sibling)

	h := newTestHome(t, testConfig(), active)
	h.appState.(*fakeAppState).knownRepos = []string{underHome, sibling}

	press(t, h, key("n"))

	clearRepoField(t, h)
	typeRunes(t, h, "zeta")
	require.Contains(t, h.repoOverlay.Render(), "zeta-api", "a bare word is a name to search for")

	// The repositories are stored as absolute paths, so a tilde matches none of them
	// until it has been expanded into the place it stands for.
	clearRepoField(t, h)
	typeRunes(t, h, "~/git/")
	require.Contains(t, h.repoOverlay.Render(), "zeta-api")
	require.NotContains(t, h.repoOverlay.Render(), "acme-web")

	// A relative path is read from the active repository, here as its sibling.
	clearRepoField(t, h)
	typeRunes(t, h, "../acme-web")
	require.Contains(t, h.repoOverlay.Render(), "acme-web")
	require.NotContains(t, h.repoOverlay.Render(), "zeta-api")
}

func TestRepoStep_TabCompletionFeedsTheField(t *testing.T) {
	active := initTestRepoNamed(t, "acme-tools")
	h := newTestHome(t, testConfig(), active)

	press(t, h, key("n"))
	clearRepoField(t, h)
	typeRunes(t, h, filepath.Join(filepath.Dir(active), "acme"))
	press(t, h, tea.KeyMsg{Type: tea.KeyTab})

	require.Equal(t, active+string(filepath.Separator), h.repoOverlay.Value())
	require.Contains(t, h.repoOverlay.Render(), "Active repository",
		"the completed path is resolved without waiting for another key")
	require.NotContains(t, h.repoOverlay.Render(), "No matching repositories",
		"the separator completion leaves behind must not unlist the repository it named")
}

// Two repositories can each have a branch of the same name. A session on one of them says
// nothing about the other.
func TestBranchStep_ABranchIsOnlyTakenInItsOwnRepository(t *testing.T) {
	other := initTestRepoNamed(t, "acme-web")

	takeBranch := func(t *testing.T, h *home, repo string) {
		t.Helper()
		taken, err := session.NewInstance(session.InstanceOptions{
			Title: "Elsewhere", Path: repo, Program: "claude"})
		require.NoError(t, err)
		taken.SetBranch("carlos/shared", false)
		h.list.AddInstance(taken)
	}

	t.Run("in another repository it is free", func(t *testing.T) {
		h := newTestHome(t, testConfig(), initTestRepoNamed(t, "acme-tools"))
		takeBranch(t, h, other)

		startTitleStep(t, h, "Shared")
		press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
		clearBranch := h.branchOverlay.Value()
		for range clearBranch {
			press(t, h, tea.KeyMsg{Type: tea.KeyBackspace})
		}
		typeRunes(t, h, "carlos/shared")

		instance := newSession(h)
		press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

		require.Equal(t, stateDefault, h.state)
		require.Equal(t, "carlos/shared", instance.Branch)
	})

	t.Run("in the same repository it is taken", func(t *testing.T) {
		repo := initTestRepoNamed(t, "acme-tools")
		h := newTestHome(t, testConfig(), repo)
		takeBranch(t, h, repo)

		startTitleStep(t, h, "Shared")
		press(t, h, tea.KeyMsg{Type: tea.KeyEnter})
		for range h.branchOverlay.Value() {
			press(t, h, tea.KeyMsg{Type: tea.KeyBackspace})
		}
		typeRunes(t, h, "carlos/shared")
		press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

		require.Equal(t, stateNewBranch, h.state)
		require.Contains(t, h.errBox.String(), "already used by session")
	})
}
