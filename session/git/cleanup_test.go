package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// localBranches returns the repository's local branch names.
func localBranches(t *testing.T, repo string) []string {
	t.Helper()
	out := mustRunGit(t, repo, "branch", "--format=%(refname:short)")
	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			branches = append(branches, line)
		}
	}
	return branches
}

// addWorktree creates a worktree for a new branch at an arbitrary path, so a test can
// build a layout this application no longer produces.
func addWorktree(t *testing.T, repo, branch, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	mustRunGit(t, repo, "worktree", "add", "-b", branch, path)
}

func TestCleanupWorktrees_FindsWorktreesInEveryLayout(t *testing.T) {
	home := tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	root := worktreesRoot(home)

	// The current layout, with a branch nested several levels deep.
	nested := filepath.Join(root, "acme-tools", "carlos", "feature", "deep")
	addWorktree(t, repo, "carlos/feature/deep", nested)
	// The old flat layout.
	flat := filepath.Join(root, "flat-session_18d23e72149158db")
	addWorktree(t, repo, "flat-session", flat)
	// The old branch-prefix layout.
	prefixed := filepath.Join(root, "carlos", "prefixed-session_18d23e72149158db")
	addWorktree(t, repo, "carlos/prefixed-session", prefixed)

	// A directory holding something that is not a worktree must survive.
	notes := filepath.Join(root, "notes")
	require.NoError(t, os.MkdirAll(notes, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(notes, "todo.txt"), []byte("keep me\n"), 0644))

	require.NoError(t, CleanupWorktrees())

	require.NoDirExists(t, nested)
	require.NoDirExists(t, flat)
	require.NoDirExists(t, prefixed)

	branches := localBranches(t, repo)
	require.NotContains(t, branches, "carlos/feature/deep")
	require.NotContains(t, branches, "flat-session")
	require.NotContains(t, branches, "carlos/prefixed-session")

	require.NoDirExists(t, filepath.Join(root, "acme-tools"), "empty repository directories are removed")
	require.NoDirExists(t, filepath.Join(root, "carlos"))
	require.FileExists(t, filepath.Join(notes, "todo.txt"), "directories still holding something are left alone")
	require.DirExists(t, root, "the worktrees root itself stays")
}

func TestCleanupWorktrees_DeletesTheBranchOfEachWorktreeAcrossRepositories(t *testing.T) {
	home := tempHome(t)
	first := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	second := initRepo(t, filepath.Join(t.TempDir(), "widgets"))
	root := worktreesRoot(home)

	// Same branch name in two repositories: matching by name alone is not enough.
	addWorktree(t, first, "carlos/shared", filepath.Join(root, "acme-tools", "carlos", "shared"))
	addWorktree(t, second, "carlos/shared", filepath.Join(root, "widgets", "carlos", "shared"))
	mustRunGit(t, second, "branch", "carlos/untouched")

	require.NoError(t, CleanupWorktrees())

	require.NotContains(t, localBranches(t, first), "carlos/shared")
	require.NotContains(t, localBranches(t, second), "carlos/shared")
	require.Contains(t, localBranches(t, second), "carlos/untouched",
		"a branch with no worktree of its own is left alone")
}

func TestCleanupWorktrees_SucceedsWhenNothingHasBeenCreatedYet(t *testing.T) {
	tempHome(t)
	require.NoError(t, CleanupWorktrees())
}
