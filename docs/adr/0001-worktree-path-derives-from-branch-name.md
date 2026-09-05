# Worktree paths derive from the branch name, not the session title

A session used to take its worktree path from its branch name, which was itself derived
from the session title plus the configured branch prefix — so the title silently dictated
both the branch and a path, and the prefix's slash leaked into the filesystem as an
unintended directory level. We split the two: the title is a label, the branch is the
identity the user owns and edits, and a worktree lives at
`~/.claude-squad/worktrees/<repo-directory-name>/<branch-name>`, slashes and all.

## Considered options

- **Keep deriving the path from the title.** Rejected: the title is free text with no
  uniqueness or naming rules of its own, so the path had to be sanitised and suffixed to
  stay valid and unique, which is what made it unreadable.
- **Disambiguate repositories by hashing the absolute repo path** rather than using the
  directory name. Rejected: two clones of the same project sharing a directory is what a
  human browsing `worktrees/` wants to see, and sessions inside it stay distinct anyway.
- **Migrate existing worktrees into the new layout.** Rejected: it means moving live git
  worktrees (repairing each `gitdir` link) and rewriting persisted state, for a benefit
  that arrives on its own as old sessions get closed.

## Consequences

- The uniqueness suffix (`_<hex nanotime>`) is gone: git refuses to check out one branch
  in two worktrees, so `<repo>/<branch>` is unique by construction and a path can be read
  back as a branch name.
- That uniqueness argument arrives later than it looks. Both setup paths clear whatever
  occupies the target path before `git worktree add` runs — the step that makes a stale
  directory safe to reuse — so git never gets the chance to refuse. With the suffix gone
  that path can belong to a running session, so setup refuses to clear a worktree git
  still has registered and that is on disk, and the branch step rejects a branch that is
  already checked out anywhere in the repository.
- Older layouts — flat directories at the worktrees root, and directories nested under a
  branch prefix — keep working untouched, because sessions store absolute paths. Anything
  that walks the worktrees directory has to understand all three layouts.
- The session title is now validated as unique when a session is created, because stored
  state is still matched by title.
