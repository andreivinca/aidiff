// Package splitview puts live terminal panes to the right of another Bubble
// Tea model. The wrapped model believes the window is only as wide as the left
// side, so its own layout, mouse hit-testing and key handling stay untouched.
//
// Terminals open as full-height columns to the right of the wrapped model,
// each new one to the right of the last, sharing the terminal area. A
// terminal whose program exits (e.g. the user types `exit`) is removed; with
// none left the wrapped model gets the full width again. The palette key
// (Config.Keys) opens a command palette with "Open Terminal" and friends.
//
// Focus moves with a mouse click or with the directional focus keys. While a
// terminal has focus every key goes to it, except the global keys.
//
// Run the program with QuitGuard, so that no quit, whatever command issues it,
// closes running panes without the user's confirmation.
package splitview

import (
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// focusDiff is the Model.focus value when the wrapped model has focus.
const focusDiff = -1

// Model is the root Bubble Tea model.
type Model struct {
	inner  tea.Model
	cfg    Config
	styles styles

	terms []Terminal
	focus int // focusOuter, focusSidebar, focusDiff, or an index into terms

	palette       palette
	quitDialog    bool          // the quit dialog is open
	quitSel       int           // selected quit-dialog button: confirmButton or cancelButton
	quitConfirmed bool          // nothing stands in the way of quitting; QuitGuard lets tea.QuitMsg through
	confirm       *confirmState // the open confirmation popup (ConfirmMsg), nil when none
	switcher      switcherState // the session switcher (switcher.go)
	keys          *keyReports   // the terminal's keyboard mode as the split set it; shared across copies
	flash         string        // one-line message shown until the next key

	winW          int       // the window's width
	width, height int       // width: the columns right of the sidebar, for the wrapped model and the panes
	leftW         int       // columns given to the wrapped model
	termRatio     float64   // terminal area share of the width; changed by dragging
	weights       []float64 // each terminal's share of the terminal area, parallel to terms
	dragging      bool      // a divider is being dragged with the mouse
	dragSep       int       // which: dragMain, or k for the separator right of terminal k
	dragX         int       // where the pointer took the divider, in the columns right of the sidebars
	dragMoved     bool      // the pointer moved it: the divider is drawn there, the panes resize on release
	innerW        int       // size last sent to the wrapped model
	innerH        int
	opened        bool // OpenOnStart has been honored

	refreshPending bool // files changed; the wrapped model has not refreshed yet
	innerSized     bool // the wrapped model has received a size (0x0 is a valid one)

	leftCache *leftCache      // shared across copies; see leftBlock
	panes     *paneCache      // shared across copies; see fitted
	closing   *sync.WaitGroup // panes being closed; shared across copies so Close can wait for them

	pending []StartFunc // panes requested before the window had a size

	wsKey  string               // the workspace on screen; "" is unnamed, closed when left
	parked map[string]workspace // the named workspaces not on screen; shared across copies, cloned on write
	wsID   int                  // the workspace on screen's wrapped model, as innerMsg names it
	mru    []string             // the workspaces, most recently used first; replaced, never written in place
	tabs   []string             // the named workspaces in the order they were opened (tabs.go); replaced, never written in place
	top    int                  // rows the tab strip takes at the top of the window, as of the last relayout
	strip  *tabCache            // the strip's last layout; shared across copies, see tabStrip

	sideOpen   bool // the sidebar is shown
	sideReturn int  // the focus before the sidebar was shown, given back when it hides

	outerOpen   bool // the outer sidebar is shown
	outerReturn int  // the focus before the outer sidebar was shown
}

// New wraps inner.
func New(inner tea.Model, cfg Config) Model {
	cfg = cfg.withDefaults()
	m := Model{
		inner:     inner,
		cfg:       cfg,
		styles:    newStyles(cfg.Colors),
		focus:     focusDiff,
		termRatio: float64(cfg.WidthPercent) / 100,
		leftCache: &leftCache{},
		panes:     &paneCache{},
		closing:   &sync.WaitGroup{},
		wsKey:     cfg.Workspace,
		wsID:      nextWorkspaceID(),
		mru:       []string{cfg.Workspace},
		tabs:      opened(nil, cfg.Workspace),
		strip:     &tabCache{},
		keys:      newKeyReports(cfg.Output),
	}
	return m.withTabs(cfg)
}

// Inner returns the wrapped model, e.g. to read its final state after Run.
func (m Model) Inner() tea.Model {
	return m.inner
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	if isUnopened(m.inner) && m.cfg.Sessions != nil { // Config.Opening: it opens now
		return m.scoped(m.cfg.Sessions.Open(m.wsKey))
	}
	return m.tag(m.inner.Init())
}

// Update implements tea.Model. The terminal reports key releases while the
// switcher is open waiting for one, and only then (keyreports.go), whatever
// opened or closed it.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.updateAll(msg)
	next := model.(Model)
	next.keys.follow(next.switcher.awaitsRelease())
	return next, cmd
}

