# Switching between a session's agent and its shell while attached

Status: implemented
ADRs: [ADR-0005](../adr/0005-two-keystrokes-are-kept-from-attached-sessions.md)

## Problem Statement

A session owns two tmux sessions: the one running the agent, and the shell the Terminal
tab opens in the same worktree. They are the two things a user does inside a session —
talk to the agent, and run git — and they are used in alternation: read what the agent
changed, run `git diff`, go back and say what to fix.

Only one of them is reachable at a time, and getting from one to the other costs a trip
through the session list. From inside the agent: `ctrl+q` to detach, `tab` until the
Terminal tab is active, then `enter` to attach. Four keystrokes minimum, three of them
navigating a layout the user did not want to visit, and the count grows with wherever the
tab cursor happened to be left. The way back is the same in reverse.

The layout is a good place to choose *which session* to work in. It has nothing to say
about which side of the session you are on, and passing through it to answer that question
means leaving the session, being shown every other session, and coming back.

## Solution

While attached, `ctrl+y` hands the terminal to the session's other side: the shell if you
are in the agent, the agent if you are in the shell. `ctrl+q` still detaches and is still
the only way back to the session list.

The two sides are attached in a loop, not one at a time. Attaching runs until the user
detaches for the layout; a switch ends one attachment and begins the other without the TUI
drawing in between, so the screen goes from one tmux straight to the other.

The shell is made on demand. It used to exist only once the Terminal tab had been drawn,
which would have made the key work or not depending on which tabs the user had visited
earlier — invisible state deciding whether a keystroke does anything.

## User Stories

1. As a user talking to an agent, I want the shell of that session one keystroke away, so that running a git command is not a trip through the session list.
2. As a user in the shell, I want the same keystroke to take me back to the agent, so that the way back is the way in.
3. As a user who has never opened the Terminal tab, I want the keystroke to work anyway, so that what it does does not depend on where I have been.
4. As a user whose shell has died since I last used it, I want the next switch to open a working one, so that a shell that is gone is not a dead end.
5. As a user whose agent has finished and left, I want the switch to leave me in the shell, so that a keystroke never silently strands me somewhere I did not ask for.
6. As a user detaching after switching, I want the window showing the side I ended on, so that the layout describes where I was.
7. As a user who attached from the diff tab and never switched, I want the diff still there when I come back, so that a look at the agent does not cost me my place.
8. As a user detaching, I want the panes drawn at the size of the panes, so that bouncing in and out does not leave the layout mis-drawn.
9. As a user who leaves the shell with `exit`, I want the terminal back, so that closing a shell on purpose is not how I lose the program.

## Implementation Decisions

### The keystroke is classified before tmux sees it

While a session is attached, claude-squad reads stdin first and forwards what is not its
own (`session/tmux/tmux.go`). That loop used to look for one byte; it now asks
`readAttachAction`, which recognises `ctrl+q` and `ctrl+y` and forwards everything else.

Only a keystroke arriving *alone* in a read is claude-squad's. A longer read is a paste or
an escape sequence, and a detach byte inside one was sent to the session, not typed at us.
This was already how `ctrl+q` behaved; naming it makes it a rule rather than an `nr == 1`.

### Detaching says what it was for

`Attach` returns a channel that closes when the attachment ends, which answers "am I
still attached?" but not "what next?". `TmuxSession` now records a `DetachReason` —
`DetachedToLayout` or `DetachedToSwitch` — written before the channel closes, so a caller
that has received from the channel can read it without a race. Every other path to
detaching leaves the reason at `DetachedToLayout`, which is the answer that costs nothing
if it is wrong: the user ends up in the session list, where they always could.

### An attachment can also end without anybody asking

Leaving the shell with `exit` or `ctrl+d`, or the agent's program quitting, ends the tmux
session while claude-squad is attached to it. Before this change nothing closed the attach
channel then: the caller waited forever with the TUI not drawing, and the `ctrl+q` a user
reaches for at that point ran `Detach` into `Restore` on a session that no longer exists,
which panicked. Both sides had it; making the shell - the side people close on purpose -
reachable in one keystroke is what turned it from an oddity into a matter of time.

So the end of an attachment is now something the session reports, with `DetachedSessionEnded`
as the third reason. It is reached from either direction: the goroutine copying tmux's
output sees EOF with nobody having asked to detach, and `detachFor` finds
`ErrSessionNotFound` when it tries to restore the detached PTY. Both end the attachment,
and the loop treats it as it treats any reason that is not a switch - the terminal goes
back to the session list.

