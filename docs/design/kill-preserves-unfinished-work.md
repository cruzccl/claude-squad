# Killing a session never destroys unfinished work

Status: implemented

## Problem Statement

`D` kills a session. For a session whose branch it created, it also runs `git branch -D`
on that branch, and it does so unconditionally: the only thing consulted is
`isExistingBranch`, a flag fixed when the session was created. Whether the branch has
commits, whether those commits were ever pushed, whether the worktree holds uncommitted
changes — none of it is asked.

The confirmation says `[!] Kill session 'X'?` and stops there. It never mentions the
branch, so the user cannot tell the two outcomes apart before choosing, and the two
outcomes are as far apart as "a session disappeared from a list" and "a day of work is
gone".

The destruction is thorough. `git worktree remove -f` discards anything uncommitted, and
`git branch -D` discards commits without asking whether they are merged or pushed
anywhere. What is left lives in the reflog until the next `gc`, which is archaeology, not
a recovery path.

The flag itself is misread easily. `isExistingBranch` does not mean "a branch that
exists"; it means "a branch that already existed at the moment this session was created".
It is a photograph of an instant, and it is never updated — push the branch to `origin`
and it still reads `false`, so `D` still deletes it locally.

There is a second, blunter path: `CleanupWorktrees()`, behind the `reset` command, deletes
every branch it discovers with `-D` and does not consult `isExistingBranch` at all.

## Solution

`D` stops being able to destroy work.

A session is killed only when nothing would be lost. Before anything is touched, the
session is asked what killing it would do, and there are four answers:

- **The worktree has uncommitted changes.** The kill is refused. No confirmation is shown
  — there is nothing to confirm — and the error says what is in the way and where to deal
  with it: attach to the session (`o`) and commit or discard the changes there.
  This applies to every session, whether its branch is new or pre-existing: uncommitted
  work is uncommitted work, and today it is `worktree remove -f` that destroys it, which
  does not care who created the branch.
- **The branch is checked out somewhere else.** The kill is refused, as it is today, but
  the check widens: any worktree holding the branch blocks it, not only the main working
  tree, and the error says where.
- **The branch is new and untouched** — it still points at the commit it was created from.
  The session is killed and the branch is deleted, because deleting it loses nothing.
- **Anything else.** The session is killed and the branch is kept. The worktree goes, the
  persisted state goes, the row leaves the list, and the branch stays in the repository
  with its commits.

The confirmation says which of the last two is about to happen, naming the branch.

`CONTEXT.md` defines **untouched branch**. See ADR-0002 for the decision to refuse rather
than to commit on the user's behalf, and for the alternatives rejected.

## User Stories

1. As a user killing a session, I want to be told what will happen to its branch before I confirm, so that I am not choosing blind between two very different outcomes.
2. As a user killing a session whose branch has commits, I want the branch left in the repository, so that ending a session never means losing the work in it.
3. As a user killing a session whose branch was created and never used, I want the branch deleted, so that abandoned experiments do not accumulate in `git branch`.
4. As a user killing a session with uncommitted changes, I want to be stopped, so that a keystroke cannot discard work I have not saved.
5. As a user who has been stopped, I want to be told what is in the way and where to deal with it, so that the refusal is a detour and not a wall.
6. As a user who has been stopped, I want to decide myself what happens to my uncommitted changes, so that no key commits them on my behalf while I am asking to throw the session away.
7. As a user killing a session on a branch someone else's worktree has checked out, I want to be told which worktree holds it, so that I can go and deal with it.
8. As a user killing a session on a pre-existing branch with uncommitted changes, I want the same protection as on a new branch, so that the danger does not depend on who created the branch.
9. As a user killing a paused session, I want it to work, so that pausing is not a trap that makes a session unkillable.
10. As a user whose repository has moved or broken, I want to still be able to kill the session, so that a broken session is not a permanent row in my list.
11. As a user whose repository has moved or broken, I want the branch kept rather than deleted, so that uncertainty resolves towards keeping.
12. As a user whose session failed to start, I want its half-created branch cleaned up, so that a failure leaves nothing behind.
13. As a user whose session failed to resume, I want a branch that has commits left alone, so that an error path is not a second way to lose work.

