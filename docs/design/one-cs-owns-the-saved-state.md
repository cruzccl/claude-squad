# One cs owns the saved state

Status: implemented
ADRs: [ADR-0003](../adr/0003-second-cs-refused-rather-than-sharing-state.md)

## Problem Statement

`~/.claude-squad/state.json` is a single file for the whole machine. It holds the
help-screen bitmask and the full list of sessions, across every repository the user has
ever run `cs` in — sessions are not partitioned by repo, they are one list.

Every `cs` reads that file once, at startup, into an in-memory `State`, and every save
rewrites the whole file from that snapshot: `Storage.SaveInstances` serialises the entire
in-memory list, `State.SaveInstances` replaces `InstancesData` wholesale, and `SaveState`
does an `os.WriteFile` over the file. Nothing re-reads, nothing merges, nothing notices
that the file on disk has changed since it was loaded.

So two `cs` running at once is last-writer-wins over everything. The second one starts
from a snapshot taken before the first created anything; the moment it saves — starting a
session, killing one, reordering the list with `J`/`K`, or simply quitting, which saves
unconditionally — the first one's sessions disappear from `state.json`. Their tmux
sessions and their worktrees are still on disk, still running agents, but nothing in the
TUI lists them any more: they cannot be attached to, paused or killed through the program
that created them. The damage is silent and it is total, and it lands on the user who did
the least — the one who quit first loses to whoever quits later.

Writes to the state file are not the only collision. Each `cs` kills the daemon at
startup and relaunches it on exit, so with two of them running, autoyes ends up governed
by whichever process exited last, over whichever list that process happened to hold.

`cs reset` is the sharper edge of the same problem. It deletes every stored instance,
kills every tmux session the program owns and removes every worktree it finds — while a
running `cs` has those exact worktrees checked out and those exact tmux sessions attached.

Nothing warned about any of this. Two terminals in two repositories is the obvious way to
reach for a tool whose entire premise is running several agents at once.

## Solution

One `cs` owns the saved state at a time, machine-wide.

A process takes an exclusive lock before it touches anything the state describes, holds it
for as long as it runs, and releases it on exit. A second one is refused, and says so:

```
cs is already running: PID 48213, started 09:41 in /home/carlos/git/other-repo
```

The lock is an advisory OS lock — `flock` on unix, `LockFileEx` on Windows — on
`~/.claude-squad/state.lock`. The kernel drops it when the process dies, so a crash leaves
nothing stale behind and no later process has to decide whether an old lock is real.

Two commands take it: the TUI, before it kills the daemon, and `cs reset`, before it loads
anything. `cs version` and `cs debug` do not, because they change nothing. The daemon does
not either; see Out of Scope.

If the lock cannot be taken for any reason other than another process holding it — no
config directory, a home directory on a filesystem that does not implement locking — the
failure is logged and `cs` starts anyway, holding nothing. See ADR-0003 for the decision to
refuse rather than to share, and for the alternatives rejected.

## User Stories

1. As a user who already has `cs` open, I want a second one to refuse to start, so that starting it cannot delete the sessions the first one is running.
2. As a user who has been refused, I want to be told which process holds the lock and where it is running, so that I know which window to go back to.
3. As a user whose first `cs` crashed, I want the next one to start normally, so that a crash does not leave a lock I have to find and delete by hand.
4. As a user running `cs reset`, I want it refused while a `cs` is running, so that a command that deletes every worktree cannot run under a program that is using them.
5. As a user, I want the refusal on stderr and a non-zero exit status, so that a script that wraps `cs` can tell that it did not run.
6. As a user whose home directory does not support file locking, I want `cs` to start anyway, so that a safety net that cannot be hung does not become a wall.

## Implementation Decisions

### One lock for the machine, not one per repository

The lock guards `state.json`, and `state.json` is one file for the machine. A per-repo lock
would let two `cs` in two repositories run at once and then destroy each other's lists
exactly as before — it would guard the wrong thing precisely. The cost is real and is the
point: two repositories at once is no longer possible. Making it possible again is a change
to how state is stored, not to how it is locked.

### The lock is a file lock, not a PID file

The daemon already uses a PID file, and reusing that idiom was rejected. A PID file records
an intention; an OS lock records a fact. A crashed `cs` leaves a PID file naming a process
that no longer exists, and reasoning about it afterwards — is that PID alive, is it still
`cs`, has the number been reused — is guesswork with a wrong answer that either wedges the
program or defeats the lock. ADR-0003 records the full comparison.

### A separate lock file, not a lock on `state.json`

`state.json` is rewritten with `os.WriteFile` by anything that saves, and on Windows a byte
range lock is mandatory rather than advisory — it would block reads and writes of the very
file the program is trying to use. `state.lock` exists only to be locked, is never parsed
by anything that matters, and can be truncated freely.

### Where the lock is taken

In the TUI it comes after the git-repository check and before everything else, because the
first thing that follows is killing the daemon — already an act that assumes ownership of
the state. In `cs reset` it comes before `LoadState`, because everything the command does
afterwards is destructive.

`cs reset` still kills the daemon at the end rather than the beginning, unchanged from
before. The lock does not fix that ordering and does not pretend to.

