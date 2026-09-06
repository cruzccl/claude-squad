# Choosing the repository a session is created in

Status: implemented

## Problem Statement

`cs` decides which repository it works in once, silently, at startup: `os.Getwd()` becomes
`home.repoPath` and never changes. Every session created in that run branches from there,
the branch step lists that repository's branches, and nothing in the interface offers
another.

Working in a second repository therefore means quitting and relaunching from it — and
since one `cs` per machine took the state lock (ADR 0003), it also means giving up the
sessions of the first one for as long as the second runs. The lock was the right answer to
a real corruption, but it removed something people did every day: a `cs` open in each of
two repositories.

The saved state has been machine-wide all along: `state.json` holds every session whatever
repository it belongs to, and the list already labels each row with its repository when
more than one is in play. The only thing that is single-repository is the moment of
creation.

Two smaller failures come from the same place. A `cs` launched outside a git repository
gets as far as the branch step before failing, when the worktree cannot be created. And
the check that says "that branch is already used by session X" compares branch names
without regard to repository, which is correct only for as long as every session lives in
one repository.

## Solution

Creation opens on a new first step: **the repository**. The field arrives pre-filled with
the active repository, so accepting it costs one keystroke, and under it are the
repositories `cs` already knows about — the ones its sessions live in, and the ones chosen
before — filtered by whatever is typed. A path can be typed instead, completed with tab.
The line under the field says what choosing it would do, in the same shape the branch step
uses: `Active repository`, `New sessions in <root>`, or a red reason it cannot be used.

What the step submits becomes the active repository for the rest of the run: the session
being created is built there, and the next `n` opens on it. It is not saved. Every `cs`
starts with the directory it was launched in, whatever was chosen last time (ADR 0004).

## User Stories

1. As a user creating a session, I want the repository asked for first, so that the rest of the flow is about a repository I chose rather than one I happened to launch from.
2. As a user creating a session in the repository I am already in, I want it pre-filled, so that the common case still costs one keystroke.
3. As a user creating a session elsewhere, I want the repositories I already use listed, so that changing repository does not mean typing a path from memory.
4. As a user with many repositories, I want that list to filter as I type, so that I can find one by name.
5. As a user typing a path, I want tab to complete it, so that a repository that has never been used is still reachable without typing every character.
6. As a user typing a path, I want `~` and relative paths to work, so that I can write what I would write in a shell.
7. As a user who named a directory inside a repository, I want the repository itself chosen, so that where I happened to point does not change what I get.
8. As a user who named something that is not a repository, I want to be told before anything is created, so that the failure arrives at the step that caused it.
9. As a user who launched `cs` outside a repository, I want creation to stop at the first step with a reason, so that I do not discover it three steps later.
10. As a user who has changed repository, I want the next session to be offered the same one, so that a run of sessions in one repository costs one decision.
11. As a user who restarts `cs`, I want the directory I launched from to decide again, so that where I am standing is never overruled by something I chose days ago.
12. As a user who has chosen a repository once, I want it offered next time, so that the list learns what I work on.
13. As a user whose repository has been deleted or moved, I want it to disappear from the list, so that what is offered is somewhere a session can actually be created.
14. As a user creating a session in a second repository, I want a branch name used in the first one to be free, so that `main` in one project does not block `main` in another.
15. As a user whose title is already used by a session in another repository, I want the message to name that repository, so that I know where to look.
16. As a user who abandons the repository step, I want nothing created and nothing selected in the list disturbed, so that an exploratory `n` costs nothing.
17. As a user with a long path, I want to see the end of it, so that the repository's name is what survives the truncation.
18. As a user typing a path into a directory I have never worked in, I want tab to show me what is there when it cannot complete any further, so that a repository nothing has heard of is still findable.
19. As a user typing `~` or a relative path, I want the repositories I already know to be matched against the place it means, so that navigating to where they live does not hide them.

## Implementation Decisions

### The repository step

- A new first state, `stateNewRepo`, entered by `n`. The step is always shown, never
  skipped: unlike the program step, whose profiles are a closed set, this step's answer
  includes "any other path", so it never asks a question with a single answer.
- `RepoInputOverlay` mirrors `BranchInputOverlay` — pre-filled field, filtered list under
  it, outcome line, the same styles — with one deliberate divergence: it holds the whole
  list of known repositories and filters it itself, because the list is already in memory
  and nothing has to be looked up to filter it.
- The pre-filled path is not a filter. Nothing has been typed when the step opens, so the
  list opens showing every known repository; the first edit ends that and from then on the
  list is exactly what the field says. `ctrl+u` clears the field and `ctrl+w` drops the
  last path segment, so reaching the list, or walking back up a wrong turn, costs one key
  rather than one per character of a path that can be thirty long. A filter of nothing
  matches everything, which makes clearing the field the way back to the list the step
  opened with.
- The list is matched against both the text as it was written and the path that text
  names. The field is two things at once: `zeta` is a name to search for and means nothing
  as a path, while `~/git/` is a place and means nothing as a name — the repositories are
  stored as absolute paths, so a tilde matched literally finds none of them. Asking both
  questions is what saves having to decide which one was being asked. The step is handed
  the expansion as a function: where a home directory or an active repository is, is not
  something an overlay knows.
