package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// defaultBranchStem is used when a session title sanitizes down to nothing, so the
// suggested branch name is never just the branch prefix.
const defaultBranchStem = "session"

// sanitizeBranchName transforms an arbitrary string into a Git branch name friendly string.
// Note: Git branch names have several rules, so this function uses a simple approach
// by allowing only a safe subset of characters.
func sanitizeBranchName(s string) string {
	// Convert to lower-case
	s = strings.ToLower(s)

	// Replace spaces with a dash
	s = strings.ReplaceAll(s, " ", "-")

	// Remove any characters not allowed in our safe subset.
	// Here we allow: letters, digits, dash, underscore, slash, and dot.
	re := regexp.MustCompile(`[^a-z0-9\-_/.]+`)
	s = re.ReplaceAllString(s, "")

	// Replace multiple dashes with a single dash (optional cleanup)
	reDash := regexp.MustCompile(`-+`)
	s = reDash.ReplaceAllString(s, "-")

	// Trim leading and trailing dashes or slashes to avoid issues
	s = strings.Trim(s, "-/")

	return s
}

// SuggestBranchName returns the branch name suggested for a session title: the
// configured branch prefix followed by the sanitized title. It is only a suggestion —
// the user owns the branch name and can replace it entirely.
func SuggestBranchName(branchPrefix, sessionTitle string) string {
	stem := sanitizeBranchName(sessionTitle)
	if stem == "" {
		stem = defaultBranchStem
	}
	suggestion := sanitizeBranchName(branchPrefix + stem)
	if suggestion == "" {
		return defaultBranchStem
	}
	return suggestion
}

// ValidateBranchName reports whether name is a branch name git would accept. The rules
// are git's own, so a name that passes here is one git can create.
func ValidateBranchName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("branch name cannot be empty")
	}
	// git localizes its own diagnostics, so report the failure in our own words.
	out, err := exec.Command("git", "check-ref-format", "--branch", name).Output()
	if err != nil {
		return fmt.Errorf("%q is not a valid branch name", name)
	}
	// --branch also expands shorthands such as "@{-1}". A name git turns into a
	// different one is not the name the user asked for, so reject it rather than
	// create something they did not type.
	if strings.TrimSpace(string(out)) != name {
		return fmt.Errorf("%q is not a valid branch name", name)
	}
	return nil
}

// BranchExists reports whether a branch of this name is already in the repository,
// either locally or on the remote. A session started on one works on that branch and
// leaves it behind when it is closed.
func BranchExists(repoPath, branchName string) bool {
	for _, ref := range []string{"refs/heads/" + branchName, "refs/remotes/origin/" + branchName} {
		cmd := exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", ref)
		if cmd.Run() == nil {
			return true
		}
	}
	return false
}

// worktreeListing is one entry of `git worktree list --porcelain`.
type worktreeListing struct {
	path string
	// branch is empty when that worktree has a detached HEAD.
	branch string
}

// listWorktrees returns the worktrees git knows about for this repository, including the
// repository's own working tree.
func listWorktrees(repoPath string) []worktreeListing {
	out, err := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}

	var listings []worktreeListing
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			listings = append(listings, worktreeListing{path: strings.TrimPrefix(line, "worktree ")})
		case strings.HasPrefix(line, "branch ") && len(listings) > 0:
			ref := strings.TrimPrefix(line, "branch ")
			listings[len(listings)-1].branch = strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	return listings
}

// BranchCheckedOutAt returns the path of the worktree that currently has branchName
// checked out, and whether there is one. A branch checked out somewhere cannot be checked
// out again: a session started on it would take over the directory that worktree uses.
func BranchCheckedOutAt(repoPath, branchName string) (string, bool) {
	for _, listing := range listWorktrees(repoPath) {
		if listing.branch == branchName {
			return listing.path, true
		}
	}
	return "", false
}

// BranchOutcomeKind is what starting a session on a branch name would do.
type BranchOutcomeKind int

const (
	// BranchInvalid means git would not accept the name.
	BranchInvalid BranchOutcomeKind = iota
	// BranchNew means no branch of that name exists, so one would be created from HEAD.
	BranchNew
	// BranchExisting means the branch is already in the repository, so the session would
	// work on it and leave it behind when it is closed.
	BranchExisting
	// BranchCheckedOut means another worktree holds the branch, so no session can have it.
	BranchCheckedOut
)

// BranchOutcome is what starting a session on a branch name would do, resolved against
// the repository. The branch step shows it while the name is being typed, so what is
// about to happen is visible rather than decided silently on submit.
type BranchOutcome struct {
	Kind BranchOutcomeKind
	// CheckedOutAt is the worktree holding the branch, when Kind is BranchCheckedOut.
	CheckedOutAt string
	// LastCommit is git's relative date for the branch tip ("3 days ago"), when Kind is
	// BranchExisting. Empty if git could not say.
	LastCommit string
	// HeldBy names the session standing in the way, when Kind is BranchCheckedOut. Empty
	// when the blocker is outside this application, which is all git can see: ResolveBranch
	// never sets it, and only a caller that knows this application's sessions can.
	HeldBy string
}

// ResolveBranch reports what starting a session on branchName would do. A checked-out
// branch is reported as such rather than as an existing one: it is the more specific
// answer, and the only one that blocks.
func ResolveBranch(repoPath, branchName string) BranchOutcome {
	if err := ValidateBranchName(branchName); err != nil {
		return BranchOutcome{Kind: BranchInvalid}
	}
	if where, taken := BranchCheckedOutAt(repoPath, branchName); taken {
		return BranchOutcome{Kind: BranchCheckedOut, CheckedOutAt: where}
	}
	if !BranchExists(repoPath, branchName) {
		return BranchOutcome{Kind: BranchNew}
	}
	return BranchOutcome{Kind: BranchExisting, LastCommit: lastCommitRelative(repoPath, branchName)}
}

// lastCommitRelative returns git's own relative date for the branch tip. The branch may
// exist only on the remote, so fall back to the remote-tracking name.
func lastCommitRelative(repoPath, branchName string) string {
	for _, ref := range []string{branchName, "origin/" + branchName} {
		cmd := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%cr", ref)
		// git localizes relative dates; this one is read inside an English sentence.
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		out, err := cmd.Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

// samePath reports whether two paths point at the same place, resolving symlinks so a
// path git reports and one we built compare equal.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	resolvedA, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	resolvedB, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return resolvedA == resolvedB
}

// checkGHCLI checks if GitHub CLI is installed and configured
func checkGHCLI() error {
	// Check if gh is installed
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("GitHub CLI (gh) is not installed. Please install it first")
	}

	// Check if gh is authenticated
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("GitHub CLI is not configured. Please run 'gh auth login' first")
	}

	return nil
}

// IsGitRepo checks if the given path is within a git repository
func IsGitRepo(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	return cmd.Run() == nil
}

func findGitRepoRoot(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to find Git repository root from path: %s", path)
	}
	return strings.TrimSpace(string(out)), nil
}