// updateAll is Update before the terminal's keyboard mode follows.
func (m Model) updateAll(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(FilesChangedMsg); ok {
		m.refreshPending = m.cfg.RefreshMsg != nil
		return m.flushRefresh(nil)
	}
	model, cmd := m.update(msg)
	next := model.(Model)
	if next.switcher.open && (next.palette.open || next.quitDialog || next.confirm != nil) {
		// another popup came up: the switcher gives way to it
		closed, closeCmd := next.closeSwitcher()
		next, cmd = closed.(Model), tea.Batch(cmd, closeCmd)
	}
	if next.modal() {
		next.dragging = false // a popup takes the mouse: the drag's release would never come
	}
	model, cmd = next.closeUnfocused(cmd)
	// any message may have ended what kept the wrapped model busy
	return model.(Model).flushRefresh(cmd)
}

// flushRefresh forwards a held refresh once the wrapped model can take it.
func (m Model) flushRefresh(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if !m.refreshPending {
		return m, cmd
	}
	if r, ok := m.inner.(refreshGate); ok && !r.CanRefresh() {
		return m, cmd
	}
	m.refreshPending = false
	model, refreshCmd := m.updateInner(m.cfg.RefreshMsg)
	return model, tea.Batch(cmd, refreshCmd)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if seq, ok := rawSequence(msg); ok {
		if flags, ok := keyFlagsReply(seq); ok {
			m.keys.answered(flags) // the terminal's answer, never typed into a pane
			return m, nil
		}
		if r, ok := parseKeyReport(seq); ok {
			return m.handleKeyReport(r, seq, msg)
		}
		return m.forwardRaw(seq, msg)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.resize(msg)
	case OpenPaneMsg:
		return m.requestPane(msg.Start)
	case OpenTerminalMsg:
		return m.requestPane(m.cfg.Start)
	case termUpdateMsg:
		return m.handleTermUpdate(msg)
	case quitRequestMsg:
		return m.requestQuit()
	case ForceQuitMsg:
		return m.quit()
	case ShowPickerMsg:
		return m.showPicker(msg)
	case UpdatePickerItemMsg:
		return m.updatePickerItem(msg), nil
	case ShowPromptMsg:
		return m.showPrompt(msg)
	case ConfirmMsg:
		return m.showConfirm(msg)
	case FilesMsg:
		return m.showFiles(msg), nil
	case FlashMsg:
		m.flash = msg.Text
		return m, nil
	case RunCommandMsg:
		return m.runCommandNamed(msg)
	case RunMsg:
		if msg.Run == nil {
			return m, nil
		}
		return m, m.scoped(msg.Run(m.inner))
	case ShowSidebarMsg:
		return m.showSidebar()
	case HideSidebarMsg:
		return m.hideSidebar()
	case ReplaceInnerMsg:
		return m.replaceInner(msg)
	case SwitchWorkspaceMsg:
		return m.switchWorkspace(msg)
	case closeTabMsg:
		return m.closeTab(msg.key)
	case DropTabMsg:
		return m.dropTab(msg.Key)
	case innerMsg:
		return m.routeInner(msg)
	case scopedMsg:
		return m.routeScoped(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m.updateInner(msg)
}

// resize takes the new window size, opening the panes that waited for one.
func (m Model) resize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.winW, m.height = msg.Width, msg.Height
	m.width = m.winW - m.leftCols()
	if m.cfg.OpenOnStart && !m.opened {
		m.opened = true
		m.pending = append([]StartFunc{m.cfg.Start}, m.pending...)
	}
	if len(m.pending) == 0 {
		return m.relayout()
	}
	return m.openPending()
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.flash = ""
	if m.quitDialog {
		return m.handleQuitDialogKey(msg)
	}
	if m.confirm != nil {
		return m.handleConfirmKey(msg)
	}
	if m.palette.open {
		return m.handlePaletteKey(msg)
	}
	if m.switcher.open {
		return m.handleSwitcherKey(msg)
	}
	switch k := msg.String(); {
	case m.isSwitchKey(k):
		return m.openSwitcher(1, heldIn(k))
	case k == m.cfg.Keys.SwitchBack:
		return m.openSwitcher(-1, heldIn(k))
	}
	switch k := msg.String(); {
	case k == m.cfg.Keys.Focus || (k != "" && k == m.cfg.Keys.FocusAlt):
		return m.focusRight()
	case k == m.cfg.Keys.FocusLeft || (k != "" && k == m.cfg.Keys.FocusLeftAlt):
		return m.focusLeft()
	}
	switch msg.String() {
	case m.cfg.Keys.Quit:
		return m.askQuit()
	case m.cfg.Keys.Palette, m.cfg.Keys.PaletteAlt:
		return m.openPalette(), nil
	case m.cfg.Keys.CloseTab, shifted(m.cfg.Keys.CloseTab):
		return m.requestCloseTab(m.wsKey)
	}
	if n, ok := altNumber(msg); ok {
		return m.selectTab(n) // a focused sidebar takes the plain digits for its rows
	}
	if m.sidebarAvailable() {
		if k := msg.String(); k == m.cfg.Keys.Sidebar || k == m.cfg.Keys.SidebarAlt {
			return m.toggleSidebar()
		}
	}
	if m.focus == focusOuter {
		return m.outerKey(msg)
	}
	if m.focus == focusSidebar {
		return m.sidebarKey(msg)
	}
	if m.focus == focusDiff {
		return m.updateInner(msg)
	}
	sendKey(m.terms[m.focus], msg)
	return m, nil
}

// moveFocus moves between the sidebar, when shown, the review and panes,
// stopping at either end.
func (m Model) moveFocus(direction int) Model {
	lowest := focusDiff
	if m.sideOpen {
		lowest = focusSidebar
	}
	m.focus = max(lowest, min(m.focus+direction, len(m.terms)-1)) // focusSidebar is focusDiff-1
	return m
}

func (m Model) updateInner(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.inner, cmd = m.inner.Update(msg)
	cmd = m.tag(cmd)
	m.dropLeftCache()
	if m.palette.open {
		m = m.refilterPalette() // the commands may follow the wrapped model's state
	}
	return m, cmd
}

// replaceInner swaps the wrapped model. The newcomer is initialized and gets
// the size the old one had, so it starts loading at once; a pending refresh
// belonged to the old model and is dropped.
func (m Model) replaceInner(msg ReplaceInnerMsg) (tea.Model, tea.Cmd) {
	if msg.Model == nil {
		return m, nil
	}
	if msg.Retire != nil {
		msg.Retire(m.inner)
	}
	var carry tea.Cmd
	if m.cfg.Carry != nil {
		carry = m.cfg.Carry(m.inner, msg.Model)
	}
	m.inner = msg.Model
	m.wsID = nextWorkspaceID() // what the old model still had under way is dropped
	m.dropLeftCache()
	m.innerSized = false
	m.refreshPending = false
	m.flash = ""
	initCmd := m.tag(m.inner.Init())
	model, sizeCmd := m.relayout()
	return model, tea.Batch(initCmd, sizeCmd, m.tag(carry))
}
