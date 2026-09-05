package git

import (
	"claude-squad/log"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MaxBranchSearchResults is the maximum number of branches returned by SearchBranches.
const MaxBranchSearchResults = 50

// FetchBranches fetches and prunes remote-tracking branches (best-effort, won't fail if offline).
func FetchBranches(repoPath string) {
	cmd := exec.Command("git", "-C", repoPath, "fetch", "--prune")
	_ = cmd.Run()
}

// SearchBranches searches for branches whose name contains filter (case-insensitive),
// ordered by most recently updated first. Returns at most MaxBranchSearchResults.
// If filter is empty, returns all branches up to the limit.
func SearchBranches(repoPath, filter string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "branch", "-a",
		"--sort=-committerdate",
		"--format=%(refname:short)")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %s (%w)", output, err)
	}

	seen := make(map[string]bool)
	var branches []string
	lower := strings.ToLower(filter)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "HEAD") {
			continue
		}
		name := strings.TrimPrefix(line, "origin/")
		if seen[name] {
			continue
		}
		seen[name] = true
		if filter != "" && !strings.Contains(strings.ToLower(name), lower) {
			continue
		}
		branches = append(branches, name)
		if len(branches) >= MaxBranchSearchResults {
			break
		}
	}
	return branches, nil
}

// runGitCommand executes a git command and returns any error
func (g *GitWorktree) runGitCommand(path string, args ...string) (string, error) {
	baseArgs := []string{"-C", path}
	cmd := exec.Command("git", append(baseArgs, args...)...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git command failed: %s (%w)", output, err)
	}

	return string(output), nil
}

// PushChanges commits and pushes changes in the worktree to the remote branch
func (g *GitWorktree) PushChanges(commitMessage string, open bool) error {
	if err := checkGHCLI(); err != nil {
		return err
	}

	// Check if there are any changes to commit
	isDirty, err := g.IsDirty()
	if err != nil {
		return fmt.Errorf("failed to check for changes: %w", err)
	}

	if isDirty {
		// Stage all changes
		if _, err := g.runGitCommand(g.worktreePath, "add", "."); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to stage changes: %w", err)
		}

		// Create commit
		if _, err := g.runGitCommand(g.worktreePath, "commit", "-m", commitMessage, "--no-verify"); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to commit changes: %w", err)
		}
	}

	// First push the branch to remote to ensure it exists
	pushCmd := exec.Command("gh", "repo", "sync", "--source", "-b", g.branchName)
	pushCmd.Dir = g.worktreePath
	if err := pushCmd.Run(); err != nil {
		// If sync fails, try creating the branch on remote first
		gitPushCmd := exec.Command("git", "push", "-u", "origin", g.branchName)
		gitPushCmd.Dir = g.worktreePath
		if pushOutput, pushErr := gitPushCmd.CombinedOutput(); pushErr != nil {
			log.ErrorLog.Print(pushErr)
			return fmt.Errorf("failed to push branch: %s (%w)", pushOutput, pushErr)
		}
	}

	// Now sync with remote
	syncCmd := exec.Command("gh", "repo", "sync", "-b", g.branchName)
	syncCmd.Dir = g.worktreePath
	if output, err := syncCmd.CombinedOutput(); err != nil {
		log.ErrorLog.Print(err)
		return fmt.Errorf("failed to sync changes: %s (%w)", output, err)
	}

	// Open the branch in the browser
	if open {
		if err := g.OpenBranchURL(); err != nil {
			// Just log the error but don't fail the push operation
			log.ErrorLog.Printf("failed to open branch URL: %v", err)
		}
	}

	return nil
}

// CommitChanges commits changes locally without pushing to remote
func (g *GitWorktree) CommitChanges(commitMessage string) error {
	// Check if there are any changes to commit
	isDirty, err := g.IsDirty()
	if err != nil {
		return fmt.Errorf("failed to check for changes: %w", err)
	}

	if isDirty {
		// Stage all changes
		if _, err := g.runGitCommand(g.worktreePath, "add", "."); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to stage changes: %w", err)
		}

		// Create commit (local only)
		if _, err := g.runGitCommand(g.worktreePath, "commit", "-m", commitMessage, "--no-verify"); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to commit changes: %w", err)
		}
	}

	return nil
}

