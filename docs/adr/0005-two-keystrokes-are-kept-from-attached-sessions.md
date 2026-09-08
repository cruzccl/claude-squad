# Two keystrokes are kept from attached sessions, and the rest are the session's

Design: [switching-between-agent-and-shell-while-attached](../design/switching-between-agent-and-shell-while-attached.md)

While a session is attached, claude-squad reads stdin before tmux does. Anything it keeps
for itself is unreachable inside every session it runs: the agent never sees it, the shell
never sees it, and there is no escape hatch that would let a user send it through. The
budget is two — `ctrl+q` to detach, `ctrl+y` to switch between a session's agent and its
shell — and a third costs another key taken from every program the user runs inside `cs`.

`ctrl+y` was chosen by elimination rather than preference. Claude Code binds `ctrl+t`
(`app:toggleTodos`) and `ctrl+o` (`app:toggleTranscript`) globally, along with `ctrl+r`,
`ctrl+g`, `ctrl+s`, `ctrl+b`, `ctrl+l` and `ctrl+j` in its own contexts; the shell side
spends `ctrl+a`, `ctrl+e`, `ctrl+w`, `ctrl+u` and `ctrl+k` on line editing that is used
constantly. What is left is `ctrl+y`, whose cost is readline's `yank` — a key that pastes
back what `ctrl+w` or `ctrl+u` just deleted.

That cost is real and is being paid deliberately. It is paid because the alternative is
paying it on the other side, where the collision is with the agent the user is talking to,
and because the keys that collide with nothing at all do not exist.

## Considered options

- **`ctrl+t`, for "toggle".** Rejected on the fact rather than the mnemonic: it is
  Claude Code's global todo toggle, and Claude Code is the default program. The user could
  rebind it in `~/.claude/keybindings.json`, but that makes claude-squad's key work only
  after configuring something else.
- **A configurable key (`switch_key` in the config).** Rejected for now, not on principle.
  It is the right answer the day `ctrl+y` hurts, and it costs a validated setting, a
  documented default, and a rule for what happens when someone sets it to `ctrl+q`. Taken
  before there is evidence of pain, it is a setting nobody has an opinion about, and it
  does not remove the need to choose a default — which is this decision.
- **A prefix key, tmux-style (`ctrl+q` then a letter).** Rejected: `ctrl+q` must detach
  instantly. Making it wait for a second byte, or for a timeout to decide it was alone,
  puts a delay on the key people press to get out of somewhere.
- **A key sequence with no prefix, such as pressing `ctrl+q` twice.** Rejected for the
  same reason and one more: the first press has already detached by the time the second
  arrives.
- **Letting tmux bind it inside the session instead.** Rejected: it is the same theft, one
  layer down, and it would have to be installed into every session claude-squad creates —
  including the shells it makes on demand.

## Consequences

- The keys are named where the user is about to lose them: the attach help screen, shown
  before the terminal is handed over, and the general help on `?`. A key taken silently is
  indistinguishable from a broken program.
- `readAttachAction` is the one place that decides what claude-squad keeps. A future key
  is a case there, and this ADR is the argument it has to get past.
- Only a keystroke arriving alone in a read is claude-squad's; a paste containing one is
  forwarded. This is what keeps the budget from spending bytes it did not mean to.
- The reason for a detach is now part of what an attachment reports, so the cost of the
  next key like this one is a case in an enum rather than another channel.
