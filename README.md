# Claude Squad

**Personal fork.** This repository is a fork of
[smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad) that I maintain for my
own use. It comes with no support, no releases, and no promise of staying in sync with
upstream.

All credit for Claude Squad belongs to its author and
[contributors](https://github.com/smtg-ai/claude-squad/graphs/contributors) — the idea,
the design, and effectively all of the code are theirs. If you are looking for the
project itself rather than my personal changes, go to the
[original repository](https://github.com/smtg-ai/claude-squad).

## Changes over v1.0.20

This fork branches from upstream
[v1.0.20](https://github.com/smtg-ai/claude-squad/releases/tag/v1.0.20) (`ce1ffb4`), which
is the version every change below is built on.

These are the changes that were designed rather than merely written, oldest first. Each
links a document recording the problem it solved, the decisions taken, and the
alternatives rejected. The smaller ones — the installer, the version command, the removal
of the machinery for publishing a product — are in the git log.

| Change | Design |
| --- | --- |
| **One session creation flow, with worktrees named after the branch** — Creating a session is one flow (title, then branch, then program) instead of two entry points that disagreed, and a session's worktree path comes from its branch name rather than from its title. | [design](docs/design/unified-session-creation.md) |
| **Killing a session never destroys unfinished work** — `D` used to delete the branch on a flag fixed at creation time. It now refuses outright on uncommitted changes, deletes a branch only when the session created it and nothing was committed to it, and says which it will do before you confirm. | [design](docs/design/kill-preserves-unfinished-work.md) |
| **One `cs` owns the saved state at a time** — Two running at once used to delete each other's sessions silently, leaving agents running in worktrees nothing listed any more. A second one now refuses to start and says who holds the lock. | [design](docs/design/one-cs-owns-the-saved-state.md) |
| **Session creation asks which repository first** — `cs` decided its repository once, at startup, and working in another meant quitting, which the state lock made expensive. Creation now opens on a repository step, pre-filled and with the known repositories listed, and a branch name taken in one repository no longer blocks it in another. | [design](docs/design/choosing-the-repository-a-session-is-created-in.md) |

Licensed under [AGPL-3.0](LICENSE.md).
