package overlay

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// maxVisibleRepos is how many known repositories the list shows at once.
const maxVisibleRepos = 5

// RepoInputOverlay is the repository step of session creation: an editable path,
// pre-filled with the active repository, with the known repositories listed underneath and
// filtered by what has been typed. Unlike the branch step it holds the whole list, because
// the list is already in memory: nothing has to be looked up to filter it.
type RepoInputOverlay struct {
	// typed is what the user has typed, and the filter the repository list is matched with.
	typed string
	// pristine is true while the field still holds the repository it was pre-filled with.
	// Nothing has been typed yet, so nothing filters the list and the step opens showing
	// every known repository. The first edit ends it, and from then on the list is
	// exactly what the field says it is. Emptying the field filters by nothing, so
	// ctrl+u is the one keystroke back to the list the step opened with.
	pristine bool
	// repos are every known repository, in the order they were offered.
	repos []string
	// expand turns what is typed into the path it names, so that a ~ or a relative start
	// is matched against the known repositories as the path it means rather than as the
	// characters it is written with. The step does not know where a home directory or an
	// active repository is; whoever built it does.
	expand func(string) string
	// candidateDir is the directory the listed candidates were read from, and is empty
	// while the list is the known repositories. Tab fills it when completion has nothing
	// left to add: the contents of the directory are the answer it could not give.
	candidateDir string
	// results are the known repositories matching what has been typed.
	results []string
	// cursor selects one of the results, or -1 for the typed path.
	cursor int
	// outcome says what creating sessions in the current path would do.
	outcome string
	// outcomeBlocks is true when the outcome is a reason the path cannot be used.
	outcomeBlocks bool
	width         int
}

// RepoInputEvent is what a key press did to the repository step.
type RepoInputEvent struct {
	// Submitted is true when the user accepted what the field holds.
	Submitted bool
	// ValueChanged is true when the step would now submit a different path, so what that
	// path would do has to be resolved again.
	ValueChanged bool
	// Complete is true when the user asked for the typed path to be completed, which needs
	// the filesystem and so is done by the caller.
	Complete bool
}

// NewRepoInputOverlay creates the repository step over the known repositories, with the
// field pre-filled with the active repository. expand turns typed text into the path it
// names, and may be nil when there is nothing to expand.
func NewRepoInputOverlay(active string, repos []string, expand func(string) string) *RepoInputOverlay {
	r := &RepoInputOverlay{typed: active, repos: repos, expand: expand, cursor: -1, pristine: true}
	r.refilter()
	return r
}

// ShowCandidates lists the directories found under dir in place of the known repositories.
// It is what tab falls back to when it cannot extend the field: the step cannot make the
// choice, so it shows what the choice is between. The next edit puts the known
// repositories back.
func (r *RepoInputOverlay) ShowCandidates(dir string, paths []string) {
	r.candidateDir = dir
	r.results = paths
	r.cursor = -1
}

// Value returns the path the step will submit: a selected repository if one is highlighted
// in the list, otherwise the typed path.
func (r *RepoInputOverlay) Value() string {
	if r.cursor >= 0 && r.cursor < len(r.results) {
		return r.results[r.cursor]
	}
	return r.typed
}

// SetValue replaces what the field holds, dropping any selection: the caller completed the
// typed path, and what it completed to is now what is being edited.
func (r *RepoInputOverlay) SetValue(value string) {
	r.typed = value
	r.cursor = -1
	r.pristine = false
	r.refilter()
}

// refilter matches the list against the field. A pre-fill filters by nothing, so what the
// list shows and what the field holds can never disagree about what has been typed. It
// also puts the known repositories back in place of any candidates tab was showing.
func (r *RepoInputOverlay) refilter() {
	filter := r.typed
	if r.pristine {
		filter = ""
	}
	expanded := filter
	if r.expand != nil {
		expanded = r.expand(filter)
	}
	r.candidateDir = ""
	r.results = filterRepos(r.repos, filter, expanded)
}

// SetWidth sets the rendering width.
func (r *RepoInputOverlay) SetWidth(width int) {
	r.width = width
}

// SetOutcome records what creating sessions in the current path would do.
func (r *RepoInputOverlay) SetOutcome(outcome string, blocks bool) {
	r.outcome = outcome
	r.outcomeBlocks = blocks
}

// HandleKeyPress processes a key press and reports what it did to the step.
func (r *RepoInputOverlay) HandleKeyPress(msg tea.KeyMsg) RepoInputEvent {
	switch msg.Type {
	case tea.KeyEnter:
		return RepoInputEvent{Submitted: true}
	case tea.KeyTab:
		return RepoInputEvent{Complete: true}
	case tea.KeyUp:
		if r.cursor >= 0 {
			r.cursor--
			return RepoInputEvent{ValueChanged: true}
		}
		return RepoInputEvent{}
	case tea.KeyDown:
		if r.cursor < len(r.results)-1 {
			r.cursor++
			return RepoInputEvent{ValueChanged: true}
		}
		return RepoInputEvent{}
	case tea.KeyBackspace:
		if r.Value() == "" {
			return RepoInputEvent{}
		}
		return r.edit(func(value string) string {
			runes := []rune(value)
			return string(runes[:len(runes)-1])
		})
	case tea.KeyCtrlU:
		// The field arrives holding a path nobody typed. Clearing it is what turns the
		// step into the list of known repositories, so it costs one key rather than one
		// per character of a path that can be thirty long.
		if r.Value() == "" {
			return RepoInputEvent{}
		}
		return r.edit(func(string) string { return "" })
	case tea.KeyCtrlW:
		if r.Value() == "" {
			return RepoInputEvent{}
		}
		return r.edit(dropLastSegment)
	case tea.KeySpace:
		return r.edit(func(value string) string { return value + " " })
	case tea.KeyRunes:
		return r.edit(func(value string) string { return value + string(msg.Runes) })
	}
	return RepoInputEvent{}
}

