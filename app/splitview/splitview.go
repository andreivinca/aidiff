// Package splitview puts live terminal panes to the right of another Bubble
// Tea model. The wrapped model believes the window is only as wide as the left
// side, so its own layout, mouse hit-testing and key handling stay untouched.
//
// Terminals open as full-height columns to the right of the diff, each new
// one to the right of the last, sharing the terminal area. A terminal whose
// program exits (e.g. the user types `exit`) is removed; with none left the
// wrapped model gets the full width again. ctrl+p opens a command palette with
// "Open Terminal" and friends.
//
// Focus moves with a mouse click or by cycling with ctrl+] (FocusKey). While a
// terminal has focus every key goes to it, except the few global keys below.
package splitview

import (
	"reflect"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// Global keys, handled before anything reaches the diff or a terminal.
const (
	FocusKey   = "ctrl+]" // cycle focus: diff → terminal 1 → … → diff
	QuitKey    = "ctrl+q" // quit, asking first while terminals run
	PaletteKey = "ctrl+p" // open the command palette
)

const (
	defaultWidthPercent = 45
	minPaneWidth        = 20
	minTermWidth        = 10
	focusDiff           = -1 // Model.focus value when the wrapped model has focus
	dragMain            = -1 // Model.dragSep value for the diff/terminals divider
)

// Terminal is one running terminal shown in the right column.
type Terminal interface {
	Updates() <-chan struct{}
	Exited() bool
	SendText(s string)
	SendKey(k uv.KeyPressEvent)
	Paste(s string)
	SendMouse(m vt.Mouse)
	Resize(width, height int)
	Render(showCursor bool) string
	Close()
}

// Config configures the split.
type Config struct {
	// Start launches a terminal of the given content size.
	Start func(width, height int) (Terminal, error)
	// WidthPercent is the right column's share of the window (10-90).
	WidthPercent int
	// OpenOnStart opens one terminal as soon as the window size is known.
	OpenOnStart bool
	// RefreshMsg is forwarded to the wrapped model when files change (see
	// FilesChangedMsg). Nil disables refreshing.
	RefreshMsg tea.Msg
	// Commands adds entries to the ctrl+p palette. It is asked again each
	// time the palette renders, so the list can follow the program's state.
	Commands func() []Command
}

// FilesChangedMsg tells the split that files changed on disk. It forwards
// Config.RefreshMsg to the wrapped model, holding it while the model reports
// (through an optional CanRefresh() bool method) that it is busy.
type FilesChangedMsg struct{}

// OpenPaneMsg adds a pane to the right column the way Open Terminal adds a
// shell. Start receives the pane's content size. Until the window size is
// known the request waits for it.
type OpenPaneMsg struct {
	Start func(width, height int) (Terminal, error)
}

// QuitNoted is a pane that tells the quit dialog what closing it means, in
// place of the shell's "everything running in it" warning.
type QuitNoted interface{ QuitNote() string }

// Model is the root Bubble Tea model.
type Model struct {
	inner tea.Model
	cfg   Config

	terms []Terminal
	focus int // focusDiff, or an index into terms

	palette     palette
	confirmQuit bool   // the quit dialog is open (terminals are still running)
	quitSel     int    // selected quit-dialog button: quitButton or cancelButton
	flash       string // one-line message shown until the next key

	width, height int
	leftW         int       // columns given to the wrapped model
	termRatio     float64   // terminal area share of the width; changed by dragging
	weights       []float64 // each terminal's share of the terminal area, parallel to terms
	dragging      bool      // a divider is being dragged with the mouse
	dragSep       int       // which: dragMain, or k for the separator right of terminal k
	dragOffset    int       // distance from the actual border when grabbing its adjacent cell
	innerW        int       // size last sent to the wrapped model
	innerH        int
	opened        bool // OpenOnStart has been honored

	refreshPending bool // files changed; the wrapped model has not refreshed yet
	innerSized     bool // the wrapped model has received a size (0x0 is a valid one)

	leftCache *leftCache // shared across copies; see leftBlock

	pending []func(int, int) (Terminal, error) // panes requested before the window had a size
}

// leftCache holds the wrapped model's fitted view. It is dropped whenever the
// wrapped model receives a message, so a terminal echo reuses it.
type leftCache struct {
	valid bool
	w, h  int
	lines []string
}

// termUpdateMsg reports that a terminal's screen changed or its program ended.
type termUpdateMsg struct{ term Terminal }

// quitRequestMsg replaces the wrapped model's tea.QuitMsg so the user can
// confirm before running terminals (and whatever runs in them) are killed.
type quitRequestMsg struct{}

// New wraps inner.
func New(inner tea.Model, cfg Config) Model {
	if cfg.WidthPercent < 10 || cfg.WidthPercent > 90 {
		cfg.WidthPercent = defaultWidthPercent
	}
	return Model{inner: inner, cfg: cfg, focus: focusDiff, termRatio: float64(cfg.WidthPercent) / 100, leftCache: &leftCache{}}
}

// Inner returns the wrapped model, e.g. to read its final state after Run.
func (m Model) Inner() tea.Model { return m.inner }

// Close stops every terminal. Safe to call more than once.
func (m Model) Close() {
	for _, t := range m.terms {
		t.Close()
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return m.guardQuit(m.inner.Init()) }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(FilesChangedMsg); ok {
		m.refreshPending = m.cfg.RefreshMsg != nil
		return m.flushRefresh(nil)
	}
	model, cmd := m.update(msg)
	// any message may have ended what kept the wrapped model busy
	return model.(Model).flushRefresh(cmd)
}

// flushRefresh forwards a held refresh once the wrapped model can take it.
func (m Model) flushRefresh(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if !m.refreshPending {
		return m, cmd
	}
	if r, ok := m.inner.(interface{ CanRefresh() bool }); ok && !r.CanRefresh() {
		return m, cmd
	}
	m.refreshPending = false
	model, refreshCmd := m.updateInner(m.cfg.RefreshMsg)
	return model, tea.Batch(cmd, refreshCmd)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if seq, ok := rawSequence(msg); ok {
		return m.forwardRaw(seq, msg)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.cfg.OpenOnStart && !m.opened {
			m.opened = true
			m.pending = append([]func(int, int) (Terminal, error){m.cfg.Start}, m.pending...)
		}
		if len(m.pending) == 0 {
			return m.relayout()
		}
		return m.openPending()
	case OpenPaneMsg:
		if msg.Start == nil {
			return m, nil
		}
		if m.width == 0 {
			m.pending = append(m.pending, msg.Start)
			return m, nil
		}
		return m.openPane(msg.Start)
	case termUpdateMsg:
		return m.handleTermUpdate(msg)
	case quitRequestMsg:
		if !m.anyRunning() {
			return m, tea.Quit
		}
		m.confirmQuit, m.quitSel = true, cancelButton
		return m, nil
	case ShowPickerMsg:
		m.flash = ""
		m.palette = palette{open: true, mode: modePick, title: msg.Title, items: msg.Items,
			onPick: msg.OnPick, onText: msg.OnText, textLabel: msg.TextLabel}
		if m.palette.textLabel == "" {
			m.palette.textLabel = "%s"
		}
		return m, nil
	case ShowPromptMsg:
		m.flash = ""
		m.palette = palette{open: true, mode: modePrompt, title: msg.Title, placeholder: msg.Placeholder, onText: msg.OnSubmit}
		return m, nil
	case FlashMsg:
		m.flash = msg.Text
		return m, nil
	case ReplaceInnerMsg:
		return m.replaceInner(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m.updateInner(msg)
}

// forwardRaw hands an unnamed escape sequence to the focused pane, or to the
// wrapped model when the diff has focus or a popup is open.
func (m Model) forwardRaw(seq []byte, msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.focus == focusDiff || m.palette.open || m.confirmQuit {
		return m.updateInner(msg)
	}
	m.terms[m.focus].SendText(string(seq))
	return m, nil
}

// rawSequence recovers an escape sequence Bubble Tea could not name, such as
// the CSI u form of shift+enter ("\x1b[13;2u"), so the focused pane still
// gets it. Bubble Tea reports those as an unexported byte-slice message.
func rawSequence(msg tea.Msg) ([]byte, bool) {
	v := reflect.ValueOf(msg)
	if !v.IsValid() || v.Kind() != reflect.Slice || v.Type().Elem().Kind() != reflect.Uint8 {
		return nil, false
	}
	if !strings.HasSuffix(v.Type().String(), "unknownCSISequenceMsg") {
		return nil, false
	}
	return v.Bytes(), true
}

// askQuit opens the quit dialog whatever is open: ctrl+q is deliberate, and
// a confirmation costs one key. A quit the wrapped model asks for (its own
// quit key) still goes straight through when no pane is open.
func (m Model) askQuit() (tea.Model, tea.Cmd) {
	m.confirmQuit, m.quitSel = true, cancelButton
	return m, nil
}

func (m Model) updateInner(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.inner, cmd = m.inner.Update(msg)
	m.dropLeftCache()
	return m, m.guardQuit(cmd)
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
	m.inner = msg.Model
	m.dropLeftCache()
	m.innerSized = false
	m.refreshPending = false
	m.flash = ""
	initCmd := m.guardQuit(m.inner.Init())
	model, sizeCmd := m.relayout()
	return model, tea.Batch(initCmd, sizeCmd)
}

func (m Model) anyRunning() bool {
	return slices.ContainsFunc(m.terms, needsConfirm)
}

// needsConfirm reports whether quitting would close t: any pane still open
// asks first, so a stray ctrl+q never drops a shell or a chat.
func needsConfirm(t Terminal) bool {
	return !t.Exited()
}

func (m Model) indexOf(t Terminal) int {
	for i, x := range m.terms {
		if x == t {
			return i
		}
	}
	return -1
}

func (m Model) handleTermUpdate(msg termUpdateMsg) (tea.Model, tea.Cmd) {
	i := m.indexOf(msg.term)
	if i < 0 {
		return m, nil // already closed
	}
	if msg.term.Exited() {
		return m.closeTerminal(i)
	}
	return m, waitForUpdate(msg.term)
}

// openTerminal adds a shell column at the far right and focuses it.
func (m Model) openTerminal() (tea.Model, tea.Cmd) {
	return m.openPane(m.cfg.Start)
}

// openPending opens the panes requested before the window had a size.
func (m Model) openPending() (tea.Model, tea.Cmd) {
	pending := m.pending
	m.pending = nil
	cmds := make([]tea.Cmd, 0, len(pending))
	for _, start := range pending {
		model, cmd := m.openPane(start)
		m = model.(Model)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// openPane adds a pane column at the far right and focuses it.
func (m Model) openPane(start func(width, height int) (Terminal, error)) (tea.Model, tea.Cmd) {
	if m.width == 0 || start == nil {
		return m, nil
	}
	oldTerms, oldWeights := m.terms, m.weights
	restore := func() Model {
		m.terms, m.weights = oldTerms, oldWeights
		m.leftW = m.leftWidth()
		return m
	}
	// the new terminal gets an equal share; the others shrink proportionally,
	// keeping the sizes the user dragged them to.
	n := len(m.terms) + 1
	m.weights = make([]float64, 0, n)
	for _, w := range m.normWeights() {
		m.weights = append(m.weights, w*float64(n-1)/float64(n))
	}
	m.weights = append(m.weights, 1/float64(n))
	m.terms = append(slices.Clip(m.terms), nil) // reserve the slot so the layout sizes it
	m.leftW = m.leftWidth()
	rects := m.termRects()
	for _, r := range rects {
		if r.w < minTermWidth {
			m = restore()
			m.flash = "No room for another terminal (drag the divider left to make space)"
			return m.relayout()
		}
	}
	r := rects[len(rects)-1]
	t, err := start(r.w, r.h)
	if err != nil {
		m = restore()
		m.flash = "Terminal failed to start: " + err.Error()
		return m.relayout()
	}
	m.terms[len(m.terms)-1] = t
	m.focus = len(m.terms) - 1
	model, cmd := m.relayout()
	return model, tea.Batch(cmd, waitForUpdate(t))
}

// closeTerminal stops and removes terminal i; with none left the diff gets
// the full width again.
func (m Model) closeTerminal(i int) (tea.Model, tea.Cmd) {
	t := m.terms[i]
	weights := m.normWeights() // before removing, while it matches m.terms
	m.terms = append(m.terms[:i:i], m.terms[i+1:]...)
	m.weights = slices.Delete(slices.Clone(weights), i, i+1) // the others share its space proportionally
	m.weights = m.normWeights()
	go t.Close() // may wait up to a second for stubborn processes
	switch {
	case len(m.terms) == 0 || m.focus == focusDiff:
		if len(m.terms) == 0 {
			m.focus = focusDiff
		}
	case m.focus == i:
		m.focus = min(i, len(m.terms)-1)
	case m.focus > i:
		m.focus--
	}
	return m.relayout()
}

// leftWidth is the wrapped model's width for the current window and terminals.
func (m Model) leftWidth() int {
	if len(m.terms) == 0 {
		return m.width
	}
	if m.width < 2*minPaneWidth {
		return max(1, m.width/2)
	}
	termW := int(float64(m.width)*m.termRatio + 0.5)
	return min(max(m.width-termW, minPaneWidth), m.width-minPaneWidth)
}

// rect is a terminal's place in the terminal area; x is relative to leftW.
type rect struct{ x, w, h int }

// termRects lays the terminals out side by side across the terminal area,
// full height, with a one-column separator between neighbors.
func (m Model) termRects() []rect {
	n := len(m.terms)
	if n == 0 {
		return nil
	}
	total := max(1, m.width-m.leftW)
	avail := max(n, total-(n-1))
	h := max(1, m.height)

	weights := m.normWeights()
	widths := make([]int, n)
	used := 0
	for i := range n - 1 {
		widths[i] = max(1, int(float64(avail)*weights[i]+0.5))
		used += widths[i]
	}
	widths[n-1] = avail - used
	for widths[n-1] < 1 { // rounding overshot: take from the widest
		widest := 0
		for i := range n - 1 {
			if widths[i] > widths[widest] {
				widest = i
			}
		}
		widths[widest]--
		widths[n-1]++
	}

	rects := make([]rect, n)
	x := 0
	for i, w := range widths {
		rects[i] = rect{x: x, w: w, h: h}
		x += w + 1
	}
	return rects
}

// normWeights returns the terminal shares summing to 1, falling back to equal
// shares when they are missing or out of sync with the terminal list.
func (m Model) normWeights() []float64 {
	n := len(m.terms)
	out := make([]float64, n)
	sum := 0.0
	if len(m.weights) == n {
		for _, w := range m.weights {
			sum += w
		}
	}
	for i := range out {
		if sum > 0 {
			out[i] = m.weights[i] / sum
		} else {
			out[i] = 1 / float64(n)
		}
	}
	return out
}

// relayout recomputes the split and pushes sizes to the diff and terminals.
func (m Model) relayout() (tea.Model, tea.Cmd) {
	m.leftW = m.leftWidth()
	for i, r := range m.termRects() {
		if m.terms[i] != nil {
			m.terms[i].Resize(r.w, r.h)
		}
	}
	if m.innerSized && m.leftW == m.innerW && m.height == m.innerH {
		return m, nil // e.g. dragging between terminals: spare the diff a re-render
	}
	m.innerW, m.innerH, m.innerSized = m.leftW, m.height, true
	return m.updateInner(tea.WindowSizeMsg{Width: m.leftW, Height: m.height})
}

func waitForUpdate(t Terminal) tea.Cmd {
	return func() tea.Msg {
		<-t.Updates()
		return termUpdateMsg{term: t}
	}
}

// guardQuit routes the wrapped model's quit through quitRequestMsg, so quitting
// the diff view cannot silently kill running terminals.
func (m Model) guardQuit(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		switch msg := cmd().(type) {
		case tea.QuitMsg:
			return quitRequestMsg{}
		case tea.BatchMsg:
			wrapped := make(tea.BatchMsg, len(msg))
			for i, c := range msg {
				wrapped[i] = m.guardQuit(c)
			}
			return wrapped
		default:
			return msg
		}
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.flash = ""
	if m.confirmQuit {
		return m.handleQuitDialogKey(msg)
	}
	if m.palette.open {
		return m.handlePaletteKey(msg)
	}
	switch msg.String() {
	case FocusKey:
		if m.focus+1 < len(m.terms) {
			m.focus++
		} else {
			m.focus = focusDiff
		}
		return m, nil
	case QuitKey:
		return m.askQuit()
	case PaletteKey:
		m.palette = newPalette()
		return m, nil
	}
	if m.focus == focusDiff {
		return m.updateInner(msg)
	}
	sendKey(m.terms[m.focus], msg)
	return m, nil
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.palette.open || m.confirmQuit {
		return m, nil // modal: swallow
	}
	if m.dragging {
		return m.dragMouse(msg)
	}
	press := msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft
	rects := m.termRects()
	if m.routeCapturedMouse(msg, rects) {
		return m, nil
	}

	if press {
		if sep, ok := m.dividerAt(msg.X, rects); ok {
			m.dragging, m.dragSep = true, sep
			m.dragOffset = 0
			if sep == dragMain {
				m.dragOffset = msg.X - (m.leftW - 1)
			}
			return m, nil
		}
	}
	if msg.X < m.leftW {
		if press {
			m.focus = focusDiff
		}
		return m.updateInner(msg)
	}
	rel := msg.X - m.leftW
	for i, r := range rects {
		if rel < r.x || rel >= r.x+r.w {
			continue
		}
		if press {
			m.focus = i
		}
		if ev, ok := toTermMouse(msg, rel-r.x, msg.Y); ok {
			m.terms[i].SendMouse(ev)
		}
	}
	return m, nil // separator columns do nothing
}

// routeCapturedMouse keeps selection drags attached to their originating pane.
func (m Model) routeCapturedMouse(msg tea.MouseMsg, rects []rect) bool {
	if msg.Action != tea.MouseActionMotion && msg.Action != tea.MouseActionRelease {
		return false
	}
	for i, pane := range m.terms {
		if capture, ok := pane.(interface{ MouseCaptured() bool }); ok && capture.MouseCaptured() {
			if ev, ok := toTermMouse(msg, msg.X-m.leftW-rects[i].x, msg.Y); ok {
				pane.SendMouse(ev)
			}
			return true
		}
	}
	return false
}

// dragMouse follows a held pointer and commits its release position, even
// when a terminal coalesces the last motion event. Wheel events do not end it.
func (m Model) dragMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionMotion && msg.Action != tea.MouseActionRelease {
		return m, nil
	}
	if msg.Action == tea.MouseActionRelease {
		m.dragging = false
	}
	if m.dragSep == dragMain {
		return m.dragTo(msg.X - m.dragOffset)
	}
	return m.dragSepTo(m.dragSep, msg.X)
}

// dividerAt tells which divider column x is on: dragMain for the wrapped
// model's right border and the column after it, k for the separator right of
// terminal k.
func (m Model) dividerAt(x int, rects []rect) (int, bool) {
	if len(m.terms) > 0 && (x == m.leftW-1 || x == m.leftW) {
		return dragMain, true
	}
	for k := 0; x > m.leftW && k+1 < len(rects); k++ {
		if x-m.leftW == rects[k].x+rects[k].w {
			return k, true
		}
	}
	return 0, false
}

// dragTo moves the divider so the wrapped model's right border sits at column x.
func (m Model) dragTo(x int) (tea.Model, tea.Cmd) {
	if m.width < 2*minPaneWidth {
		return m, nil
	}
	leftW := min(max(x+1, minPaneWidth), m.width-minPaneWidth)
	if leftW == m.leftW {
		return m, nil
	}
	m.termRatio = float64(m.width-leftW) / float64(m.width)
	return m.relayout()
}

// dragSepTo moves the separator right of terminal k to column x, resizing
// only terminals k and k+1.
func (m Model) dragSepTo(k, x int) (tea.Model, tea.Cmd) {
	rects := m.termRects()
	if k+1 >= len(rects) {
		return m, nil
	}
	a, b := rects[k], rects[k+1]
	combined := a.w + b.w
	if combined < 2*minTermWidth {
		return m, nil
	}
	newA := min(max(x-m.leftW-a.x, minTermWidth), combined-minTermWidth)
	if newA == a.w {
		return m, nil
	}
	// store the new widths as exact shares so the layout reproduces them
	avail := 0
	for _, r := range rects {
		avail += r.w
	}
	m.weights = make([]float64, len(rects))
	for i, r := range rects {
		m.weights[i] = float64(r.w) / float64(avail)
	}
	m.weights[k] = float64(newA) / float64(avail)
	m.weights[k+1] = float64(combined-newA) / float64(avail)
	return m.relayout()
}

// View implements tea.Model.
func (m Model) View() string {
	if m.width == 0 {
		return m.inner.View()
	}
	view := m.columnView(m.leftBlock())
	if m.palette.open {
		view = overlayCenter(view, m.paletteView(), m.width, m.height)
	}
	switch {
	case m.confirmQuit:
		view = overlayCenter(view, m.quitDialogView(), m.width, m.height)
	case m.flash != "":
		view = replaceLastLine(view, " "+m.flash+" ", m.width)
	}
	return view
}

// leftBlock is the wrapped model's view fitted to its column, one line per
// row. It is cached until the wrapped model receives a message or the column
// changes size, so a terminal echo does not re-render the diff.
func (m Model) leftBlock() []string {
	c := m.leftCache
	if c == nil {
		c = &leftCache{} // a Model built without New caches nothing
	}
	if !c.valid || c.w != m.leftW || c.h != m.height {
		c.lines = strings.Split(fitBlock(m.inner.View(), m.leftW, m.height), "\n")
		c.valid, c.w, c.h = true, m.leftW, m.height
	}
	return c.lines
}

// dropLeftCache forgets the fitted view once the wrapped model has changed.
func (m Model) dropLeftCache() {
	if m.leftCache != nil {
		m.leftCache.valid = false
	}
}

// columnView renders the wrapped model's lines and the terminals side by
// side, with a vertical line between neighbors.
func (m Model) columnView(left []string) string {
	rects := m.termRects()
	blocks := make([][]string, len(rects))
	for i, r := range rects {
		showCursor := m.focus == i && !m.palette.open
		blocks[i] = strings.Split(fitBlock(m.terms[i].Render(showCursor), r.w, r.h), "\n")
	}
	rows := make([]string, m.height)
	for y := range rows {
		var b strings.Builder
		if len(rects) > 0 && y < m.height-1 {
			// Draw our own separator in the review's right margin; its
			// content no longer has a border. Preserve the status row.
			b.WriteString(ansi.Cut(left[y], 0, max(0, m.leftW-1)))
			b.WriteString(m.resizeGrip(y, dragMain))
		} else {
			b.WriteString(left[y])
		}
		for i := range blocks {
			if i > 0 {
				b.WriteString(m.resizeGrip(y, i-1))
			}
			b.WriteString(blocks[i][y])
		}
		rows[y] = b.String()
	}
	return strings.Join(rows, "\n")
}

// resizeGrip marks each wrapper-owned divider and highlights a grabbed one.
func (m Model) resizeGrip(y, divider int) string {
	glyph, color := "│", "8"
	if y == m.height/2 {
		glyph = "↔"
	}
	if m.dragging && m.dragSep == divider {
		color = "12"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(glyph)
}

func replaceLastLine(view, text string, width int) string {
	lines := strings.Split(view, "\n")
	lines[len(lines)-1] = lipgloss.NewStyle().Reverse(true).Width(width).MaxWidth(width).Render(text)
	return strings.Join(lines, "\n")
}
