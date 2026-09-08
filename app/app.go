package app

import (
	"claude-squad/config"
	"claude-squad/keys"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const GlobalInstanceLimit = 10

// Run is the main entrypoint into the application.
func Run(ctx context.Context, program string, autoYes bool) error {
	p := tea.NewProgram(
		newHome(ctx, program, autoYes),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // Mouse scroll
	)
	_, err := p.Run()
	return err
}

// sizedOverlay is what every overlay the model can put on screen has in common: it is
// told how wide to be. Rendering is not part of it — the help and confirmation overlays
// take whitespace options the creation steps do not — so currentOverlay hands back a
// function that renders rather than the overlay itself.
type sizedOverlay interface {
	SetWidth(width int)
}

type state int

const (
	stateDefault state = iota
	// stateNewRepo is the first step of session creation: the repository the session is
	// created in.
	stateNewRepo
	// stateNewTitle is the second step: naming the session.
	stateNewTitle
	// stateNewBranch is the third step: the branch the session works on.
	stateNewBranch
	// stateNewProgram is the fourth step: the program to run. It is only entered when
	// there is more than one profile to choose from.
	stateNewProgram
	// stateHelp is the state when a help screen is displayed.
	stateHelp
	// stateConfirm is the state when a confirmation modal is displayed.
	stateConfirm
)

type home struct {
	ctx context.Context

	// -- Storage and Configuration --

	program string
	autoYes bool
	// repoPath is the active repository: the one new sessions branch from, and whose
	// branches the branch step lists. It starts as the directory the application was
	// started in and changes when the repository step picks another; it is never saved,
	// so every run starts from where it was launched.
	repoPath string

	// storage is the interface for saving/loading data to/from the app's state
	storage *session.Storage
	// appConfig stores persistent application configuration
	appConfig *config.Config
	// appState stores persistent application state like seen help screens
	appState config.AppState

	// -- State --

	// state is the current discrete state of the application
	state state
	// keySent is used to manage underlining menu items
	keySent bool

	// -- UI Components --

	// list displays the list of instances
	list *ui.List
	// menu displays the bottom menu
	menu *ui.Menu
	// tabbedWindow displays the tabbed window with preview and diff panes
	tabbedWindow *ui.TabbedWindow
	// errBox displays error messages
	errBox *ui.ErrBox
	// global spinner instance. we plumb this down to where it's needed
	spinner spinner.Model
	// repoOverlay is the repository step of session creation
	repoOverlay *overlay.RepoInputOverlay
	// branchOverlay is the branch step of session creation
	branchOverlay *overlay.BranchInputOverlay
	// profileOverlay is the program step of session creation
	profileOverlay *overlay.ProfileOverlay
	// textOverlay displays text information
	textOverlay *overlay.TextOverlay
	// confirmationOverlay displays confirmation modals
	confirmationOverlay *overlay.ConfirmationOverlay
}

func newHome(ctx context.Context, program string, autoYes bool) *home {
	// Load application config
	appConfig := config.LoadConfig()

	// Load application state
	appState := config.LoadState()

	// Initialize storage
	storage, err := session.NewStorage(appState)
	if err != nil {
		fmt.Printf("Failed to initialize storage: %v\n", err)
		os.Exit(1)
	}

	// New sessions branch from the repository the application was started in, named by its
	// root so that launching cs anywhere inside a repository means the same repository.
	// A launch outside one leaves the directory as it is: the repository step is where
	// that is reported, rather than here where there is nobody to tell.
	repoPath, err := os.Getwd()
	if err != nil {
		log.WarningLog.Printf("failed to get working directory: %v", err)
		repoPath = "."
	}
	if root, err := git.RepoRoot(repoPath); err == nil {
		repoPath = root
	}

	h := &home{
		ctx:          ctx,
		repoPath:     repoPath,
		spinner:      spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
		storage:      storage,
		appConfig:    appConfig,
		program:      program,
		autoYes:      autoYes,
		state:        stateDefault,
		appState:     appState,
	}
	h.list = ui.NewList(&h.spinner, autoYes)

	// Load saved instances
	instances, err := storage.LoadInstances()
	if err != nil {
		fmt.Printf("Failed to load instances: %v\n", err)
		os.Exit(1)
	}

	// Add loaded instances to the list
	for _, instance := range instances {
		h.list.AddInstance(instance)
		if autoYes {
			instance.AutoYes = true
		}
	}

	return h
}

// updateHandleWindowSizeEvent sets the sizes of the components.
// The components will try to render inside their bounds.
func (m *home) updateHandleWindowSizeEvent(msg tea.WindowSizeMsg) {
	// List takes 20% of width, preview takes 80%
	listWidth := int(float32(msg.Width) * 0.2)
	tabsWidth := msg.Width - listWidth

	// Menu takes 10% of height, list and window take 90%
	contentHeight := int(float32(msg.Height) * 0.9)
	menuHeight := msg.Height - contentHeight - 1     // minus 1 for error box
	m.errBox.SetSize(int(float32(msg.Width)*0.9), 1) // error box takes 1 row

	m.tabbedWindow.SetSize(tabsWidth, contentHeight)
	m.list.SetSize(listWidth, contentHeight)

	for _, ov := range m.scaledOverlays() {
		ov.SetWidth(int(float32(msg.Width) * 0.6))
	}

	previewWidth, previewHeight := m.tabbedWindow.GetPreviewSize()
	if err := m.list.SetSessionPreviewSize(previewWidth, previewHeight); err != nil {
		log.ErrorLog.Print(err)
	}
	m.menu.SetSize(msg.Width, menuHeight)
}

func (m *home) Init() tea.Cmd {
	// Upon starting, we want to start the spinner. Whenever we get a spinner.TickMsg, we
	// update the spinner, which sends a new spinner.TickMsg. I think this lasts forever lol.
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			time.Sleep(100 * time.Millisecond)
			return previewTickMsg{}
		},
		tickUpdateMetadataCmd(m.snapshotActiveInstances(), m.list.GetSelectedInstance()),
	)
}

