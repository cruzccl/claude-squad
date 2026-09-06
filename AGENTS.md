## Agent skills

### Issue tracker

Issues live as markdown files under `.scratch/<feature>/` in this repo. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical roles, each label string equal to its role name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

### Design docs

`docs/design/` holds one document per feature: the problem it solved, the solution, the
user stories, and the decisions taken while building it. An ADR records a single decision
and the alternatives rejected; a design doc records the whole feature that decision was
taken inside. Read the design doc for how a feature works, the ADR for why one choice
within it was made and what was turned down.

**Design docs are named, not numbered.** ADRs are numbered because the number is the order
the decisions were taken in and never changes. Numbering designs to match would claim a
one-to-one relationship that does not exist:
`choosing-the-repository-a-session-is-created-in` rests on ADR-0004 *and* ADR-0003, and a
decision can be recorded with no design doc behind it at all.

The two are correlated by name instead, in both directions:

- Each ADR carries a `Design:` line under its title, linking the design doc it came out of.
- Each design doc carries an `ADRs:` line under its `Status:`, linking every decision it
  rests on.

Cite an ADR in prose as `ADR-0003`, hyphenated and four digits.

**A new design doc earns a row in the README, in the same commit.** The *Changes over
v1.0.20* table is the list of changes that were designed, so a design document missing
from it makes the table wrong about what this fork is. Append the row — the table is
chronological, oldest first — with a sentence saying what the change does and a link to
the document. Nothing else goes in that table: a change written without a design document
is left to the git log, which is what keeps a row in it worth reading.
