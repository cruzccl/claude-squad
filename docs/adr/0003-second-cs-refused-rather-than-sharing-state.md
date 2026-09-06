# A second cs is refused rather than made to share the saved state

Design: [one-cs-owns-the-saved-state](../design/one-cs-owns-the-saved-state.md)

`~/.claude-squad/state.json` holds one list of sessions for the whole machine, and every
`cs` loads it once at startup and rewrites it whole from that snapshot on every save. Two
of them running at once therefore delete each other's sessions, silently and completely.
We made the second one refuse to start: a process takes an exclusive advisory lock on
`~/.claude-squad/state.lock` for as long as it runs, and anyone who cannot take it exits
with a message naming who has it.

Refusing to start is a heavy answer, and it takes away something people do today — a `cs`
open in each of two repositories. We chose it anyway, because the alternative on offer is
not "two of them coexist" but "two of them coexist until one of them saves".

## Considered options

- **Merge writes instead of serialising processes** — re-read `state.json` before each save
  and reconcile the two lists. Rejected. The list is not a set of independent rows: `J`/`K`
  persist the user's ordering, and a session missing from a snapshot is indistinguishable
  from a session the other process just killed, so any merge rule silently resurrects
  killed sessions or silently drops new ones. And the file is the smaller half of the
  problem — the two processes also both drive tmux and both own worktrees, which no merge
  in `state.json` reconciles.
- **A lock per repository.** Rejected: it guards the wrong thing precisely. The state file
  is one file for the machine, so two `cs` in two repositories would each hold their own
  lock and destroy the same list. A per-repository lock only becomes correct after
  per-repository state, which is a different change and a migration.
- **A PID file, as the daemon already uses.** Rejected. A PID file records an intention
  where an OS lock records a fact: it survives the process that wrote it, so every reader
  has to guess whether the PID is alive, whether it is still `cs`, and whether the number
  has been reused. Guessing wrong wedges the program behind a lock nobody holds, and the
  usual cure — a `--force` flag, or deleting the file — hands the user a way to cause the
  exact corruption the lock exists to prevent. The kernel drops a `flock` when the process
  dies, so there is no stale state and nothing to second-guess.
- **Lock `state.json` itself** rather than a file that exists only to be locked. Rejected:
  saves rewrite it with `os.WriteFile`, and Windows byte-range locks are mandatory, so the
  lock would block access to the file it is meant to protect.
- **Let the second process run read-only.** Rejected as a worse product than a refusal.
  Almost everything `cs` does — create a session, kill one, pause one, reorder the list —
  is a write, so the read-only mode is a viewer that cannot be told apart at a glance from
  the real thing, and the user finds out which one they have only when a keystroke fails.
- **Warn and continue.** Rejected: the loss it warns about is silent, total, and arrives
  minutes later, by which time the warning has scrolled away.
- **Wait for the lock instead of failing.** Rejected: a terminal that hangs with no
  explanation is a worse answer than a sentence saying which process to go back to.

## Consequences

- Running `cs` in two repositories at once stops working, machine-wide. This is the price
  of the decision and not a side effect of it: the feature was never real, only unenforced.
  Restoring it means giving each repository its own state, at which point the lock becomes
  per-repository too.
- The guarantee is best-effort by construction. Any failure to lock that is not "someone
  else holds it" — no config directory, a filesystem without locking — is logged and `cs`
  starts unlocked. It protects against the case that actually happens, two terminals, and
  not against an exotic filesystem. Refusing to start there would be a worse failure than
  the one being prevented.
- The daemon is not a participant. It loads and saves the state without the lock, and gets
  away with it because it lives only while no TUI does and is killed with `SIGKILL` before
  its shutdown save can run. `cs reset` is the exception, doing its work with a live daemon
  polling underneath. Recorded as a known gap, not as a claim.
- The refusal can name the holder because a holder writes its PID, repository and start
  time into the locked file. Everything about that record is best-effort: a holder may not
  have written it yet, and a refused process then says only that `cs` is already running.
  It is a courtesy on top of the lock, never the mechanism.
- `main` now writes errors to stderr and exits non-zero, where it wrote to stdout and
  exited 0. The refusal is what forced the question, but the change applies to every error
  the program returns, and Cobra's own reporting had to be silenced so the message is
  printed once and without usage text.
- The lock lives on the open file description, so it would be inherited by a child process.
  `cs` starts the daemon as a child, which makes `O_CLOEXEC` load-bearing rather than
  hygienic — without it a released lock would outlive the process that took it.