func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case hideErrMsg:
		m.errBox.Clear()
	case previewTickMsg:
		cmd := m.instanceChanged()
		return m, tea.Batch(
			cmd,
			func() tea.Msg {
				time.Sleep(100 * time.Millisecond)
				return previewTickMsg{}
			},
		)
	case keyupMsg:
		m.menu.ClearKeydown()
		return m, nil
	case metadataUpdateDoneMsg:
		for _, r := range msg.results {
			// Skip instances that were paused while metadata was being computed
			if r.instance.Status == session.Paused {
				continue
			}
			if r.updated {
				r.instance.SetStatus(session.Running)
			} else if r.hasPrompt {
				r.instance.TapEnter()
			} else {
				r.instance.SetStatus(session.Ready)
			}
			if r.diffStats != nil && r.diffStats.Error != nil {
				if !strings.Contains(r.diffStats.Error.Error(), "base commit SHA not set") {
					log.WarningLog.Printf("could not update diff stats: %v", r.diffStats.Error)
				}
				r.instance.SetDiffStats(nil)
			} else {
				r.instance.SetDiffStats(r.diffStats)
			}
		}
		return m, tickUpdateMetadataCmd(m.snapshotActiveInstances(), m.list.GetSelectedInstance())
	case tea.MouseMsg:
		// Handle mouse wheel events for scrolling the diff/preview pane
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelDown || msg.Button == tea.MouseButtonWheelUp {
				selected := m.list.GetSelectedInstance()
				if selected == nil || selected.Status == session.Paused {
					return m, nil
				}

				switch msg.Button {
				case tea.MouseButtonWheelUp:
					m.tabbedWindow.ScrollUp()
				case tea.MouseButtonWheelDown:
					m.tabbedWindow.ScrollDown()
				}
			}
		}
		return m, nil
	case branchSearchDebounceMsg:
		if m.branchOverlay == nil || msg.version != m.branchOverlay.FilterVersion() {
			return m, nil // stale, the filter has changed since
		}
		return m, m.runBranchSearch(msg.filter, msg.version)
	case branchSearchResultMsg:
		if m.branchOverlay == nil {
			return m, nil
		}
		if m.branchOverlay.SetResults(msg.branches, msg.version) {
			// The picked branch dropped out of the list and the step fell back to the
			// typed name. Resolve it now, undebounced: this is a result arriving, not a
			// keystroke, so the line would otherwise stay blank until the next one.
			return m, m.runBranchOutcome(m.branchOverlay.Value(), m.branchOverlay.ValueVersion())
		}
		return m, nil
	case branchOutcomeDebounceMsg:
		if m.branchOverlay == nil || msg.version != m.branchOverlay.ValueVersion() {
			return m, nil // stale, the name has changed since
		}
		return m, m.runBranchOutcome(msg.name, msg.version)
	case branchOutcomeMsg:
		if m.branchOverlay != nil {
			m.branchOverlay.SetOutcome(msg.outcome, msg.blocks, msg.version)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	case tea.WindowSizeMsg:
		m.updateHandleWindowSizeEvent(msg)
		return m, nil
	case error:
		// Handle errors from confirmation actions
		return m, m.handleError(msg)
	case instanceChangedMsg:
		// Handle instance changed after confirmation action
		return m, m.instanceChanged()
	case instanceStartedMsg:
		// Select the instance that just started (or failed)
		m.list.SelectInstance(msg.instance)

		if msg.err != nil {
			m.list.Kill()
			return m, tea.Batch(m.handleError(msg.err), m.instanceChanged())
		}

		// Save after successful start
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}
		if m.autoYes {
			msg.instance.AutoYes = true
		}

		m.menu.SetState(ui.StateDefault)
		m.showHelpScreen(helpStart(msg.instance), nil)

		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// currentOverlay returns the overlay the state on screen shows. The bool says whether the