### Naming the holder

After taking the lock, a process writes a small JSON record into the locked file: its PID,
the repository it is working in, and when it started. That record is only ever read by a
process that has just been refused the lock, which means the process that wrote it is by
definition still alive and the record still current. There is no staleness question to
answer.

The record is best-effort in both directions. A holder that fails to write it costs the
next process nothing but detail, so the failure is logged and ignored. A reader that finds
the file empty — it arrived between the holder taking the lock and writing its record — or
finds something it cannot parse reports an unidentified holder and prints
`cs is already running` with no detail, rather than an error about a lock file.

`Release` truncates the file before unlocking. Nothing reads a record left by a dead
process, but a file naming a PID that no longer exists is a puzzle for whoever opens it.

### Windows locks are mandatory, so the lock sits past the end of the file

`LockFileEx` locks a byte range and blocks reads of that range, and a refused process still
has to read the holder's record at offset 0. The lock is therefore taken on a single byte at
offset `1<<40`, far past anything the file will ever contain. Locking a range beyond the end
of a file is allowed and does not extend it.

### The daemon must not inherit the lock

`cs` starts the daemon as a child process. An advisory lock lives on the open file
description, so a descriptor inherited across `fork`/`exec` would keep the lock held after
`cs` itself exits — the next `cs` would be refused by a ghost. Go opens files `O_CLOEXEC` by
default, which closes the hole; the code says so at the `os.OpenFile` call, because a change
to that line is what would reopen it.

### Failing to lock is not failing to run

Every error except "another process holds it" is logged and treated as success, leaving the
caller holding a lock object that holds nothing and releases nothing. The lock is a safety
net over an arrangement that has worked for other people for a long time, and refusing to
start because a home directory is on a filesystem with no locking would be a worse failure
than the one being prevented.

### What the user sees

`main` prints the error to stderr and exits 1, where it used to print to stdout and exit 0.
Cobra's own error reporting is silenced (`SilenceErrors`, `SilenceUsage`): it would print the
message a second time and follow it with the usage text, which suits a mistyped command line
and not a program that is refusing to run.

The `StateLockHeldError` carries the holder record; turning it into the user-facing sentence
is `main`'s job, because only `main` knows the name the binary was invoked under.

## Testing Decisions

The tests live in `config`, against a temporary `HOME`, so that running them never touches
the state lock of a `cs` the developer has open.

An advisory lock belongs to the open file description rather than to the process, so a
second `AcquireStateLock` inside one test process is refused exactly as another `cs` would
be. That is what makes the interesting cases testable without spawning anything: the refusal
and the holder description, a lock that becomes available again after `Release`, `Release`
being safe twice and on a lock that holds nothing, and an unreadable record reported as an
unidentified holder.

`describeStart` is tested directly, for the only distinction it makes: a process started
today is named by its time, an older one also by its date.

**Run these tests filtered** — `go test ./config -run 'StateLock|DescribeStart'`. Running the
whole `config` package hangs, and not because of this: `TestGetClaudeCommand` spawns an
interactive shell. As everywhere in this repository, never `go test ./...`.

Two things the tests do not cover, checked by hand instead: the cross-process refusal, by
holding `state.lock` with `flock(1)` and running the binary against a temporary `HOME`; and
the Windows implementation, which is only compile-checked with `GOOS=windows go build ./...`.

## Out of Scope

- **The daemon.** `cs --daemon` loads and saves the state without taking the lock. It gets
  away with it by accident of scheduling: it is launched by a `cs` that is exiting and killed
  by the next one that starts, so it is alive only while no TUI is. It is also killed with
  `SIGKILL`, so the save in its shutdown path never runs. The exception is `cs reset`, which
  holds the lock and does its damage with a live daemon polling underneath, killing it only
  at the end. Making the daemon a participant means giving it a lock it can hand back, and
  giving `StopDaemon` a signal the daemon can act on — a separate change to the daemon's
  lifecycle.
- **Per-repository state.** The real reason two `cs` cannot coexist is that they share one
  list. Splitting `state.json` per repository would remove the constraint rather than enforce
  it, and it is a migration.
- **Merging concurrent writes.** Rejected in ADR-0003 rather than deferred.
- **`config.json`.** It is written by hand far more often than by the program, and losing an
  edit to it is not losing a day of work.
- **Waiting for the lock.** A refused `cs` fails immediately. Blocking until the other one
  quits would leave a terminal apparently hung with no way to tell why.

## Further Notes

- The daemon is launched inside a `defer`, which means it starts *before* the lock is
  released — deferred calls run last-in-first-out, and the daemon's `defer` is registered
  after the lock's. This is harmless and worth knowing: the daemon does not want the lock,
  and `LaunchDaemon` writes the PID file before returning, so the next `cs` can always find
  and kill it.
- `log.Close` prints `wrote logs to /tmp/claudesquad.log` to stdout as it runs, so a refused
  `cs` prints that line before the refusal reaches stderr. Cosmetic, pre-existing, and left
  alone.
- The description of a holder started on a previous day includes the date, which is the case
  that matters: a `cs` left running overnight in a repository the user has forgotten about is
  exactly the process they will be surprised to be refused by.
