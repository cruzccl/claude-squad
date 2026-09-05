# Claude Squad

A TUI that runs several AI coding agents in parallel, each isolated in its own tmux
session and its own git worktree.

## Language

**Session**:
One running agent, with its own workspace and its own place in the TUI list.
_Avoid_: instance, task

**Session title**:
The human label for a session, typed by the user when creating it. Descriptive rather
than technical — it names the session in the TUI and seeds the suggested branch name —
but unique across sessions, because stored state is matched by it.
_Avoid_: session name, instance name

**Branch name**:
The git branch a session commits to. It is the session's technical identity: unique
within a repository, subject to git's naming rules, and never checked out by two
worktrees at once. Suggested from the session title, but owned by the user.
_Avoid_: branch title

**Branch prefix**:
A per-user string (`branch_prefix` in the config) prepended to a suggested branch name,
so one user's branches are recognisable in a shared repository.

**Worktree directory**:
The directory on disk holding a session's git worktree, under
`~/.claude-squad/worktrees/`. Derived from the branch name, not from the session title.

**Tmux session name**:
The identifier tmux knows a session by, derived from the session title with tmux's own
naming rules and prefixed with `claudesquad_`. Distinct from the branch name: the two
are sanitised differently and are not interchangeable.
