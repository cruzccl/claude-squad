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

**Active repository**:
The repository new sessions are created in: the one whose branches the branch step lists
and whose HEAD a new branch starts from. It starts as the repository the directory `cs`
was launched in belongs to — launching anywhere inside a repository means that repository —
and changes when the first step of session creation picks another; it lives only as long
as the process, so a launch always says which repository it means.
_Avoid_: current repository, working repository

**Known repository**:
A repository the first step of session creation offers by name rather than making the user
type its path: one that has sessions, or one that was picked before. What is remembered is
what has been offered, never which one is active.
_Avoid_: recent repository, saved repository

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

**Agent session**:
The tmux session running a session's agent. It is what a session is attached to by
default, what the preview pane shows, and what pausing detaches from and resuming
rebuilds. Distinct from the session itself, which also owns a shell.
_Avoid_: the session's tmux, main session

**Session shell**:
The interactive shell tmux session a session owns, running in its worktree directory.
Made the first time anything asks for it and dismantled when the session is paused or
killed, so it is disposable in a way the agent session is not: a shell that was closed or
never opened is simply made again.
_Avoid_: terminal session, terminal tab

**Attached**:
The state in which one of a session's tmux sessions — its agent or its shell — owns the
terminal, and the TUI is not drawing. Nothing of claude-squad is on screen while attached,
which is why the keys that end it are named on the screen shown just before it starts.
_Avoid_: opened, in the session

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
