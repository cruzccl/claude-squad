package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// tempHome points HOME at a fresh directory so the worktrees root lives inside the
// test's own sandbox.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// initRepo creates a git repository with one commit at dir and returns dir.
func initRepo(t *testing.T, dir string) string {
	t.Helper()
	mustRunGit(t, "", "init", dir)
	mustRunGit(t, dir, "config", "user.name", "Test User")
	mustRunGit(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRunGit(t, dir, "add", "README.md")
	mustRunGit(t, dir, "commit", "-m", "initial")
	return dir
}

// worktreesRoot is where worktrees live under a temporary HOME.
func worktreesRoot(home string) string {
	return filepath.Join(home, ".claude-squad", "worktrees")
}

func TestSetup_NewBranchLandsUnderRepositoryDirectory(t *testing.T) {
	home := tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	wt, err := NewGitWorktreeForBranch(repo, "carlos/new-feature", "New feature", false)
	require.NoError(t, err)
	require.NoError(t, wt.Setup())

	require.Equal(t,
		filepath.Join(worktreesRoot(home), "acme-tools", "carlos", "new-feature"),
		wt.GetWorktreePath(),
		"the path is the repository directory followed by the branch name, with no uniqueness suffix")

	valid, err := wt.IsValidWorktree()
	require.NoError(t, err)
	require.True(t, valid)
}

func TestSetup_BranchWithSeveralSlashesNestsAsDirectories(t *testing.T) {
	home := tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	wt, err := NewGitWorktreeForBranch(repo, "carlos/feature/deep/work", "Deep work", false)
	require.NoError(t, err)
	require.NoError(t, wt.Setup())

	require.Equal(t,
		filepath.Join(worktreesRoot(home), "acme-tools", "carlos", "feature", "deep", "work"),
		wt.GetWorktreePath())
	require.DirExists(t, wt.GetWorktreePath(), "the whole parent chain is created before the worktree is added")
}

func TestSetup_SessionsInTheSameRepositoryShareAParentDirectory(t *testing.T) {
	home := tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	first, err := NewGitWorktreeForBranch(repo, "carlos/one", "One", false)
	require.NoError(t, err)
	require.NoError(t, first.Setup())

	second, err := NewGitWorktreeForBranch(repo, "carlos/two", "Two", false)
	require.NoError(t, err)
	require.NoError(t, second.Setup())

	repoDir := filepath.Join(worktreesRoot(home), "acme-tools")
	require.Equal(t, repoDir, filepath.Dir(filepath.Dir(first.GetWorktreePath())))
	require.Equal(t, repoDir, filepath.Dir(filepath.Dir(second.GetWorktreePath())))
}

func TestSetup_ClonesWithTheSameDirectoryNameShareOneDirectory(t *testing.T) {
	home := tempHome(t)
	firstClone := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	secondClone := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	first, err := NewGitWorktreeForBranch(firstClone, "carlos/one", "One", false)
	require.NoError(t, err)
	require.NoError(t, first.Setup())

	second, err := NewGitWorktreeForBranch(secondClone, "carlos/two", "Two", false)
	require.NoError(t, err)
	require.NoError(t, second.Setup())

	repoDir := filepath.Join(worktreesRoot(home), "acme-tools")
	require.Equal(t, filepath.Join(repoDir, "carlos", "one"), first.GetWorktreePath())
	require.Equal(t, filepath.Join(repoDir, "carlos", "two"), second.GetWorktreePath())
}

func TestSetup_NewBranchSucceedsOverAStaleDirectory(t *testing.T) {
	home := tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	stale := filepath.Join(worktreesRoot(home), "acme-tools", "carlos", "reused")
	require.NoError(t, os.MkdirAll(stale, 0755))
	leftover := filepath.Join(stale, "leftover.txt")
	require.NoError(t, os.WriteFile(leftover, []byte("stale\n"), 0644))

	wt, err := NewGitWorktreeForBranch(repo, "carlos/reused", "Reused", false)
	require.NoError(t, err)
	require.NoError(t, wt.Setup())

	require.Equal(t, stale, wt.GetWorktreePath())
	require.NoFileExists(t, leftover)
	valid, err := wt.IsValidWorktree()
	require.NoError(t, err)
	require.True(t, valid)
}

func TestCleanup_ExistingBranchIsLeftInTheRepository(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	mustRunGit(t, repo, "branch", "carlos/already-here")

	wt, err := NewGitWorktreeForBranch(repo, "carlos/already-here", "Already here", true)
	require.NoError(t, err)
	require.NoError(t, wt.Setup())
	require.NoError(t, wt.Cleanup())

	require.Contains(t, localBranches(t, repo), "carlos/already-here",
		"a session on a pre-existing branch leaves the branch behind when it is closed")
}

func TestCleanup_NewBranchIsRemovedWithTheSession(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	wt, err := NewGitWorktreeForBranch(repo, "carlos/temporary", "Temporary", false)
	require.NoError(t, err)
	require.NoError(t, wt.Setup())
	require.NoError(t, wt.Cleanup())

	require.NotContains(t, localBranches(t, repo), "carlos/temporary")
}

func TestNewGitWorktreeFromStorage_KeepsTheStoredPath(t *testing.T) {
	tempHome(t)

	// A session created under the old flat layout stores an absolute path, and that is
	// where it stays: nothing is moved into the new layout.
	stored := "/home/someone/.claude-squad/worktrees/loquesea_18d23e72149158db"
	wt := NewGitWorktreeFromStorage("/repos/acme-tools", stored, "Lo que sea", "carlos/loquesea", "abc123", false)

	require.Equal(t, stored, wt.GetWorktreePath())
}

func TestSetup_RefusesToTakeOverALiveWorktree(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	// A session running on the branch, with work that has not been committed.
	running, err := NewGitWorktreeForBranch(repo, "carlos/shared", "Running", false)
	require.NoError(t, err)
	require.NoError(t, running.Setup())
	work := filepath.Join(running.GetWorktreePath(), "work-in-progress.txt")
	require.NoError(t, os.WriteFile(work, []byte("uncommitted\n"), 0644))

	// A second session on the same branch wants the same directory, because the path is
	// derived from the branch name.
	second, err := NewGitWorktreeForBranch(repo, "carlos/shared", "Second", true)
	require.NoError(t, err)
	require.Equal(t, running.GetWorktreePath(), second.GetWorktreePath())

	err = second.Setup()

	require.Error(t, err, "setting up over a live worktree must fail rather than take it over")
	require.Contains(t, err.Error(), "already in use")
	require.FileExists(t, work, "the running session's uncommitted work survives")
	valid, err := running.IsValidWorktree()
	require.NoError(t, err)
	require.True(t, valid)
}

func TestBranchCheckedOutAt(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	wt, err := NewGitWorktreeForBranch(repo, "carlos/busy", "Busy", false)
	require.NoError(t, err)
	require.NoError(t, wt.Setup())

	where, taken := BranchCheckedOutAt(repo, "carlos/busy")
	require.True(t, taken)
	require.True(t, samePath(wt.GetWorktreePath(), where), "want %s, got %s", wt.GetWorktreePath(), where)

	// The repository's own working tree counts: that branch cannot be checked out again.
	current := strings.TrimSpace(mustRunGit(t, repo, "branch", "--show-current"))
	where, taken = BranchCheckedOutAt(repo, current)
	require.True(t, taken)
	require.True(t, samePath(repo, where))

	_, taken = BranchCheckedOutAt(repo, "carlos/nobody-has-this")
	require.False(t, taken)
}

func TestResolveBranch(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	mustRunGit(t, repo, "branch", "carlos/already-here")

	t.Run("a name nobody has creates a branch", func(t *testing.T) {
		outcome := ResolveBranch(repo, "carlos/brand-new")
		require.Equal(t, BranchNew, outcome.Kind)
	})

	t.Run("an existing branch is worked on, and says how old it is", func(t *testing.T) {
		outcome := ResolveBranch(repo, "carlos/already-here")
		require.Equal(t, BranchExisting, outcome.Kind)
		require.NotEmpty(t, outcome.LastCommit, "git's own relative date for the branch tip")
	})

	t.Run("a checked-out branch blocks", func(t *testing.T) {
		wt, err := NewGitWorktreeForBranch(repo, "carlos/busy", "Busy", false)
		require.NoError(t, err)
		require.NoError(t, wt.Setup())

		outcome := ResolveBranch(repo, "carlos/busy")
		require.Equal(t, BranchCheckedOut, outcome.Kind)
		require.True(t, samePath(wt.GetWorktreePath(), outcome.CheckedOutAt))
	})

	t.Run("checked out beats existing", func(t *testing.T) {
		// The repository's own branch exists and is checked out; the blocking answer wins.
		current := strings.TrimSpace(mustRunGit(t, repo, "branch", "--show-current"))
		require.Equal(t, BranchCheckedOut, ResolveBranch(repo, current).Kind)
	})

	t.Run("a malformed name is invalid", func(t *testing.T) {
		require.Equal(t, BranchInvalid, ResolveBranch(repo, "bad branch").Kind)
	})
}