// IsDirty checks if the worktree has uncommitted changes
func (g *GitWorktree) IsDirty() (bool, error) {
	output, err := g.runGitCommand(g.worktreePath, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("failed to check worktree status: %w", err)
	}
	return len(output) > 0, nil
}

// IsValidWorktree reports whether the worktree path exists and contains a
// .git entry, i.e. git can still recognize it as a working tree.
// Returns (false, nil) if the worktree is orphaned (path or .git missing).
func (g *GitWorktree) IsValidWorktree() (bool, error) {
	if _, err := os.Stat(g.worktreePath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to stat worktree path: %w", err)
	}
	if _, err := os.Stat(filepath.Join(g.worktreePath, ".git")); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to stat worktree .git: %w", err)
	}
	return true, nil
}

// WorktreeIsDirty reports whether the worktree holds uncommitted changes, untracked
// files included. A worktree that is not on disk holds nothing and so is not dirty: a
// paused session has none, and Pause commits whatever it finds before removing it.
func (g *GitWorktree) WorktreeIsDirty() (bool, error) {
	valid, err := g.IsValidWorktree()
	if err != nil {
		return false, err
	}
	if !valid {
		return false, nil
	}
	return g.IsDirty()
}

// BranchIsUntouched reports whether the branch still points at the commit the session was
// created from, so that deleting it would throw nothing away. It is asked of the
// repository rather than of the worktree, because a paused session has no worktree.
//
// The comparison is against the recorded base commit and not against the repository's
// current HEAD: were it HEAD, a branch with no commits of its own would read as touched
// as soon as anyone else moved the branch it came from.
func (g *GitWorktree) BranchIsUntouched() (bool, error) {
	// State written by older versions can be missing the base commit. Without it there is
	// nothing to compare against, so the branch cannot be shown to be untouched.
	if g.baseCommitSHA == "" {
		return false, nil
	}
	output, err := g.runGitCommand(g.repoPath, "rev-list", "--count",
		fmt.Sprintf("%s..%s", g.baseCommitSHA, g.branchName))
	if err != nil {
		return false, fmt.Errorf("failed to count commits on branch %s: %w", g.branchName, err)
	}
	return strings.TrimSpace(output) == "0", nil
}

// BranchCheckedOutAtOtherWorktree returns the path of a worktree other than this
// session's own that has the branch checked out, and whether there is one. The session's
// own worktree always holds its own branch, so it is never the obstacle; every other
// worktree is, including the repository's main working tree.
func (g *GitWorktree) BranchCheckedOutAtOtherWorktree() (string, bool) {
	where, taken := BranchCheckedOutAt(g.repoPath, g.branchName)
	if !taken || samePath(where, g.worktreePath) {
		return "", false
	}
	return where, true
}

// BranchDeletionLosesNothing reports whether deleting the session's branch would throw
// nothing away: the session created the branch, and nothing has been committed to it
// since. Every other answer keeps the branch, a git command that cannot answer included.
//
// Cleanup is the one that acts on this, and the kill confirmation asks it beforehand so
// that it can say what is about to happen. The two can disagree — the agent runs on while
// the confirmation is open and may commit in the gap — but only one way: the branch stops
// being untouched and Cleanup keeps what the confirmation promised to delete.
func (g *GitWorktree) BranchDeletionLosesNothing() bool {
	if g.isExistingBranch {
		return false
	}
	untouched, err := g.BranchIsUntouched()
	if err != nil {
		log.ErrorLog.Printf("could not tell whether branch %s is untouched, keeping it: %v", g.branchName, err)
		return false
	}
	return untouched
}

// OpenBranchURL opens the branch URL in the default browser
func (g *GitWorktree) OpenBranchURL() error {
	// Check if GitHub CLI is available
	if err := checkGHCLI(); err != nil {
		return err
	}

	cmd := exec.Command("gh", "browse", "--branch", g.branchName)
	cmd.Dir = g.worktreePath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to open branch URL: %w", err)
	}
	return nil
}