// state shows one at all, which is a different question from whether one is there: a state
// that should have an overlay and has none is a bug, and the caller decides what to do
// about it.
//
// This is the only place that maps a state to its overlay. Everything else asks here, so
// adding a step means adding one case rather than editing every cascade that mentions the
// states one by one.
func (m *home) currentOverlay() (render func() string, showsOverlay bool) {
	switch m.state {
	case stateNewRepo:
		if m.repoOverlay == nil {
			return nil, true
		}
		return m.repoOverlay.Render, true
	case stateNewBranch:
		if m.branchOverlay == nil {
			return nil, true
		}
		return m.branchOverlay.Render, true
	case stateNewProgram:
		if m.profileOverlay == nil {
			return nil, true
		}
		return m.profileOverlay.Render, true
	case stateHelp:
		if m.textOverlay == nil {
			return nil, true
		}
		// Wrapped, not taken as a method value: these two render with whitespace options
		// the creation steps have no use for, and neither is passed any here.
		return func() string { return m.textOverlay.Render() }, true
	case stateConfirm:
		if m.confirmationOverlay == nil {
			return nil, true
		}
		return func() string { return m.confirmationOverlay.Render() }, true
	}
	return nil, false
}

// scaledOverlays are the overlays whose width follows the window, whether or not they are
// the one on screen: an overlay opened earlier keeps its size until it is shown again. The
// confirmation modal is not among them — it is sized to its question when it is opened.
func (m *home) scaledOverlays() []sizedOverlay {
	var overlays []sizedOverlay
	if m.repoOverlay != nil {
		overlays = append(overlays, m.repoOverlay)
	}
	if m.branchOverlay != nil {
		overlays = append(overlays, m.branchOverlay)
	}
	if m.profileOverlay != nil {
		overlays = append(overlays, m.profileOverlay)
	}
	if m.textOverlay != nil {
		overlays = append(overlays, m.textOverlay)
	}
	return overlays
}

func (m *home) handleQuit() (tea.Model, tea.Cmd) {
	if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
		return m, m.handleError(err)
	}
	return m, tea.Quit
}

func (m *home) handleMenuHighlighting(msg tea.KeyMsg) (cmd tea.Cmd, returnEarly bool) {
	// Handle menu highlighting when you press a button. We intercept it here and immediately return to
	// update the ui while re-sending the keypress. Then, on the next call to this, we actually handle the keypress.
	if m.keySent {
		m.keySent = false
		return nil, false
	}
	// While an overlay is up the keys belong to it, so the menu must not light them up as
	// if they were the list's.
	if _, showsOverlay := m.currentOverlay(); showsOverlay {
		return nil, false
	}
	// If it's in the global keymap, we should try to highlight it.
	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return nil, false
	}

	if m.list.GetSelectedInstance() != nil && m.list.GetSelectedInstance().Paused() && name == keys.KeyEnter {
		return nil, false
	}
	if name == keys.KeyShiftDown || name == keys.KeyShiftUp {
		return nil, false
	}

	// Skip the menu highlighting if the key is not in the map or we are using the shift up and down keys.
	// TODO: cleanup: when you press enter on the title step, we use keys.KeySubmitName. We should unify the keymap.
	if name == keys.KeyEnter && m.state == stateNewTitle {
		name = keys.KeySubmitName
	}
	m.keySent = true
	return tea.Batch(
		func() tea.Msg { return msg },
		m.keydownCallback(name)), true
}

