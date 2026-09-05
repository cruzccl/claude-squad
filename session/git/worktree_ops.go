package git

import (
	"claude-squad/log"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Setup creates a new worktree for the session
func (g *GitWorktree) Setup() error {
	// The branch name nests as directories, so create the whole parent chain up front.
	if err := os.MkdirAll(filepath.Dir(g.worktreePath), 0755); err != nil {
		return fmt.Errorf("failed to create worktree parent directory: %w", err)
	}

	// If this worktree uses a pre-existing branch, always set up from that branch
	// (it may exist locally or only on the remote).
	if g.isExistingBranch {
		return g.setupFromExistingBranch()
	}

	// Check if branch exists using git CLI (much faster than go-git PlainOpen)
	_, err := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName))
	if err == nil {
		return g.setupFromExistingBranch()
	}
	return g.setupNewWorktree()
}

// worktreePathIsLive reports whether git has a worktree registered at this path and that
// worktree is still on disk. A live worktree belongs to a session that is using it; a
// registration without a directory, or a directory git never knew about, is stale.
func (g *GitWorktree) worktreePathIsLive() bool {
	if _, err := os.Lstat(filepath.Join(g.worktreePath, ".git")); err != nil {
		return false
	}
	for _, listing := range listWorktrees(g.repoPath) {
		if samePath(listing.path, g.worktreePath) {
			return true
		}
	}
	return false
}

// clearWorktreePath drops whatever occupies the worktree path: a registration git still
// remembers, and the directory itself. It refuses when a live worktree is there, because
// that directory belongs to another session and removing it would destroy the work in it.
// Worktree paths are derived from the branch name, so two sessions on one branch want the
// same directory — this is what stops the second from taking it.
func (g *GitWorktree) clearWorktreePath() error {
	if g.worktreePathIsLive() {
		return fmt.Errorf("worktree path %s is already in use", g.worktreePath)
	}
	_, _ = g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath) // Ignore error if worktree doesn't exist
	// If the directory is still there (orphaned, not registered with git), drop it so `git worktree add` won't fail.
	_ = os.RemoveAll(g.worktreePath)
	return nil
}

// setupFromExistingBranch creates a worktree from an existing branch
func (g *GitWorktree) setupFromExistingBranch() error {
	// Directory already created in Setup(), skip duplicate creation

	if err := g.clearWorktreePath(); err != nil {
		return err
	}

	// Check if the local branch exists
	_, localErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName))
	if localErr != nil {
		// Local branch doesn't exist — check if remote tracking branch exists
		_, remoteErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/remotes/origin/%s", g.branchName))
		if remoteErr != nil {
			return fmt.Errorf("branch %s not found locally or on remote", g.branchName)
		}
		// Create a local tracking branch via worktree add -b
		if _, err := g.runGitCommand(g.repoPath, "worktree", "add", "-b", g.branchName, g.worktreePath, fmt.Sprintf("origin/%s", g.branchName)); err != nil {
			return fmt.Errorf("failed to create worktree from remote branch %s: %w", g.branchName, err)
		}
		return g.recordBaseCommit()
	}

	// Create a new worktree from the existing local branch
	if _, err := g.runGitCommand(g.repoPath, "worktree", "add", g.worktreePath, g.branchName); err != nil {
		return fmt.Errorf("failed to create worktree from branch %s: %w", g.branchName, err)
	}

	return g.recordBaseCommit()
}

// recordBaseCommit stores the commit the session starts from. Diffs are computed
// against it, so leaving it unset makes every git diff invocation fail with an
// ambiguous argument error once the session is running.
func (g *GitWorktree) recordBaseCommit() error {
	output, err := g.runGitCommand(g.worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("failed to get base commit hash for branch %s: %w", g.branchName, err)
	}
	g.baseCommitSHA = strings.TrimSpace(output)
	return nil
}

// setupNewWorktree creates a new worktree from HEAD
func (g *GitWorktree) setupNewWorktree() error {
	if err := g.clearWorktreePath(); err != nil {
		return err
	}

	// Clean up any existing branch using git CLI (much faster than go-git PlainOpen)
	_, _ = g.runGitCommand(g.repoPath, "branch", "-D", g.branchName) // Ignore error if branch doesn't exist

	output, err := g.runGitCommand(g.repoPath, "rev-parse", "HEAD")
	if err != nil {
		if strings.Contains(err.Error(), "fatal: ambiguous argument 'HEAD'") ||
			strings.Contains(err.Error(), "fatal: not a valid object name") ||
			strings.Contains(err.Error(), "fatal: HEAD: not a valid object name") {
			return fmt.Errorf("this appears to be a brand new repository: please create an initial commit before creating an instance")
		}
		return fmt.Errorf("failed to get HEAD commit hash: %w", err)
	}
	headCommit := strings.TrimSpace(string(output))
	g.baseCommitSHA = headCommit

	// Create a new worktree from the HEAD commit
	// Otherwise, we'll inherit uncommitted changes from the previous worktree.
	// This way, we can start the worktree with a clean slate.
	// TODO: we might want to give an option to use main/master instead of the current branch.
	if _, err := g.runGitCommand(g.repoPath, "worktree", "add", "-b", g.branchName, g.worktreePath, headCommit); err != nil {
		return fmt.Errorf("failed to create worktree from commit %s: %w", headCommit, err)
	}

	return nil
}

