package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// commitInto writes a file in dir and commits it, so a branch gains a commit of its own.
func commitInto(t *testing.T, dir, message string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, message+".txt"), []byte(message+"\n"), 0644))
	mustRunGit(t, dir, "add", ".")
	mustRunGit(t, dir, "commit", "-m", message)
}

// sessionWorktree creates the worktree a session would have for branchName.
func sessionWorktree(t *testing.T, repo, branchName string, existing bool) *GitWorktree {
	t.Helper()
	wt, err := NewGitWorktreeForBranch(repo, branchName, "Session", existing)
	require.NoError(t, err)
	require.NoError(t, wt.Setup())
	return wt
}

func TestBranchIsUntouched_ABranchWithNoCommitsOfItsOwn(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	wt := sessionWorktree(t, repo, "carlos/untouched", false)

	untouched, err := wt.BranchIsUntouched()
	require.NoError(t, err)
	require.True(t, untouched, "a branch still pointing at the commit it was created from is untouched")
}

func TestBranchIsUntouched_OneCommitIsEnoughToTouchIt(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	wt := sessionWorktree(t, repo, "carlos/working", false)
	commitInto(t, wt.GetWorktreePath(), "work")

	untouched, err := wt.BranchIsUntouched()
	require.NoError(t, err)
	require.False(t, untouched)
}

func TestBranchIsUntouched_TheBaseBeingOvertakenChangesNothing(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	wt := sessionWorktree(t, repo, "carlos/untouched", false)
	// Someone else moves the branch this one was created from.
	commitInto(t, repo, "someone-elses-work")

	untouched, err := wt.BranchIsUntouched()
	require.NoError(t, err)
	require.True(t, untouched,
		"the answer is about what the session committed, not about what the base did since")
}

func TestBranchIsUntouched_WithoutABaseCommitTheBranchCannotBeShownUntouched(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	wt := sessionWorktree(t, repo, "carlos/untouched", false)

	// State persisted by older versions can have no base commit recorded.
	older := NewGitWorktreeFromStorage(repo, wt.GetWorktreePath(), "Session", "carlos/untouched", "", false)

	untouched, err := older.BranchIsUntouched()
	require.NoError(t, err)
	require.False(t, untouched)
}

func TestWorktreeIsDirty_AnUntrackedFileCounts(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	wt := sessionWorktree(t, repo, "carlos/dirty", false)

	dirty, err := wt.WorktreeIsDirty()
	require.NoError(t, err)
	require.False(t, dirty, "a freshly created worktree is clean")

	require.NoError(t, os.WriteFile(filepath.Join(wt.GetWorktreePath(), "scratch.txt"), []byte("x\n"), 0644))

	dirty, err = wt.WorktreeIsDirty()
	require.NoError(t, err)
	require.True(t, dirty, "a file git has never seen is still work that would be discarded")
}

func TestWorktreeIsDirty_AWorktreeThatIsNotThereIsNotDirty(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	wt := sessionWorktree(t, repo, "carlos/paused", false)

	// What Pause leaves behind: the branch, and no worktree.
	require.NoError(t, wt.Remove())

	dirty, err := wt.WorktreeIsDirty()
	require.NoError(t, err)
	require.False(t, dirty)
}

func TestCleanup_DeletesANewBranchNothingWasCommittedTo(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	wt := sessionWorktree(t, repo, "carlos/abandoned", false)

	require.NoError(t, wt.Cleanup())

	require.NotContains(t, localBranches(t, repo), "carlos/abandoned",
		"deleting a branch created and never used loses nothing")
	require.NoDirExists(t, wt.GetWorktreePath())
}

func TestCleanup_KeepsANewBranchThatHasCommits(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	wt := sessionWorktree(t, repo, "carlos/working", false)
	commitInto(t, wt.GetWorktreePath(), "work")

	require.NoError(t, wt.Cleanup())

	require.Contains(t, localBranches(t, repo), "carlos/working",
		"ending a session is not a reason to throw away the commits made in it")
	require.NoDirExists(t, wt.GetWorktreePath())
}

func TestCleanup_KeepsAPreExistingBranchEitherWay(t *testing.T) {
	for _, tc := range []struct {
		name    string
		commits bool
	}{
		{"untouched", false},
		{"with commits", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tempHome(t)
			repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
			mustRunGit(t, repo, "branch", "carlos/theirs")

			wt := sessionWorktree(t, repo, "carlos/theirs", true)
			if tc.commits {
				commitInto(t, wt.GetWorktreePath(), "work")
			}

			require.NoError(t, wt.Cleanup())

			require.Contains(t, localBranches(t, repo), "carlos/theirs",
				"a branch the session did not create is never its to delete")
		})
	}
}

func TestBranchCheckedOutAtOtherWorktree_TheSessionsOwnWorktreeIsNotAnObstacle(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	wt := sessionWorktree(t, repo, "carlos/mine", false)

	where, taken := wt.BranchCheckedOutAtOtherWorktree()
	require.False(t, taken, "a session always holds its own branch; that cannot block it")
	require.Empty(t, where)
}

func TestBranchCheckedOutAtOtherWorktree_AnyOtherWorktreeIsReportedWithItsPath(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))

	// The session is paused, so its own worktree is gone and someone has taken the branch
	// into a working copy of their own.
	wt := sessionWorktree(t, repo, "carlos/shared", false)
	require.NoError(t, wt.Remove())
	theirs := filepath.Join(t.TempDir(), "their-copy")
	mustRunGit(t, repo, "worktree", "add", theirs, "carlos/shared")

	where, taken := wt.BranchCheckedOutAtOtherWorktree()
	require.True(t, taken)
	require.Equal(t, theirs, where, "the error has to say where to go and deal with it")
}

func TestBranchCheckedOutAtOtherWorktree_TheMainWorkingTreeCountsToo(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	current := mustRunGit(t, repo, "branch", "--show-current")
	mainBranch := current[:len(current)-1]

	// A session on the branch the repository itself has checked out.
	wt := NewGitWorktreeFromStorage(repo, filepath.Join(t.TempDir(), "nowhere"), "Session", mainBranch, "", false)

	where, taken := wt.BranchCheckedOutAtOtherWorktree()
	require.True(t, taken)
	require.Equal(t, repo, where)
}

// The base commit is what tells an untouched branch from one that has been worked on, and
// Setup runs again on every resume: were it re-recorded there, the base would become the
// branch tip and the work done before the pause would stop counting.
func TestSetup_ResumingASessionKeepsTheCommitItStartedFrom(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	wt := sessionWorktree(t, repo, "carlos/paused-work", false)
	base := wt.GetBaseCommitSHA()

	commitInto(t, wt.GetWorktreePath(), "work")
	// What Pause and Resume do to the worktree: it goes, and Setup builds it again from
	// the branch.
	require.NoError(t, wt.Remove())
	require.NoError(t, wt.Setup())

	require.Equal(t, base, wt.GetBaseCommitSHA(),
		"the base is the commit the session was created from, not wherever its branch has got to")
}

func TestCleanup_KeepsABranchCommittedToBeforeAPauseAndResume(t *testing.T) {
	tempHome(t)
	repo := initRepo(t, filepath.Join(t.TempDir(), "acme-tools"))
	wt := sessionWorktree(t, repo, "carlos/paused-work", false)

	commitInto(t, wt.GetWorktreePath(), "work")
	require.NoError(t, wt.Remove())
	require.NoError(t, wt.Setup())

	require.NoError(t, wt.Cleanup())

	require.Contains(t, localBranches(t, repo), "carlos/paused-work",
		"a pause and a resume do not make the commits made before them disposable")
}