func (m *home) handleKeyPress(msg tea.KeyMsg) (mod tea.Model, cmd tea.Cmd) {
	cmd, returnEarly := m.handleMenuHighlighting(msg)
	if returnEarly {
		return m, cmd
	}

	if m.state == stateHelp {
		return m.handleHelpState(msg)
	}

	// The repository step is abandoned on its own: it runs before there is a session, so
	// there is nothing for abandonNewSession to discard.
	if m.state == stateNewRepo {
		if msg.String() == "ctrl+c" || msg.Type == tea.KeyEsc {
			return m, m.abandonRepoStep()
		}
		return m.handleRepoStep(msg)
	}

	if m.state == stateNewTitle || m.state == stateNewBranch || m.state == stateNewProgram {
		// Abandoning is the same at every step. Don't handle q: the user might be
		// typing it.
		if msg.String() == "ctrl+c" || msg.Type == tea.KeyEsc {
			return m, m.abandonNewSession()
		}
		switch m.state {
		case stateNewTitle:
			return m.handleTitleStep(msg)
		case stateNewBranch:
			return m.handleBranchStep(msg)
		case stateNewProgram:
			return m.handleProgramStep(msg)
		}
	}

	// Handle confirmation state
	if m.state == stateConfirm {
		shouldClose := m.confirmationOverlay.HandleKeyPress(msg)
		if shouldClose {
			m.state = stateDefault
			m.confirmationOverlay = nil
			return m, nil
		}
		return m, nil
	}

	// Exit scrolling mode when ESC is pressed and preview pane is in scrolling mode
	// Check if Escape key was pressed and we're not in the diff tab (meaning we're in preview tab)
	// Always check for escape key first to ensure it doesn't get intercepted elsewhere
	if msg.Type == tea.KeyEsc {
		// If in preview tab and in scroll mode, exit scroll mode
		if m.tabbedWindow.IsInPreviewTab() && m.tabbedWindow.IsPreviewInScrollMode() {
			// Use the selected instance from the list
			selected := m.list.GetSelectedInstance()
			err := m.tabbedWindow.ResetPreviewToNormalMode(selected)
			if err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		// If in terminal tab and in scroll mode, exit scroll mode
		if m.tabbedWindow.IsInTerminalTab() && m.tabbedWindow.IsTerminalInScrollMode() {
			m.tabbedWindow.ResetTerminalToNormalMode()
			return m, m.instanceChanged()
		}
	}

	// Handle quit commands first
	if msg.String() == "ctrl+c" || msg.String() == "q" {
		return m.handleQuit()
	}

	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return m, nil
	}

	switch name {
	case keys.KeyHelp:
		return m.showHelpScreen(helpTypeGeneral{}, nil)
	case keys.KeyNew:
		if m.list.NumInstances() >= GlobalInstanceLimit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
		}
		// Creation opens on the repository step: the session is built once it says where.
		return m.enterRepoStep()
	case keys.KeyUp:
		m.list.Up()
		return m, m.instanceChanged()
	case keys.KeyDown:
		m.list.Down()
		return m, m.instanceChanged()
	case keys.KeyShiftUp:
		m.tabbedWindow.ScrollUp()
		return m, m.instanceChanged()
	case keys.KeyShiftDown:
		m.tabbedWindow.ScrollDown()
		return m, m.instanceChanged()
	case keys.KeyTab:
		m.tabbedWindow.Toggle()
		m.menu.SetActiveTab(m.tabbedWindow.GetActiveTab())
		return m, m.instanceChanged()
	case keys.KeyKill:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}

		// What the kill would do is settled before anything is destroyed. It decides both
		// whether the kill may happen and what the confirmation promises, so a session
		// holding unfinished work is turned away rather than asked about.
		outcome := selected.KillOutcome()
		if err := killRefusal(selected.Title, outcome); err != nil {
			return m, m.handleError(err)
		}

		// Create the kill action as a tea.Cmd
		killAction := func() tea.Msg {
			// Clean up terminal session for this instance
			m.tabbedWindow.CleanupTerminalForInstance(selected.Title)

			// Delete from storage first
			if err := m.storage.DeleteInstance(selected.Title); err != nil {
				return err
			}

			// Then kill the instance
			m.list.Kill()
			return instanceChangedMsg{}
		}

		// Show confirmation modal
		return m, m.confirmAction(killConfirmation(selected.Title, outcome), killAction)
	case keys.KeySubmit:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}

		// Create the push action as a tea.Cmd
		pushAction := func() tea.Msg {
			// Default commit message with timestamp
			commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s", selected.Title, time.Now().Format(time.RFC822))
			worktree, err := selected.GetGitWorktree()
			if err != nil {
				return err
			}
			if err = worktree.PushChanges(commitMsg, true); err != nil {
				return err
			}
			return nil
		}

		// Show confirmation modal
		message := fmt.Sprintf("[!] Push changes from session '%s'?", selected.Title)
		return m, m.confirmAction(message, pushAction)
	case keys.KeyCheckout:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}

		// Show help screen before pausing
		m.showHelpScreen(helpTypeInstanceCheckout{}, func() {
			if err := selected.Pause(); err != nil {
				m.handleError(err)
			}
			m.tabbedWindow.CleanupTerminalForInstance(selected.Title)
			m.instanceChanged()
		})
		return m, nil
	case keys.KeyMoveUp:
		if m.list.MoveUp() {
			if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		return m, nil
	case keys.KeyMoveDown:
		if m.list.MoveDown() {
			if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		return m, nil
	case keys.KeyResume:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		if err := selected.Resume(); err != nil {
			return m, m.handleError(err)
		}
		return m, tea.WindowSize()
	case keys.KeyEnter:
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Paused() || selected.Status == session.Loading || !selected.TmuxAlive() {
			return m, nil
		}
		// The tab says which side of the session the user is asking for. From there on the
		// attachment is its own place, with its own way of moving between the two sides.
		side := attachSideFor(m.tabbedWindow.GetActiveTab())
		// The loop runs inside the callback, which is here when the help screen has been
		// seen and inside the dismissal when it has not. Its commands - hiding an error
		// box after its three seconds, refreshing the panes - are only reachable in the
		// first case, which is every attachment after the first.
		var attached tea.Cmd
		m.showHelpScreen(helpTypeInstanceAttach{}, func() {
			attached = m.attach(selected, side)
		})
		// Being attached stretches the session's tmux to the size of the whole terminal,
		// and nothing shrinks it back to the size of the pane it is about to be drawn in
		// again. Ask for the size afresh so the panes are not left rendering too wide
		// until the window happens to change.
		return m, tea.Batch(tea.WindowSize(), attached)
	default:
		return m, nil
	}
}

