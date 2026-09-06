# The active repository is not persisted; the launch directory wins

Design: [choosing-the-repository-a-session-is-created-in](../design/choosing-the-repository-a-session-is-created-in.md)

Since a second `cs` is refused rather than made to share the saved state (ADR-0003), one
process has to be able to create sessions in every repository, so session creation opens
by choosing one. That choice is held in memory for as long as the process runs and is
never written down: every `cs` starts with the directory it was launched in as the active
repository, whatever was chosen last time.

The choice looks like exactly the kind of preference that should stick, which is why the
absence is worth recording. It does not stick because `cd ~/git/other && cs` is already a
statement about which repository the user means, made with the shell's own vocabulary, and
a remembered choice silently outranks it — the failure mode being a session created in
yesterday's repository, discovered after the worktree exists and the agent is running.

## Considered options

- **Persist the active repository in `state.json`.** Rejected as above: it overrides an
  explicit gesture with an implicit one, and the mistake is silent and expensive to undo.
- **Persist it, but let the launch directory override it when it is a repository.**
  Rejected: the stored value then only decides the case where `cs` is launched from
  somewhere that is not a repository — a rule nobody could predict from the outside, and
  one that makes the answer to "which repository is active?" depend on where you happened
  to be standing.
- **A `--repo` flag instead of a step.** Rejected as a different feature, not an
  alternative to this one: it moves the choice back to launch time, which is the moment
  the state lock made expensive, since changing repository means quitting `cs`.

## Consequences

- The *list* of known repositories is persisted (`known_repos` in `state.json`) even
  though the active one is not. This is the intended split: remembering what to offer
  costs the user nothing if it is wrong, remembering what is selected costs a misplaced
  session.
- The repository step is shown on every creation, including when only one repository is
  known. With nothing remembered, the step is the only place the choice can be made, and
  it is also where launching `cs` outside a git repository is caught — early and with an
  explanation, instead of late when the worktree fails to be created.
