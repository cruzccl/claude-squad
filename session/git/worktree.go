package git

import (
	"claude-squad/config"
	"claude-squad/log"
	"path/filepath"
	"regexp"
	"strings"
)

func getWorktreeDirectory() (string, error) {
	configDir, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(configDir, "worktrees"), nil
}

// GitWorktree manages git worktree operations for a session
type GitWorktree struct {
	// Path to the repository
	repoPath string
	// Path to the worktree
	worktreePath string
	// Name of the session
	sessionName string
	// Branch name for the worktree
	branchName string
	// Base commit hash for the worktree
	baseCommitSHA string
	// isExistingBranch is true if the branch existed before the session was created.
	// When true, the branch will not be deleted on cleanup.
	isExistingBranch bool
}

func NewGitWorktreeFromStorage(repoPath string, worktreePath string, sessionName string, branchName string, baseCommitSHA string, isExistingBranch bool) *GitWorktree {
	return &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     worktreePath,
		sessionName:      sessionName,
		branchName:       branchName,
		baseCommitSHA:    baseCommitSHA,
		isExistingBranch: isExistingBranch,
	}
}

// unsafePathSegment matches everything that has no business in a directory name.
var unsafePathSegment = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// repositoryDirectoryName reduces a repository root to the single directory segment its
// worktrees are grouped under. Two clones whose directories share a name share a
// directory, deliberately: that grouping is how a human browsing the worktrees root
// thinks about a project.
func repositoryDirectoryName(repoRoot string) string {
	name := unsafePathSegment.ReplaceAllString(filepath.Base(repoRoot), "-")
	name = strings.Trim(name, ".-")
	if name == "" {
		return "repository"
	}
	return name
}

// resolveWorktreePaths resolves the repo root and the worktree path for the given branch
// name: <worktrees root>/<repository directory name>/<branch name>. The branch name is
// used as-is, so its slashes nest as real directories and the path can be read back as a
// branch name. No uniqueness suffix is needed — git refuses to check out one branch in
// two worktrees, so the path is unique by construction.
func resolveWorktreePaths(repoPath string, branchName string) (resolvedRepo string, worktreePath string, err error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		log.ErrorLog.Printf("git worktree path abs error, falling back to repoPath %s: %s", repoPath, err)
		absPath = repoPath
	}

	resolvedRepo, err = RepoRoot(absPath)
	if err != nil {
		return "", "", err
	}

	worktreeDir, err := getWorktreeDirectory()
	if err != nil {
		return "", "", err
	}

	worktreePath = filepath.Join(worktreeDir, repositoryDirectoryName(resolvedRepo), filepath.FromSlash(branchName))

	return resolvedRepo, worktreePath, nil
}

// NewGitWorktreeForBranch creates a GitWorktree for the given branch. When existing is
// true the branch is already in the repository: it is checked out rather than created,
// and it is left in the repository when the session is cleaned up.
func NewGitWorktreeForBranch(repoPath string, branchName string, sessionTitle string, existing bool) (*GitWorktree, error) {
	repoPath, worktreePath, err := resolveWorktreePaths(repoPath, branchName)
	if err != nil {
		return nil, err
	}

	return &GitWorktree{
		repoPath:         repoPath,
		sessionName:      sessionTitle,
		branchName:       branchName,
		worktreePath:     worktreePath,
		isExistingBranch: existing,
	}, nil
}

// IsExistingBranch returns whether this worktree uses a pre-existing branch
func (g *GitWorktree) IsExistingBranch() bool {
	return g.isExistingBranch
}

// GetWorktreePath returns the path to the worktree
func (g *GitWorktree) GetWorktreePath() string {
	return g.worktreePath
}

// GetBranchName returns the name of the branch associated with this worktree
func (g *GitWorktree) GetBranchName() string {
	return g.branchName
}

// GetRepoPath returns the path to the repository
func (g *GitWorktree) GetRepoPath() string {
	return g.repoPath
}

// GetRepoName returns the name of the repository (last part of the repoPath).
func (g *GitWorktree) GetRepoName() string {
	return filepath.Base(g.repoPath)
}

// GetBaseCommitSHA returns the base commit SHA for the worktree
func (g *GitWorktree) GetBaseCommitSHA() string {
	return g.baseCommitSHA
}
