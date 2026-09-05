# Killing a session refuses on uncommitted work rather than committing it

`D` used to delete a session's branch with `git branch -D` whenever the session had created
that branch, consulting nothing but a flag fixed at creation time — not whether the branch
had commits, not whether the worktree was dirty. We made the kill conditional: it is
refused while the worktree has uncommitted changes, and the branch is deleted only when it
is new and untouched, still pointing at the commit it was created from.

Refusing is the surprising half. Pause, two keys away, meets the same situation and
commits the changes on the user's behalf. We deliberately did not do that here.

## Considered options

- **Commit the changes, as Pause does.** Rejected, though it was the closest call. It would
  have given a cleaner invariant — `D` never destroys anything, no asterisks — and it reuses
  an idiom the program already has. But it makes a key labelled *kill* write commits, and
  because "dirty" here counts untracked files, the `add .` would swallow build artefacts and
  stray `.env` files into the history of a branch the user is walking away from. Pause can
  afford that because the session is coming back; a kill is the user leaving.
- **Keep the branch but let the worktree removal proceed anyway.** Rejected as theatre. The
  removal is `-f`, so the uncommitted changes die regardless, and what survives is a branch
  pointing at the base commit — a headstone for the work that was just discarded. The
  condition would protect a pointer, not the work.
- **Apply the refusal only to sessions on new branches**, matching the original framing of
  the problem. Rejected: it would make `D` more destructive on a pre-existing branch —
  possibly shared, possibly with more at stake — than on a throwaway branch created minutes
  ago. The branch's origin correctly decides what is deleted afterwards; it has nothing to
  say about whether unsaved changes may be thrown away.
- **Refuse whenever a git command fails**, on the principle that uncertainty should block.
  Rejected: `D` is also the escape hatch for a session whose repository has moved or broken,
  and refusing there leaves a row that cannot be removed by any means. Uncertainty instead
  proceeds and keeps the branch.

## Consequences

- The refusal needs somewhere to live, and it cannot be `Cleanup()`. `Cleanup()` removes the
  worktree before it decides about the branch, so it has already destroyed something by the
  time it could refuse; and the handler deletes the persisted state before calling it, with
  `ui/list.go` only logging what comes back. The rule therefore splits: a precondition in the
  `D` handler, which decides whether the kill happens, and a policy inside `Cleanup()`, which
  decides what a kill destroys. The `Start()` rollback and the `Resume()` error paths inherit
  the policy and not the precondition — a rollback has nobody to refuse, and a preserved
  worktree there is unowned rubbish rather than rescued work.
- Because the confirmation now states the outcome, the outcome is computed before the
  confirmation is shown and again inside `Cleanup()`. The two can disagree: the agent runs on
  in tmux while the confirmation is open and may commit in that gap. The divergence is
  one-directional — the branch stops being untouched and `Cleanup()` keeps a branch the
  confirmation promised to delete. It fails towards keeping.
- A refused kill has to be escapable, and the escape it offers is attaching to the session
  (`o`) and committing or discarding the changes there. `c` would be shorter — it commits,
  removes the worktree and keeps the branch, after which `D` proceeds — but `c` is the
  handoff key, not a save button. It is named for what it lets the user do next: it frees the
  branch so they can check it out themselves, and copies the branch name to their clipboard
  for that purpose. Pointing at it here would commit the user's work under a message reading
  `(paused)` on a branch they are abandoning, overwrite their clipboard with a name they did
  not ask for, and leave the session parked so a second `D` is needed. The behaviour we
  rejected stays available to anyone who wants it; it is simply not what a kill suggests.
- The checkout guard widens from the main working tree to any worktree, reusing
  `BranchCheckedOutAt`. The session's own worktree holds its own branch and must be excluded.
- `cs reset` is untouched and still deletes every branch it finds with `-D`. The two paths now
  disagree about what is safe to delete, deliberately: one is a key in a list, the other is a
  command that says what it does.