## Implementation Decisions

### The rule has two halves, in two places

The rule splits into a **precondition**, which decides whether the kill happens at all,
and a **policy**, which decides what a kill destroys once it is under way. They cannot
live together, because `Cleanup()` is partially destructive by construction: it removes
the worktree before it decides about the branch, so by the time it could refuse it has
already destroyed something. The current handler makes this worse — it deletes the
persisted state *before* calling `Cleanup()`, and `ui/list.go` only logs the error that
comes back — so a refusal raised inside `Cleanup()` would remove the session from the list
and from disk while leaving the worktree and branch orphaned.

- The **precondition** runs in the `D` handler, before anything is destroyed, alongside
  the checkout guard already there. It belongs to `D` alone.
- The **policy** lives inside `GitWorktree.Cleanup()`, and the other three callers inherit
  it: the `Start()` rollback (`session/instance.go:280`) and the two `Resume()` error paths
  (`session/instance.go:562,574`). In the rollback the branch is newly created and
  untouched, so the behaviour does not change. In the `Resume()` error paths the branch can
  hold commits and is deleted today; inheriting the policy is a fix, not a regression.

The precondition is deliberately *not* inherited. A rollback has nobody to refuse: the
session never came to exist, so a preserved worktree is not rescued work, it is unowned
rubbish on disk with no row in the list to manage it.

### One query answers "what would `D` do?"

`Instance` gains a single query returning a `KillOutcome`, a value with a `Kind`
enumerating the four answers above and the details each one needs (the worktree holding
the branch, the branch name). It is modelled on `git.BranchOutcome`
(`session/git/util.go:139-155`), which is also a prediction consulted before acting, and
it reads the same way at the call site — a `switch` on `Kind`, parallel to the one in
`app/creation.go:296`.

One query rather than two, because the handler needs both answers — whether it may
proceed, and what the confirmation should say — and two calls would let the modal and the
refusal disagree. An enumerated `Kind` rather than a set of booleans, because it forces
the states to be exhaustive and mutually exclusive; `Blocked && BranchWillBeDeleted` should
not be expressible.

`Instance` decides; `GitWorktree` supplies the git facts underneath and asserts nothing
about what should happen.

### The facts the query rests on

- **Untouched** is `git rev-list --count <base commit>..<branch>` returning zero. The base
  is `baseCommitSHA`, recorded when the session was created and already the reference the
  diffs use. It is run against the repository, not the worktree, because a paused session
  has no worktree. Comparing against the repository's *current* `HEAD` was rejected: if
  `main` moved on, a branch with no commits of its own reads as behind, and the answer
  would depend on what other people pushed rather than on what the user did.
- **Dirty** is the existing `IsDirty()` — `git status --porcelain`, with no `-uno`, so
  untracked files count. This keeps one definition of "dirty" in the package, the same one
  Pause and push already use. The cost is the occasional branch kept alive by a stray build
  artefact, which is the error worth making.
- **Checked out elsewhere** uses `BranchCheckedOutAt` (`session/git/util.go:120`), which
  reads `git worktree list --porcelain` and names the worktree holding the branch. It
  replaces `IsBranchCheckedOut`, which only ever looked at the main working tree's current
  branch. The session's own worktree holds its own branch, so that path must be excluded or
  every kill would be refused.

### Edge cases resolve towards keeping

- **No worktree on disk** (a paused session) is not dirty. This is true rather than
  convenient: Pause commits dirty work before removing the worktree, so a paused session
  either has its changes in commits — and the policy keeps the branch — or was clean.
- **A git command fails** (repository moved, permissions): the kill proceeds and the branch
  is kept. Refusing was rejected because `D` is also the escape hatch for a broken session,
  and a refusal would leave a zombie row that cannot be removed by any means. Keeping the
  branch protects the work without trapping the user.