// Cleanup removes the worktree and associated branch
func (g *GitWorktree) Cleanup() error {
	var errs []error

	// Check if worktree path exists before attempting removal
	if _, err := os.Stat(g.worktreePath); err == nil {
		// Remove the worktree using git command
		if _, err := g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath); err != nil {
			errs = append(errs, err)
		}
	} else if !os.IsNotExist(err) {
		// Only append error if it's not a "not exists" error
		errs = append(errs, fmt.Errorf("failed to check worktree path: %w", err))
	}

	// Delete the branch using git CLI, but skip if this is a pre-existing branch
	if !g.isExistingBranch {
		if _, err := g.runGitCommand(g.repoPath, "branch", "-D", g.branchName); err != nil {
			// Only log if it's not a "branch not found" error
			if !strings.Contains(err.Error(), "not found") {
				errs = append(errs, fmt.Errorf("failed to remove branch %s: %w", g.branchName, err))
			}
		}
	}

	// Prune the worktree to clean up any remaining references
	if err := g.Prune(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return g.combineErrors(errs)
	}

	return nil
}

// Remove removes the worktree but keeps the branch
func (g *GitWorktree) Remove() error {
	// Remove the worktree using git command
	if _, err := g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath); err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}

	return nil
}

// Prune removes all working tree administrative files and directories
func (g *GitWorktree) Prune() error {
	if _, err := g.runGitCommand(g.repoPath, "worktree", "prune"); err != nil {
		return fmt.Errorf("failed to prune worktrees: %w", err)
	}
	return nil
}

// findWorktreeDirs walks root and returns every directory that holds a worktree. The rule
// is uniform and needs no knowledge of which layout produced a directory: a directory
// containing a .git entry is a worktree, and anything else is descended into and treated
// the same way. Descent is recursive, because a branch name may contain several slashes.
func findWorktreeDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var found []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			found = append(found, dir)
			continue
		}
		nested, err := findWorktreeDirs(dir)
		if err != nil {
			return nil, err
		}
		found = append(found, nested...)
	}
	return found, nil
}

// worktreeOrigin is the repository and branch a discovered worktree belongs to, read from
// the worktree itself. Asking the worktree is what makes the association reliable: the
// path says nothing dependable about which repository it came from.
type worktreeOrigin struct {
	repoRoot string
	branch   string
}

func readWorktreeOrigin(worktreePath string) worktreeOrigin {
	var origin worktreeOrigin

	out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return origin
	}
	// The common git dir is the main repository's .git directory.
	origin.repoRoot = filepath.Dir(strings.TrimSpace(string(out)))

	out, err = exec.Command("git", "-C", worktreePath, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "HEAD" {
			origin.branch = branch
		}
	}
	return origin
}

// removeEmptyDirs removes the directories left empty under root, deepest first. Root
// itself is kept.
func removeEmptyDirs(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		removeEmptyDirs(dir)
		if remaining, err := os.ReadDir(dir); err == nil && len(remaining) == 0 {
			if err := os.Remove(dir); err != nil {
				log.ErrorLog.Printf("failed to remove empty directory %s: %v", dir, err)
			}
		}
	}
}

// CleanupWorktrees removes every worktree under the worktrees root, whichever layout
// created it, along with the branch each one was on.
func CleanupWorktrees() error {
	worktreesDir, err := getWorktreeDirectory()
	if err != nil {
		return fmt.Errorf("failed to get worktree directory: %w", err)
	}

	worktrees, err := findWorktreeDirs(worktreesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read worktree directory: %w", err)
	}

	// Read where each worktree came from before removing it — afterwards there is
	// nothing left to ask.
	origins := make([]worktreeOrigin, 0, len(worktrees))
	for _, worktree := range worktrees {
		origin := readWorktreeOrigin(worktree)
		if err := os.RemoveAll(worktree); err != nil {
			log.ErrorLog.Printf("failed to remove worktree %s: %v", worktree, err)
			continue
		}
		if origin.repoRoot != "" {
			origins = append(origins, origin)
		}
	}

	// Prune before deleting branches: git refuses to delete a branch it still believes
	// is checked out in a worktree.
	pruned := make(map[string]bool)
	for _, origin := range origins {
		if pruned[origin.repoRoot] {
			continue
		}
		pruned[origin.repoRoot] = true
		if out, err := exec.Command("git", "-C", origin.repoRoot, "worktree", "prune").CombinedOutput(); err != nil {
			log.ErrorLog.Printf("failed to prune worktrees in %s: %s (%v)", origin.repoRoot, out, err)
		}
	}

	for _, origin := range origins {
		if origin.branch == "" {
			continue
		}
		if out, err := exec.Command("git", "-C", origin.repoRoot, "branch", "-D", origin.branch).CombinedOutput(); err != nil {
			log.ErrorLog.Printf("failed to delete branch %s in %s: %s (%v)", origin.branch, origin.repoRoot, out, err)
		}
	}

	removeEmptyDirs(worktreesDir)

	return nil
}