// instanceChanged updates the preview pane, menu, and diff pane based on the selected instance. It returns an error
// Cmd if there was any error.
func (m *home) instanceChanged() tea.Cmd {
	// selected may be nil
	selected := m.list.GetSelectedInstance()

	m.tabbedWindow.UpdateDiff(selected)
	m.tabbedWindow.SetInstance(selected)
	// Update menu with current instance
	m.menu.SetInstance(selected)

	// If there's no selected instance, we don't need to update the preview.
	if err := m.tabbedWindow.UpdatePreview(selected); err != nil {
		return m.handleError(err)
	}
	if err := m.tabbedWindow.UpdateTerminal(selected); err != nil {
		return m.handleError(err)
	}
	return nil
}

type keyupMsg struct{}

// keydownCallback clears the menu option highlighting after 500ms.
func (m *home) keydownCallback(name keys.KeyName) tea.Cmd {
	m.menu.Keydown(name)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}

		return keyupMsg{}
	}
}

// hideErrMsg implements tea.Msg and clears the error text from the screen.
type hideErrMsg struct{}

// previewTickMsg implements tea.Msg and triggers a preview update
type previewTickMsg struct{}

type instanceChangedMsg struct{}

type instanceStartedMsg struct {
	instance *session.Instance
	err      error
}

