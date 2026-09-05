package session

import (
	"claude-squad/cmd/cmd_test"
	"claude-squad/log"
	"claude-squad/session/tmux"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	log.Initialize(false)
	defer log.Close()
	os.Exit(m.Run())
}

// nullPtyFactory hands back a throwaway file instead of a real PTY.
type nullPtyFactory struct {
	t     *testing.T
	calls int
}

func (p *nullPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	p.calls++
	return os.OpenFile(filepath.Join(p.t.TempDir(), "pty"), os.O_CREATE|os.O_RDWR, 0644)
}

func (p *nullPtyFactory) Close() {}

// When the tmux server dies between runs, every session goes with it while the worktree
// and branch survive on disk. Restoring such an instance must park it as Paused so the
// user can resume it. Returning an error instead is not an option: LoadInstances aborts on
// the first failure, so one dead session would hide every other instance.
// See https://github.com/smtg-ai/claude-squad/issues/216.
func TestStartPausesInstanceWhenTmuxSessionNoLongerExists(t *testing.T) {
	ptyFactory := &nullPtyFactory{t: t}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") {
				return fmt.Errorf("can't find session")
			}
			return nil
		},
	}

	instance, err := NewInstance(InstanceOptions{Title: "revived", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps("revived", "claude", ptyFactory, cmdExec))

	require.NoError(t, instance.Start(false), "a dead tmux session is recoverable, not a startup failure")
	require.Equal(t, Paused, instance.Status)
	require.True(t, instance.Started())
	require.Zero(t, ptyFactory.calls, "should not attach to a session that does not exist")
}

// The happy path is unchanged: an instance whose session survived comes back Running.
func TestStartRestoresInstanceWhenTmuxSessionSurvives(t *testing.T) {
	ptyFactory := &nullPtyFactory{t: t}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error { return nil },
	}

	instance, err := NewInstance(InstanceOptions{Title: "alive", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps("alive", "claude", ptyFactory, cmdExec))

	require.NoError(t, instance.Start(false))
	require.Equal(t, Running, instance.Status)
	require.Equal(t, 1, ptyFactory.calls)
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// sessionRepo creates a repository with one commit, under a HOME of its own so the
// session's worktree lands somewhere temporary.
func sessionRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "acme-tools")
	mustGit(t, "", "init", dir)
	mustGit(t, dir, "config", "user.name", "Test User")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644))
	mustGit(t, dir, "add", "README.md")
	mustGit(t, dir, "commit", "-m", "initial")
	return dir
}

// tmuxServer stands in for the tmux server across a whole session lifecycle: the session
// does not exist until something starts it. Starting one goes through the PTY factory
// while every other command goes through the command runner, so both are wired to the
// same flag.
type tmuxServer struct {
	t       *testing.T
	running bool
}

func (s *tmuxServer) Start(cmd *exec.Cmd) (*os.File, error) {
	if strings.Contains(cmd.String(), "new-session") {
		s.running = true
	}
	return os.OpenFile(filepath.Join(s.t.TempDir(), "pty"), os.O_CREATE|os.O_RDWR, 0644)
}

func (s *tmuxServer) Close() {}

func (s *tmuxServer) run(cmd *exec.Cmd) error {
	if strings.Contains(cmd.String(), "has-session") && !s.running {
		return fmt.Errorf("can't find session")
	}
	return nil
}

// Pausing a session commits what is in its worktree and then removes the worktree, so
// resuming rebuilds it from a branch that by now has commits on it. The base commit is
// what tells the kill those commits are there; were resuming to re-record it, the branch
// would read as untouched and D would delete the work the pause had just preserved.
func TestKillOutcomeKeepsTheBranchOfASessionResumedAfterAPause(t *testing.T) {
	repo := sessionRepo(t)
	server := &tmuxServer{t: t}

	instance, err := NewInstance(InstanceOptions{
		Title:   "paused-work",
		Path:    repo,
		Program: "claude",
		Branch:  "carlos/paused-work",
	})
	require.NoError(t, err)
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps(
		"paused-work", "claude", server, cmd_test.MockCmdExec{RunFunc: server.run}))
	require.NoError(t, instance.Start(true))

	// The agent leaves work in the worktree, and the pause commits it to the branch.
	require.NoError(t, os.WriteFile(
		filepath.Join(instance.gitWorktree.GetWorktreePath(), "work.txt"), []byte("work\n"), 0644))
	require.NoError(t, instance.Pause())
	require.NoError(t, instance.Resume())

	require.Equal(t, KillKeepsBranch, instance.KillOutcome().Kind,
		"the commits the pause made are exactly what a kill must not throw away")
}
