# Unified session creation, and worktree paths derived from the branch name

Status: implemented
ADRs: [ADR-0001](../adr/0001-worktree-path-derives-from-branch-name.md)

## Problem Statement

Creating a session today happens through two different keys that are not two different
kinds of session — they are two different *sets of capabilities*. `n` asks for a session
title and starts immediately. `N` asks for a title and then opens an overlay with an
initial prompt, a branch picker and a profile picker. A user who wants to pick the branch
has to go through the prompt flow whether or not they want a prompt, and a user who
presses `n` never learns that picking a branch was possible at all.

Underneath that, the session title silently decides everything. The branch name is the
title, sanitised, with the configured branch prefix glued in front — never shown, never
editable. Titles are free text, so the sanitiser has to be destructive: "Mi Sesión Nueva"
becomes the branch `carlos/mi-sesin-nueva`, because accented characters are dropped rather
than transliterated. A user who wants a branch called something else has no way to say so.

The worktree directory is then derived from that branch name, and because the sanitiser
preserves slashes, the branch prefix leaks into the filesystem as an unintended directory
level. Every worktree also carries a nanosecond-timestamp suffix for uniqueness. The
result is a directory nobody can read: worktrees from every repository sit in one flat
drawer, grouped by an accident of the branch prefix, named things like
`loquesea_18d23e72149158db`, with no indication of which repository they belong to.

## Solution

One way to create a session, with the branch as a first-class thing the user owns.

Pressing `n` starts a flow with two visible steps. Step one asks for the **session title**
— a label for the TUI, nothing more. Step two shows the **branch name**, pre-filled with a
suggestion derived from the title (branch prefix included, so what is shown is the whole
truth), fully editable, with the repository's existing branches listed underneath and
filtered as the user types. Accepting the suggestion creates a new branch. Selecting an
existing branch — or typing the name of one — starts a session that works on that branch
and leaves it alone when the session is closed. A third step appears only for users who
have configured more than one profile, to pick the program.

The initial-prompt step disappears; the user types into the agent once it is running.
`N` is unbound.

Worktrees move to `<worktrees root>/<repository directory name>/<branch name>`, with the
branch's slashes nesting as real directories and no uniqueness suffix — git already
refuses to check out one branch in two worktrees, so the path is unique by construction
and can be read back as a branch name at a glance. Worktrees created under the older
layouts stay exactly where they are and keep working.

See ADR-0001 for the decision and the alternatives that were rejected. `CONTEXT.md`
defines the vocabulary used throughout this spec: session title, branch name, branch
prefix, worktree directory, tmux session name.

## User Stories

1. As a user creating a session, I want a single key that always leads to the same flow, so that I never have to remember which shortcut unlocks which capability.
2. As a user creating a session, I want to give the session a title in plain language, so that I can recognise it in the list without thinking about git's naming rules.
3. As a user creating a session, I want to see the branch name that is about to be created, so that nothing is decided on my behalf behind my back.
4. As a user creating a session, I want the branch name to arrive pre-filled from my title, so that the common case costs me one keystroke.
5. As a user creating a session, I want the suggested branch name to already include my configured branch prefix, so that what I see is exactly what will be created.
6. As a user creating a session, I want to clear the suggestion and type a completely different branch name, so that the branch reflects the work rather than the label I happened to type first.
7. As a user whose title contains accents, spaces or emoji, I want a suggestion that is a valid branch name, so that I can accept it without editing.
8. As a user whose title sanitises down to nothing, I want a usable suggestion anyway, so that the flow never dead-ends.
9. As a user creating a session, I want the repository's existing branches listed under the branch field, so that I can start work on one without leaving the flow.
10. As a user creating a session, I want that list to filter as I type, so that I can find a branch in a repository with hundreds of them.
11. As a user creating a session, I want selecting a branch from the list to mean "work on this branch", so that my existing work is continued rather than duplicated.
12. As a user closing a session that worked on a pre-existing branch, I want that branch left in the repository, so that I don't lose work by ending a session.
13. As a user creating a session, I want typing the name of a branch that already exists to behave exactly like selecting it from the list, so that the outcome depends on what I asked for and not on which widget I used.
14. As a user creating a session, I want to be told when the branch name I typed is not a valid git branch name, so that the failure arrives before the session is created rather than during setup.
15. As a user creating a session, I want an invalid branch name to be rejected rather than silently rewritten, so that I stay in control of the name.
16. As a user with a single profile configured, I want no program step at all, so that creating a session is two steps.
17. As a user with several profiles configured, I want to choose the program for this session, so that I can run different agents side by side.
18. As a user creating a session, I want to abandon the flow at any step and end up back where I started, so that an exploratory keypress costs nothing.
19. As a user reusing a title that already names a session, I want to be told immediately, so that I don't end up with two sessions the application cannot tell apart.
20. As a user browsing `~/.claude-squad/worktrees`, I want one directory per repository, so that I can tell at a glance what belongs to what.
21. As a user browsing a repository's worktree directory, I want each worktree named exactly after its branch, so that I can translate a path to a branch without decoding a timestamp.
22. As a user working in several clones of projects with the same directory name, I want them grouped together, so that the grouping matches how I think about the project.
23. As a user who wants to reclaim disk space, I want a repository's worktrees under one directory, so that I can inspect or remove them as a group.
24. As a user with sessions created under the old flat layout, I want them to keep working untouched, so that upgrading costs me nothing.
25. As a user with sessions created under the branch-prefix layout, I want them to keep working untouched, so that upgrading costs me nothing.
26. As a user running the cleanup command, I want worktrees from all three layouts found and removed, so that nothing is orphaned on disk.
27. As a user running the cleanup command, I want the per-repository directories left tidy rather than empty and lingering, so that the worktrees root does not accumulate rubbish.
28. As a user recreating a session for a branch whose directory was left behind, I want the stale directory dealt with, so that creation does not fail on a leftover.
29. As a developer of this repository, I want the creation flow covered by tests that drive real key presses, so that a refactor of the overlay cannot silently break the flow.
30. As a developer of this repository, I want the worktree layout covered by tests against real git repositories, so that path decisions are verified rather than assumed.
31. As a user browsing `~/.claude-squad/worktrees`, I want a repository's directory to disappear when its last worktree does, so that the root shows only the repositories that have worktrees checked out right now.