- A trailing separator is dropped before the list is matched. Completing a path leaves one
  behind to invite the next segment, and the repository just named would otherwise stop
  matching at the moment the field held its own path exactly.
- Resolution is synchronous on every key press rather than debounced. The branch step
  debounces because it shells out to git per keystroke; here a half-typed path fails on
  `os.Stat`, and git is asked only about a directory that exists.
- Tab completes to the longest prefix the candidates share, with a trailing separator when
  there is exactly one. It never cycles. When it cannot extend the field at all — several
  directories match and they share no more than what is already typed — the candidates
  take the place of the known repositories in the list under the field, under a heading
  naming the directory they were read from, and are picked with `↑`/`↓` like any other
  row. This is the one moment the step has nothing else to offer: a repository the user
  has never opened is in no remembered list, so the directory's own contents are the only
  answer available. The next edit puts the known repositories back, because they answer a
  different question. Only directories are candidates, and hidden ones only when the typed
  text starts with a dot. A candidate is judged by following its path rather than by what the directory
  listing says about the name, so a repository reached through a symlink completes: what
  tab offers and what the resolve accepts have to be the same set.
- Escape and ctrl+c leave the step through `abandonRepoStep`, not `abandonNewSession`:
  there is no half-created session to discard yet, and `list.Kill()` would kill whichever
  session happened to be selected.

### What a path resolves to

- `~` is the home directory; a relative path is read from the **active repository**, not
  from the directory `cs` was launched in, which stops being visible as soon as the
  repository changes once.
- Every path is resolved to its repository root, so a directory inside a repository names
  the repository. The startup path is resolved the same way, so launching anywhere inside
  a repository means the same repository.
- The five outcomes: `Active repository`, `New sessions in <root>`, and the blocking
  `Path does not exist`, `Not a directory`, `Not a git repository`. An empty field blocks
  with no line and reports `repository path cannot be empty` on submit.
- The same resolve that labels the field decides what submitting does, so the step cannot
  promise one thing and do another.

### The session is built once the repository is settled

- `n` no longer constructs the `Instance`. It is constructed in `enterTitleStep`, with the
  chosen root as its `Path`, so the session's repository is right from the moment it
  exists and is never corrected afterwards.
- `git.FetchBranches` moves with it: the first moment there is a repository to fetch from
  is the moment one is chosen.
- The menu stays in its normal state during the repository step and becomes
  `StateNewInstance` on entering the title step, where its "submit name" label is true.

### What is remembered, and what is not

- `state.json` gains `known_repos`: the repositories that have been chosen, most recent
  first, at most ten, purged of paths that are no longer directories the next time it is
  written. Only the path is stored — the order of the array is the recency, so there is no
  second place for it to disagree with itself.
- A repository is recorded when the step is left, not when a session finally starts: a
  repository worth opening the step on is worth offering again, whether or not the
  creation it began was seen through.
- The active repository is **not** stored (ADR 0004).
- The list the step offers is the active repository, then `known_repos`, then the
  repositories this application's sessions live in, deduplicated and with anything no
  longer on disk left out.

### A branch belongs to a repository

- `branchesTakenBySessions` is keyed by `(repository, branch)` rather than by branch name.
  A session's repository is the root resolved when it started, falling back to its `Path`
  — which is a root for sessions created through this step, and may be a directory inside
  one for sessions created before it existed. That fallback fails open: an old session may
  not reserve its branch, and git still refuses a double checkout.
- Session titles stay unique across every repository, because stored state is located by
  title. The message now names the repository of the session that holds the title.

### Long paths truncate from the left

- `stepTruncateLeft` keeps the end of a path rather than its start, so a long path gives
  up the directories it lives in rather than the name of the repository it points at. Used
  for the field, the outcome line and the list rows.

## Testing Decisions

- The creation-flow tests drive real key presses through the new first step, so the step is
  covered by the flow every test already walks rather than in isolation.
- Every test home now starts in a real git repository (`initTestRepo`), because the step
  refuses anything else. `initTestRepoNamed` gives repositories distinguishable names for
  the tests that filter or report on them.
- `fakeAppState` keeps the remembered repositories in memory, so a test never touches the
  state file of the machine it runs on.
- What a path resolves to, and what tab completes to, are table-driven against real
  directories on disk rather than mocked.

## Out of Scope

- Any permanent display of the active repository outside the creation flow. Its only
  effect is at creation, and the list already labels each session with its repository.
- Searching the disk for repositories to offer. Tab reads the one directory the typed
  path points into, which is what completing a path means; nothing walks the filesystem
  looking for repositories to put in front of the user.
- Making session titles unique per repository. That is a change to how state is located —
  storage, tmux session names, and reload matching — not a change to a validation rule.
- Per-repository state, and with it a per-repository state lock (ADR 0003).

## Further Notes

The three-way split — the active repository in memory, the known repositories on disk, the
sessions' own repositories in `state.json` — is deliberate. Remembering what to offer costs
the user nothing when it is wrong; remembering what is selected costs a session created in
the wrong repository.
