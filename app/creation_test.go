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

// startTitleStep presses n and types a title, leaving the flow on the title step.
func startTitleStep(t *testing.T, h *home, title string) {
	t.Helper()
	press(t, h, key("n"))
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
	h := newTestHome(t, testConfig(), t.TempDir())

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
	h := newTestHome(t, testConfig(), t.TempDir())

	startTitleStep(t, h, "🚀🚀")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, "carlos/session", h.branchOverlay.Value())
}

func TestCreationFlow_TheSuggestionIsNotRecomputedAfterAnEdit(t *testing.T) {
	h := newTestHome(t, testConfig(), t.TempDir())

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
	h := newTestHome(t, testConfig(), t.TempDir())

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
	h := newTestHome(t, testConfig(), t.TempDir())

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
	h := newTestHome(t, testConfig(), t.TempDir())

	existing, err := session.NewInstance(session.InstanceOptions{Title: "New feature", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	h.list.AddInstance(existing)

	startTitleStep(t, h, "New feature")
	press(t, h, tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateNewTitle, h.state, "a duplicate title keeps the user on the title step")
	require.Contains(t, h.errBox.String(), "already")
}

func TestCreationFlow_EmptyTitleIsRejected(t *testing.T) {
	h := newTestHome(t, testConfig(), t.TempDir())

	press(t, h, key("n"))
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
				h := newTestHome(t, cfg, t.TempDir())
				step.toIt(t, h)

				press(t, h, cancel.msg)

				require.Equal(t, stateDefault, h.state)
				require.Equal(t, 0, h.list.NumInstances(), "the half-created session is discarded")
				require.Nil(t, h.branchOverlay)
				require.Nil(t, h.profileOverlay)
			})
		}
	}
}

func TestCreationFlow_ProgramStepOnlyAppearsWithSeveralProfiles(t *testing.T) {
	t.Run("a single profile skips the step", func(t *testing.T) {
		h := newTestHome(t, testConfig(config.Profile{Name: "claude", Program: "claude --verbose"}), t.TempDir())

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
		), t.TempDir())

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
	h := newTestHome(t, testConfig(), t.TempDir())

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
	h := newTestHome(t, testConfig(), t.TempDir())
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
	h := newTestHome(t, testConfig(), t.TempDir())
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
	repo := filepath.Join(t.TempDir(), "acme-tools")
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
	h := newTestHome(t, testConfig(), t.TempDir())
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