## Implementation Decisions

### The creation flow becomes one state machine

- A single key (`n`, `KeyNew`) enters the flow. `KeyPrompt` (`N`) is removed from the key
  map and from the menu, along with its help text.
- The flow has three steps: **title**, **branch**, and **program**. The program step is
  skipped entirely when the configuration resolves to a single profile — with no
  `profiles` configured, one is synthesised from `default_program`, so most users see two
  steps.
- The steps are sequential states on the `home` model, entered from the title step rather
  than branching on a flag captured at key-press time. The `promptAfterName` flag and the
  prompt state disappear with the prompt step.
- Escape or ctrl+c at any step abandons the flow and discards the half-created session, as
  the title step does today.

### The session title is a label, but a unique one

- The title keeps its existing constraints: non-empty, at most 32 display columns.
- New: a title that matches an existing session's title is rejected at the title step with
  an inline error, and the flow stays on that step. This is a stopgap, not a model: stored
  state is located by title, and the tmux session name is derived from it, so duplicates
  corrupt state today. Keying storage by something stable is out of scope (see below).

### The branch step

- The field is pre-filled with the **suggested branch name**: the configured branch prefix
  followed by the sanitised title, using the existing branch-name sanitiser. The
  suggestion is computed once, when the step opens; once the user edits the field it is
  never recomputed.
- If the sanitised title is empty (an emoji-only or punctuation-only title), the
  suggestion falls back to a fixed readable stem after the prefix rather than leaving the
  field holding only the prefix.
- The repository's branches are listed below the field and filtered by its contents,
  reusing the existing debounced, version-tracked branch search that the current prompt
  overlay uses — including its remote branches. Selecting a row replaces the field's
  contents.
- On submit, the typed name is **validated, never rewritten**. Validation is git's own —
  `git check-ref-format --branch` — so the rules are authoritative rather than a second
  guess at them. An invalid name produces an inline error and keeps the user on the step.
  An empty field is likewise rejected.
- On submit with a valid name, the session is created as **existing-branch** if a local or
  remote branch of that name exists, and as **new-branch** otherwise. This is one lookup
  against the repository, and it makes typing a name equivalent to selecting it. The
  existing-branch path already refuses to delete the branch during cleanup; that behaviour
  is what the distinction buys.
- New-branch sessions continue to branch from the repository's `HEAD`. Choosing a base
  branch for a new branch is out of scope.

### The program step

- Shown only when more than one profile is available, reusing the existing profile picker.
- When skipped, the session takes the configured default program, exactly as the `n` flow
  does today.

### Worktree paths

- `resolveWorktreePaths` produces `<worktrees root>/<repository directory name>/<branch
  name>`. The branch name is used as a path fragment as-is, so its slashes create nested
  directories; the whole parent chain is created before `git worktree add` runs.
- The **repository directory name** is the base name of the resolved repository root,
  reduced to a single safe path segment. Two clones whose directories share a name share a
  directory, deliberately.
- The `_<hex nanotime>` uniqueness suffix is removed. Uniqueness now comes from git: a
  branch cannot be checked out in two worktrees at once, and a branch name is unique
  within a repository.
- Both setup paths already remove a stale worktree registration and then delete the
  directory before creating the new one, which is what makes a reused path safe. Keep that
  behaviour; do not add a second mechanism.
- That deletion does need a guard in front of it, which this document did not anticipate.
  Once the uniqueness suffix is gone the path is derived from the branch alone, so two
  sessions on one branch ask for the same directory — and the deletion above would hand it
  to the second by destroying the first's work. The setup paths therefore refuse when a
  live worktree is registered at the path, and delete only what is stale. This is a
  precondition on the existing mechanism, not a second one; see
  `docs/adr/0001-worktree-path-derives-from-branch-name.md`.