- **`baseCommitSHA` is empty** — possible in state persisted by older versions — means the
  branch cannot be shown to be untouched, so it is kept.

### What the user sees

The outcome is computed before the confirmation is shown, so the confirmation can name it:
`Kill session 'X'? Branch <branch> will be deleted` against `... will be kept`. A refused
kill shows no confirmation at all, only the error — asking "are you sure?" about something
that is going to be rejected anyway is cruel.

The refusal text is derived from the outcome rather than being a fixed template, so the
reason it gives is the one that actually applies, and it names the worktree holding the
branch when that is the obstacle.

The escape it offers is attaching (`o`) and dealing with the changes there. It deliberately
does not offer `c`, which would be shorter; ADR-0002 records why.

## Testing Decisions

Two seams, matching the repository's precedent.

**Seam 1 — the git facts, in `session/git`.** Against real temporary repositories, as the
existing worktree tests do. Cover: a branch with no commits reads as untouched; one commit
makes it not untouched; a base that has been overtaken by `main` does not make an untouched
branch read as touched; an untracked file counts as dirty; a missing worktree is not dirty;
`Cleanup()` deletes a new untouched branch, keeps a new branch with commits, and keeps a
pre-existing branch either way; a branch held by another worktree is reported with its path,
and the session's own worktree is not.

**Seam 2 — `home.handleKeyPress`, in `app`.** Driven by `tea.KeyMsg` values, asserting what
the user observes: the confirmation text names the branch and says deleted or kept; a dirty
worktree produces an error and no confirmation; that error points at attaching and does not
name `c`; a branch held elsewhere
produces an error naming the holder; confirming a kill that is allowed removes the row.

Note a change in character. Seam 2 used to be hermetic because `handleKeyPress` *returned*
the command that would do the work and a test simply did not run it. The outcome is now
computed before the confirmation, inside `handleKeyPress`, so these tests touch git for real
and need temporary repositories under a temporary `HOME`. They must stay hermetic in the
sense that matters here: no tmux server, no writes outside the temporary directories.

**Run tests per package** — `go test ./app`, `go test ./session/git` — and never
`go test ./...`. Packages in this repository start processes and PTYs during tests, and
running the whole tree has brought the developer's machine down.

Not a separate seam: the composition inside `Instance`. It is thin logic over facts already
covered in seam 1, and testing it directly rebuilds a real repository to verify an `if`.

## Out of Scope

- **The handler's ordering.** `killAction` deletes the persisted state before calling
  `Cleanup()`, and `ui/list.go:285-287` swallows the error, so a failed cleanup still
  removes the session from the list and from disk. It is a real defect, but the precondition
  removes the two likely causes of `Cleanup()` failing — a dirty worktree and a branch held
  elsewhere — so the fix loses most of its value exactly as this change lands. Reordering is
  also not the innocent move it looks: `Cleanup()` is partially destructive internally, so
  "delete the state only if cleanup succeeded" first has to define success when half of it
  already happened. That is its own change.
- **`cs reset`.** `CleanupWorktrees()` keeps deleting every branch it finds with `-D`,
  ignoring `isExistingBranch` and the new policy alike. It is an explicit destructive
  command, not a key pressed by accident in a list.
- **Renaming `isExistingBranch`** to something that says what it means — that the branch
  already existed when the session was created, not that it exists now.
- **Recovering deleted branches.** Nothing here reads or writes the reflog.
- **Remote branches.** No path in this repository deletes a remote branch, and none is
  added.

## Further Notes

- The outcome is computed twice: once for the confirmation, and once inside `Cleanup()`,
  which remains the owner of the policy. They can disagree, because the agent is still
  running in tmux while the confirmation is open and can commit in that gap. The divergence
  only runs one way — the branch stops being untouched, and `Cleanup()` keeps a branch the
  confirmation said it would delete. It fails towards keeping; the opposite is impossible.
- Pushing a branch does not change what `D` does. A pushed branch has commits, so the policy
  keeps it; the work being safe on the remote is not what saves it.
