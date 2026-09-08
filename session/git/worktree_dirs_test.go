package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCleanup_RemovesTheRepositoryDirectoryWithItsLastWorktree(t *testing.T) {
	home := tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	wt := sessionWorktree(t, repo, "carlos/feature/deep", false)

	require.NoError(t, wt.Cleanup())

	require.NoDirExists(t, filepath.Join(worktreesRoot(home), "acme-tools"),
		"a repository with no worktrees left is not shown in the worktrees root")
	require.DirExists(t, worktreesRoot(home), "the worktrees root itself stays")
}

func TestCleanup_KeepsTheRepositoryDirectoryWhileAnotherWorktreeIsThere(t *testing.T) {
	home := tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	first := sessionWorktree(t, repo, "carlos/one", false)
	second := sessionWorktree(t, repo, "carlos/two", false)

	require.NoError(t, first.Cleanup())

	require.DirExists(t, second.GetWorktreePath(), "the other session keeps its worktree")
	require.DirExists(t, filepath.Join(worktreesRoot(home), "acme-tools", "carlos"),
		"the shared parent is only removed once nothing is under it")
}

func TestRemove_PausingTheLastSessionRemovesTheRepositoryDirectory(t *testing.T) {
	home := tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	wt := sessionWorktree(t, repo, "carlos/paused", false)

	require.NoError(t, wt.Remove())

	require.NoDirExists(t, filepath.Join(worktreesRoot(home), "acme-tools"))
	require.Contains(t, localBranches(t, repo), "carlos/paused",
		"pausing still keeps the branch")
}

func TestRemoveDirectory_TakesTheEmptyDirectoriesWithIt(t *testing.T) {
	home := tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	wt := sessionWorktree(t, repo, "carlos/orphaned", false)

	// What Pause finds when git no longer knows the worktree: a directory and nothing else.
	mustRunGit(t, repo, "worktree", "remove", "-f", wt.GetWorktreePath())
	require.NoError(t, os.MkdirAll(wt.GetWorktreePath(), 0755))

	require.NoError(t, wt.RemoveDirectory())

	require.NoDirExists(t, filepath.Join(worktreesRoot(home), "acme-tools"))
}

func TestPruneEmptyWorktreeParents_LeavesWhatIsNotUnderTheWorktreesRoot(t *testing.T) {
	tempHome(t)
	elsewhere := t.TempDir()
	stored := filepath.Join(elsewhere, "someones", "worktree")
	require.NoError(t, os.MkdirAll(stored, 0755))
	require.NoError(t, os.RemoveAll(stored))

	pruneEmptyWorktreeParents(stored)

	require.DirExists(t, filepath.Join(elsewhere, "someones"),
		"a worktree stored outside the worktrees root is nobody else's directory to remove")
}