// instanceMetaResult holds the results of a single instance's metadata update,
// computed in a background goroutine.
type instanceMetaResult struct {
	instance  *session.Instance
	updated   bool
	hasPrompt bool
	diffStats *git.DiffStats
}

// metadataUpdateDoneMsg is sent when the background metadata update completes.
type metadataUpdateDoneMsg struct {
	results []instanceMetaResult
}

// snapshotActiveInstances returns the currently active (started, not paused)
// instances. Called on the main thread so the filtering doesn't race with
// state mutations.
func (m *home) snapshotActiveInstances() []*session.Instance {
	var out []*session.Instance
	for _, inst := range m.list.GetInstances() {
		if inst.Started() && !inst.Paused() {
			out = append(out, inst)
		}
	}
	return out
}

// tickUpdateMetadataCmd returns a self-chaining Cmd that sleeps 500ms, then performs
// expensive metadata I/O (tmux capture, git diff) in parallel background goroutines.
// Because it only re-schedules after completing, overlapping ticks are impossible.
// The active instances slice should be snapshotted on the main thread via
// snapshotActiveInstances() before being passed here.
//
// Only the selected instance gets a full diff (with Content); the rest get a
// lightweight numstat-only summary. This keeps per-instance memory bounded
// since the diff pane only ever renders the selected one.
func tickUpdateMetadataCmd(active []*session.Instance, selected *session.Instance) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(500 * time.Millisecond)

		if len(active) == 0 {
			return metadataUpdateDoneMsg{}
		}

		results := make([]instanceMetaResult, len(active))
		var wg sync.WaitGroup
		for idx, inst := range active {
			wg.Add(1)
			go func(i int, instance *session.Instance) {
				defer wg.Done()
				r := &results[i]
				r.instance = instance
				r.updated, r.hasPrompt = instance.HasUpdated()
				if instance == selected {
					r.diffStats = instance.ComputeDiff()
				} else {
					r.diffStats = instance.ComputeDiffNumstat()
				}
			}(idx, inst)
		}
		wg.Wait()

		return metadataUpdateDoneMsg{results: results}
	}
}

// handleError handles all errors which get bubbled up to the app. sets the error message. We return a callback tea.Cmd that returns a hideErrMsg message
// which clears the error message after 3 seconds.
func (m *home) handleError(err error) tea.Cmd {
	log.ErrorLog.Printf("%v", err)
	m.errBox.SetError(err)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(3 * time.Second):
		}

		return hideErrMsg{}
	}
}

// confirmAction shows a confirmation modal and stores the action to execute on confirm
func (m *home) confirmAction(message string, action tea.Cmd) tea.Cmd {
	m.state = stateConfirm

	// Create and show the confirmation overlay using ConfirmationOverlay
	m.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	// Set a fixed width for consistent appearance
	m.confirmationOverlay.SetWidth(50)

	// Set callbacks for confirmation and cancellation
	m.confirmationOverlay.OnConfirm = func() {
		m.state = stateDefault
		// Execute the action if it exists
		if action != nil {
			_ = action()
		}
	}

	m.confirmationOverlay.OnCancel = func() {
		m.state = stateDefault
	}

	return nil
}

func (m *home) View() string {
	listWithPadding := lipgloss.NewStyle().PaddingTop(1).Render(m.list.String())
	previewWithPadding := lipgloss.NewStyle().PaddingTop(1).Render(m.tabbedWindow.String())
	listAndPreview := lipgloss.JoinHorizontal(lipgloss.Top, listWithPadding, previewWithPadding)

	mainView := lipgloss.JoinVertical(
		lipgloss.Center,
		listAndPreview,
		m.menu.String(),
		m.errBox.String(),
	)

	if render, showsOverlay := m.currentOverlay(); showsOverlay {
		// A state that shows an overlay and has none left to show is a bug in whatever
		// entered it. Report it and draw what is behind, rather than rendering nothing.
		if render == nil {
			log.ErrorLog.Printf("no overlay for state %d", m.state)
			return mainView
		}
		return overlay.PlaceOverlay(0, 0, render(), mainView, true, true)
	}

	return mainView
}
