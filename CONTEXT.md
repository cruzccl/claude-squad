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

**Pausing a session**:
Dismantling a session's workspace while keeping everything that identifies it. The point is
to release the branch: a branch a worktree holds cannot be checked out anywhere else, so
pausing is what lets the user take the branch into their own working copy. The TUI reaches
it with the key it labels "checkout", which names what the user can do next rather than what
the act itself does.
_Avoid_: checking out a session, stopping a session

**Paused session**:
A session that has been paused. It has no worktree on disk, its work is committed to its
branch, its tmux session is detached but alive, and it still holds its row in the TUI list.
Resuming rebuilds the workspace and carries on from where it stopped.
_Avoid_: stopped session, closed session, dead session

**Branch name**:
The git branch a session commits to. It is the session's technical identity: unique
within a repository, subject to git's naming rules, and never checked out by two
worktrees at once. Suggested from the session title, but owned by the user.
_Avoid_: branch title

**Branch prefix**:
A per-user string (`branch_prefix` in the config) prepended to a suggested branch name,
so one user's branches are recognisable in a shared repository.

**Untouched branch**:
A session's branch that still points at the commit the session was created from — nothing
has been committed to it. Says nothing about the worktree, which may hold uncommitted
changes on an untouched branch.
_Avoid_: empty branch, unused branch

**Worktree directory**:
The directory on disk holding a session's git worktree, under
`~/.claude-squad/worktrees/`. Derived from the branch name, not from the session title.

**Tmux session name**:
The identifier tmux knows a session by, derived from the session title with tmux's own
naming rules and prefixed with `claudesquad_`. Distinct from the branch name: the two
are sanitised differently and are not interchangeable.

**State lock**:
The exclusive right to write the saved state — the one `state.json` that holds every
session on the machine, whatever repository it belongs to. One `cs` holds it for as long
as it runs, which is why a second one refuses to start. It is an advisory OS lock on
`~/.claude-squad/state.lock`, so it exists only while its holder does.
_Avoid_: lock file, session lock
