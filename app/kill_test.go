package app

import (
	"claude-squad/cmd/cmd_test"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/session/tmux"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// These tests drive the D key against real temporary repositories: the outcome is now
// resolved before the confirmation is shown, so the handler itself touches git. They stay
// hermetic in the sense that matters — no tmux server is started, and nothing is written
// outside the temporary HOME and the temporary repositories.

// memoryStore keeps persisted sessions in memory, so a kill can be confirmed without
// touching the state file of whoever is running the tests.
type memoryStore struct{ data json.RawMessage }

func (s *memoryStore) SaveInstances(instancesJSON json.RawMessage) error {
	s.data = instancesJSON
	return nil
}

func (s *memoryStore) GetInstances() json.RawMessage {
	if len(s.data) == 0 {
		return json.RawMessage("[]")
	}
	return s.data
}

func (s *memoryStore) DeleteAllInstances() error {
	s.data = json.RawMessage("[]")
	return nil
}

// nullTmux hands back a tmux session that never talks to a tmux server.
func nullTmux(t *testing.T, title string) *tmux.TmuxSession {
	t.Helper()
	pty := &nullPty{t: t}
	exec := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return nil },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	return tmux.NewTmuxSessionWithDeps(title, "claude", pty, exec)
}

type nullPty struct{ t *testing.T }

func (p *nullPty) Start(*exec.Cmd) (*os.File, error) {
	return os.OpenFile(filepath.Join(p.t.TempDir(), "pty"), os.O_CREATE|os.O_RDWR, 0644)
}

func (p *nullPty) Close() {}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// newRepo creates a git repository with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "acme-tools")
	mustGit(t, "", "init", dir)
	mustGit(t, dir, "config", "user.name", "Test User")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644))
	mustGit(t, dir, "add", "README.md")
	mustGit(t, dir, "commit", "-m", "initial")
	return dir
}

// killTestHome builds a model with one session on branch, its worktree really on disk and
// its row selected. The session is parked as Paused, which is what keeps the fixture away
// from tmux; nothing in the kill path reads its status.
func killTestHome(t *testing.T, repo, branch string, existingBranch bool) (*home, *session.Instance) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	h := newTestHome(t, testConfig(), repo)
	storage, err := session.NewStorage(&memoryStore{})
	require.NoError(t, err)
	h.storage = storage

	worktree, err := git.NewGitWorktreeForBranch(repo, branch, "Session", existingBranch)
	require.NoError(t, err)
	require.NoError(t, worktree.Setup())

	instance, err := session.FromInstanceData(session.InstanceData{
		Title:   "Session",
		Path:    repo,
		Branch:  branch,
		Status:  session.Paused,
		Program: "claude",
		Worktree: session.GitWorktreeData{
			RepoPath:         worktree.GetRepoPath(),
			WorktreePath:     worktree.GetWorktreePath(),
			SessionName:      "Session",
			BranchName:       branch,
			BaseCommitSHA:    worktree.GetBaseCommitSHA(),
			IsExistingBranch: existingBranch,
		},
	})
	require.NoError(t, err)
	instance.SetTmuxSession(nullTmux(t, "Session"))

	h.list.AddInstance(instance)
	h.list.SetSelectedInstance(0)
	require.NoError(t, h.storage.SaveInstances(h.list.GetInstances()))

	return h, instance
}

// overlayText flattens the confirmation modal to one line, so an assertion can read the
// sentence the user is shown without pinning down where the box wraps it.
func overlayText(t *testing.T, h *home) string {
	t.Helper()
	require.NotNil(t, h.confirmationOverlay)
	var words []string
	for _, line := range strings.Split(h.confirmationOverlay.Render(), "\n") {
		words = append(words, strings.Fields(strings.Trim(line, "\u2502\u256d\u256e\u2570\u256f\u2500 "))...)
	}
	return strings.Join(words, " ")
}

// pressKill presses D the way the event loop delivers it.
func pressKill(t *testing.T, h *home) {
	t.Helper()
	press(t, h, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
}

func TestKill_ConfirmationSaysTheBranchWillBeDeleted(t *testing.T) {
	repo := newRepo(t)
	h, _ := killTestHome(t, repo, "carlos/abandoned", false)

	pressKill(t, h)

	require.Equal(t, stateConfirm, h.state)
	require.Contains(t, overlayText(t, h),
		"Kill session 'Session'? Branch carlos/abandoned will be deleted, as nothing was committed to it")
}

func TestKill_ConfirmationSaysTheBranchWillBeKeptWhenItHasCommits(t *testing.T) {
	repo := newRepo(t)
	h, instance := killTestHome(t, repo, "carlos/working", false)

	worktree := instance.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(worktree, "work.txt"), []byte("work\n"), 0644))
	mustGit(t, worktree, "add", ".")
	mustGit(t, worktree, "commit", "-m", "work")

	pressKill(t, h)

	require.Equal(t, stateConfirm, h.state)
	require.Contains(t, overlayText(t, h), "Branch carlos/working will be kept, as it has commits")
}