// dropLastSegment removes the last element of a path, keeping the separator so that what
// is left is the directory the removed one lived in and the next segment can be typed
// straight onto it. A path with nothing left to remove clears.
func dropLastSegment(value string) string {
	trimmed := strings.TrimRight(value, string(filepath.Separator))
	if i := strings.LastIndexByte(trimmed, filepath.Separator); i >= 0 {
		return trimmed[:i+1]
	}
	return ""
}

// edit replaces the field's contents. A repository highlighted in the list is the starting
// point, so typing continues from whatever is on screen.
func (r *RepoInputOverlay) edit(change func(string) string) RepoInputEvent {
	r.SetValue(change(r.Value()))
	return RepoInputEvent{ValueChanged: true}
}

// filterRepos returns the repositories matching either the text as it was written or the
// path that text names, without regard to case. A path is matched whole, so a repository
// can be found by its name or by any part of where it lives.
//
// Both are tried because the field is two things at once. `zeta` is a name to search for
// and means nothing as a path; `~/git/` is a place and means nothing as a name — the
// repositories are stored as absolute paths, so matching a tilde literally finds none of
// them. Asking both questions saves having to decide which one the user was asking.
//
// A trailing separator is dropped before matching. Completing a path leaves one behind to
// invite the next segment, and a repository would otherwise stop matching the moment the
// field held its own path exactly — the list emptying at the point it had just been
// narrowed to one answer.
func filterRepos(repos []string, filter string, expanded string) []string {
	written, named := repoFilterTerm(filter), repoFilterTerm(expanded)
	if written == "" && named == "" {
		return repos
	}
	matched := make([]string, 0, len(repos))
	for _, repo := range repos {
		lowered := strings.ToLower(repo)
		if (written != "" && strings.Contains(lowered, written)) ||
			(named != "" && strings.Contains(lowered, named)) {
			matched = append(matched, repo)
		}
	}
	return matched
}

// repoFilterTerm normalises text into something a stored repository path can be matched
// against.
func repoFilterTerm(text string) string {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	return strings.TrimRight(trimmed, string(filepath.Separator))
}

// Render renders the repository step.
func (r *RepoInputOverlay) Render() string {
	innerWidth := stepInnerWidth(r.width)
	divider := stepDivider(innerWidth)

	var s strings.Builder
	s.WriteString(stepTitleStyle.Render("Repository") + "\n\n")
	if r.cursor >= 0 {
		s.WriteString(stepValueStyle.Render(stepDecorateLeft("", r.Value(), "", innerWidth)))
	} else {
		s.WriteString(stepValueStyle.Render(stepDecorateLeft("", r.typed, "█", innerWidth)))
	}
	if r.outcome != "" {
		style := stepDimStyle
		if r.outcomeBlocks {
			style = stepBlockedStyle
		}
		s.WriteString("\n" + style.Render(stepDecorateLeft("→ ", r.outcome, "", innerWidth)))
	}
	s.WriteString("\n\n" + divider + "\n\n")

	// The label is what says which list this is, so it survives the cut and the directory
	// gives up its start — the same bargain the field and the rows make.
	title := stepTruncate("Known repositories", innerWidth)
	if r.candidateDir != "" {
		title = stepDecorateLeft("Directories in ", r.candidateDir, "", innerWidth)
	}
	s.WriteString(stepTitleStyle.Render(title) + "\n\n")
	s.WriteString(r.renderResults(innerWidth))
	s.WriteString("\n\n" + divider + "\n\n")
	s.WriteString(stepDimStyle.Render("↑/↓ pick a repository • tab complete • ctrl+u clear • enter continue • esc cancel"))

	return stepBoxStyle.Render(s.String())
}

func (r *RepoInputOverlay) renderResults(innerWidth int) string {
	if len(r.results) == 0 {
		return stepDimStyle.Render("  No matching repositories")
	}

	start, end := stepListWindow(len(r.results), r.cursor, maxVisibleRepos)

	var s strings.Builder
	for i := start; i < end; i++ {
		if i == r.cursor {
			s.WriteString(stepSelectedStyle.Render(stepDecorateLeft("> ", r.results[i], "", innerWidth)))
		} else {
			s.WriteString(stepDimStyle.Render(stepDecorateLeft("  ", r.results[i], "", innerWidth)))
		}
		if i < end-1 {
			s.WriteString("\n")
		}
	}
	return s.String()
}