- Nothing about persisted state changes: sessions store absolute worktree paths, so
  existing sessions continue to resolve to wherever they already live.

### Reading the worktrees root

- Anything that walks the worktrees root must handle three shapes: the new
  `<repository>/<branch…>` layout, the old flat `<name>_<hex>` layout, and the old
  `<prefix>/<name>_<hex>` layout. The rule is uniform and does not need to know which
  layout it is looking at: **a directory that contains a `.git` entry is a worktree;
  otherwise descend into it and apply the same rule**. Descent must be recursive, because
  a branch name may contain several slashes.
- Cleanup removes the worktree directories it finds, deletes the associated branches as it
  does today, and then removes the per-repository directories that are left empty.
- Ending or pausing a single session prunes the same way, but upwards from the worktree it
  just removed: the directories the branch name nested, then the repository directory,
  stopping at the first directory that still holds something. The walk never climbs past
  the worktrees root, and never touches a stored path that lies outside it.

### What is removed

- The initial-prompt step, the prompt overlay's text area, the prompt-after-name state,
  and the instance's initial-prompt field and its send-on-start handling.
- Sending keys to a session goes too. This paragraph used to keep it, on the understanding
  that prompting an already running session was a capability of its own; it was not.
  `Instance.SendPrompt` was only ever reached from inside the `N` creation flow, and
  `Instance.SendKeys` had a single call site in `ui/preview_test.go` that asserted nothing
  about what it sent. The way to talk to a running agent is to attach to it.

## Testing Decisions

A good test here asserts what the user would observe: which step the flow is on, what the
branch field holds, what title and branch the resulting session carries, and where on disk
a worktree lands. It does not assert which overlay object exists, which private field was
set, or how a string was built. Tests must be hermetic: no real tmux server, no writes
outside a temporary `HOME` and temporary repositories.

Two seams, both with prior art in the repository.

**Seam 1 — `home.handleKeyPress`, in the `app` package.** The whole creation flow is
driven by feeding it `tea.KeyMsg` values and inspecting the resulting model. Prior art:
the confirmation-modal tests already call `h.handleKeyPress(keyMsg)` and assert on the
returned model. This seam is hermetic because `handleKeyPress` *returns* the command that
would start the session; a test simply does not run it. Cover: the two-step happy path;
the suggestion's contents including the prefix; editing the suggestion; a title that
sanitises to nothing; selecting a branch from the filtered list; typing the name of an
existing branch; an invalid branch name; an empty branch field; a duplicate title;
abandoning at each step; the program step appearing only with more than one profile; and
`N` no longer starting anything.

**Seam 2 — the `GitWorktree` lifecycle, in the `session/git` package.** Covers path shape
and the layout rules against real git. Prior art: the existing worktree setup tests, which
point `HOME` at a `t.TempDir()` and run real git commands against temporary repositories.
Cover: the path for a new-branch session; the path for a branch containing slashes;
sessions from the same repository sharing a parent directory; repositories with the same
directory name sharing one; `Setup` succeeding when a stale directory occupies the path;
existing-branch setup leaving the branch alone; and cleanup finding worktrees in all three
layouts and tidying the empty repository directories afterwards.

Not separate seams: the text-input overlay's own key handling (below seam 1 — testing it
directly tests the widget, not the behaviour) and the branch-name suggestion function (
exercised through both seams; the sanitiser's own edge cases are already covered by the
existing sanitiser tests).

**Run tests per package** — `go test ./app`, `go test ./session/git` — and never
`go test ./...`. Some packages in this repository start processes and PTYs during tests;
running the whole tree has brought the developer's machine down. Any new test must be
hermetic enough not to add to that.

## Out of Scope

- **Migrating existing worktrees** to the new layout. They stay where they are; only new
  sessions use the new layout.
- **Re-keying persisted state** by branch name or by a stable id. Title uniqueness is the
  stopgap that makes today's title-keyed storage safe.
- **Choosing a base branch** for a new branch. New branches come from `HEAD`; switching
  base afterwards is a git command inside the worktree.
- **The tmux session name**, which keeps deriving from the session title with its own
  sanitiser.
- **Transliterating accented characters** in the suggestion (so "Sesión" still suggests
  `sesin`). The user can now edit the suggestion, which is what made this urgent.
- **Diagnosing the memory blow-up** when running the whole test tree. Tracked separately.

## Further Notes

- ADR-0001 records the layout decision and the rejected alternatives; contradicting it
  should be raised rather than done quietly.
- The new-branch setup path deletes any branch of the same name before creating the
  worktree. That is safe only because a name that already exists is routed to the
  existing-branch path first — worth preserving that ordering carefully when the
  existing/new decision moves to submit time.
- The branch step reuses the branch search that exists for the current prompt overlay,
  including its debounce and result versioning. Dropping either reintroduces flicker and
  stale results on large repositories.
- With `N` unbound, the key is free. Leaving it unbound is deliberate: the point of this
  change is that there is one way in.