func TestKill_ConfirmationSaysTheBranchWillBeKeptWhenTheSessionDidNotCreateIt(t *testing.T) {
	repo := newRepo(t)
	mustGit(t, repo, "branch", "carlos/theirs")
	h, _ := killTestHome(t, repo, "carlos/theirs", true)

	pressKill(t, h)

	require.Equal(t, stateConfirm, h.state)
	require.Contains(t, overlayText(t, h),
		"Branch carlos/theirs will be kept, as the session did not create it")
}

func TestKill_UncommittedChangesAreRefusedWithoutAConfirmation(t *testing.T) {
	repo := newRepo(t)
	h, instance := killTestHome(t, repo, "carlos/dirty", false)

	require.NoError(t, os.WriteFile(
		filepath.Join(instance.GetWorktreePath(), "scratch.txt"), []byte("unsaved\n"), 0644))

	pressKill(t, h)

	require.Equal(t, stateDefault, h.state, "there is nothing to confirm about a kill that is refused")
	require.Nil(t, h.confirmationOverlay)
	require.Contains(t, h.errBox.String(), "uncommitted changes")
	require.Equal(t, 1, h.list.NumInstances(), "the session is still there")
}

func TestKill_TheRefusalPointsAtAttachingAndNotAtCheckout(t *testing.T) {
	repo := newRepo(t)
	h, instance := killTestHome(t, repo, "carlos/dirty", false)

	require.NoError(t, os.WriteFile(
		filepath.Join(instance.GetWorktreePath(), "scratch.txt"), []byte("unsaved\n"), 0644))

	pressKill(t, h)

	shown := h.errBox.String()
	require.Contains(t, shown, "'o'", "the way out is attaching and dealing with the changes there")
	require.NotContains(t, shown, "'c'", "the handoff key must not be offered as a save button")
}

func TestKill_ABranchHeldByAnotherWorktreeIsRefusedAndTheHolderNamed(t *testing.T) {
	repo := newRepo(t)
	h, instance := killTestHome(t, repo, "carlos/shared", false)

	// Pausing releases the branch; someone takes it into a working copy of their own.
	worktree, err := instance.GetGitWorktree()
	require.NoError(t, err)
	require.NoError(t, worktree.Remove())
	theirs := filepath.Join(t.TempDir(), "their-copy")
	mustGit(t, repo, "worktree", "add", theirs, "carlos/shared")

	pressKill(t, h)

	require.Equal(t, stateDefault, h.state)
	require.Nil(t, h.confirmationOverlay)
	require.Contains(t, h.errBox.String(), theirs)
	require.Equal(t, 1, h.list.NumInstances())
}

func TestKill_ConfirmingRemovesTheRowAndDeletesAnUntouchedBranch(t *testing.T) {
	repo := newRepo(t)
	h, _ := killTestHome(t, repo, "carlos/abandoned", false)

	pressKill(t, h)
	press(t, h, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})

	require.Equal(t, stateDefault, h.state)
	require.Zero(t, h.list.NumInstances(), "the row leaves the list")
	require.NotContains(t, mustGit(t, repo, "branch", "--format=%(refname:short)"), "carlos/abandoned")
}

func TestKill_ConfirmingRemovesTheRowAndKeepsABranchWithCommits(t *testing.T) {
	repo := newRepo(t)
	h, instance := killTestHome(t, repo, "carlos/working", false)

	worktree := instance.GetWorktreePath()
	require.NoError(t, os.WriteFile(filepath.Join(worktree, "work.txt"), []byte("work\n"), 0644))
	mustGit(t, worktree, "add", ".")
	mustGit(t, worktree, "commit", "-m", "work")

	pressKill(t, h)
	press(t, h, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})

	require.Zero(t, h.list.NumInstances())
	require.Contains(t, mustGit(t, repo, "branch", "--format=%(refname:short)"), "carlos/working",
		"the commits made in the session outlive it")
}

func TestKill_CancellingLeavesEverythingAlone(t *testing.T) {
	repo := newRepo(t)
	h, _ := killTestHome(t, repo, "carlos/abandoned", false)

	pressKill(t, h)
	press(t, h, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})

	require.Equal(t, stateDefault, h.state)
	require.Equal(t, 1, h.list.NumInstances())
	require.Contains(t, mustGit(t, repo, "branch", "--format=%(refname:short)"), "carlos/abandoned")
}

// The fate of the branch is the half of the modal that has to be noticed, so it does not
// share a line with the question.
func TestKill_TheBranchFateGetsALineOfItsOwn(t *testing.T) {
	repo := newRepo(t)
	h, _ := killTestHome(t, repo, "carlos/abandoned", false)

	pressKill(t, h)

	for _, line := range strings.Split(h.confirmationOverlay.Render(), "\n") {
		require.False(t,
			strings.Contains(line, "Kill session") && strings.Contains(line, "will be deleted"),
			"the question and what happens to the branch are read separately")
	}
}