Ending an attachment is therefore a thing two goroutines can race to do, so it went behind
a mutex: `endAttach` closes the channel once, and the first caller's reason is the one the
waiter reads. Attachments are also numbered, because the reader holding stdin cannot be
interrupted while it is blocked on a keystroke: it checks the number it was started with
when a key finally arrives, and a reader whose attachment is over stops there rather than
forwarding into whatever is attached by then. The keystroke that told it costs the user
that one key.

A session that is gone gets no PTY back. The detached PTY stays closed, which is what any
dead session looks like from the outside - `DoesSessionExist` says no, the preview falls
back, attaching is refused - rather than a nil that the next window resize would panic on.

### The loop lives in the app, not in tmux

`app/attach.go` holds the loop, an `attachSide` (agent or shell) and how a side maps to a
tab. The tmux layer reports what was asked for; deciding what to attach next needs the
instance, the terminal pane and the window, which is the app's business. `ui.List.Attach`
was removed with its only caller: the loop attaches the selected instance directly,
because it holds it for the whole attachment.

### The two sides are not symmetric

A shell is disposable and is made whenever one is needed: never opened, or dead since it
was last used, both resolve to "make one". An agent is not — reviving one is what resuming a session does,
worktree and all — so switching to an agent whose tmux is gone keeps the user in the
shell. The alternative, detaching to the layout, moves them somewhere they did not ask
for at the exact moment they asked for something else.

### The tab follows the user, and only if they moved

Detaching after a switch leaves the window on the side the user ended on. Detaching from
the side they went in on leaves the tab alone, which is what keeps the diff tab in place
for someone who attached from it, looked at the agent, and came back.

### The size is asked for again on the way out

Being attached stretches the session's tmux to the whole terminal, and `Restore` gives it
a fresh PTY with no size. Nothing on this path shrank it back, so the pane rendered at
terminal width until the window happened to change — visible before this change, and
twice as often with a key that invites bouncing. Detaching now returns `tea.WindowSize()`,
the same thing resuming a session already did.

### The help screen's bit moved

The attach help is the only thing on screen before the terminal is handed over, so it is
where the new key is named, alongside `ctrl+q`. Its mask moved from `1 << 2` to `1 << 4`
so that the added line is shown once to people who had already dismissed the old text.
Clearing the old bit instead would have reopened a screen they had dealt with; adding a
second screen would have shown two overlays before one attachment. The general help,
which is always shown on `?`, names the key permanently.

## Testing Decisions

- The classification and the reason it produces are tested directly
  (`session/tmux/tmux_test.go`): `ctrl+q` means the layout, `ctrl+y` means the other side,
  and everything else — an ordinary key, `ctrl+c`, a paste containing either of ours,
  an empty read — is forwarded and detaches nothing. The stdin loop itself reads
  `os.Stdin` and cannot be driven from a test without a refactor that would exist only
  for the test.
- `TerminalPane.EnsureSession` is tested for making a shell that does not exist and
  reusing one that does (`ui/terminal_test.go`). This needed a seam: the pane now holds
  the function that makes a tmux session, defaulted to the real one, so a test can watch
  shells being made without a tmux server to make them in.
- The three ways an attachment ends are pinned where they can be reached without a live
  attachment (`session/tmux/tmux_test.go`): `endAttach` releases the waiter once and keeps
  the first reason, a numbered attachment tells a stale reader to stop - including when
  the next attachment has already started, which is what switching does - and detaching
  from a session that has gone away ends the attachment as `DetachedSessionEnded` instead
  of panicking.
- The loop in `app/attach.go` is not tested end to end. Driving it would mean faking the
  instance, the terminal pane, the window and the menu at once, to assert an ordering the
  two tested pieces already pin down.

## Out of Scope

- **Switching sessions while attached.** The same keystroke shape would work for "next
  session in the list", but it has to decide what happens to the selection, and to a
  session that is paused or still loading. A different feature that shares a mechanism.
- **A configurable key.** `switch_key` in the config is the answer when `ctrl+y` turns out
  to hurt; see ADR-0005 for why the cost was taken now rather than the setting.
- **The diff tab as a third side.** There is nothing to attach to: it is rendered by
  claude-squad, not by a tmux session.

## Further Notes

The commands the loop produces - hiding an error box after its three seconds, refreshing
the panes - reach the runtime only when the attach help screen has already been seen,
which is every attachment after the first. On the first one the loop runs inside the
overlay's dismissal, which returns its own commands and has nowhere to put these.

A session that ends under an attachment costs the user one keystroke: the reader holding
stdin is blocked in a read, and the key that unblocks it is consumed as it discovers there
is nothing left to forward to.

Both the attach loop and bubbletea read `os.Stdin`, which is true before this change and
unchanged by it. What is new is how often the transition happens, so a keystroke lost
immediately after switching is most likely the existing 50 ms window in which the attach
loop discards what it reads to swallow the terminal's control sequences.
