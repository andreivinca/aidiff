package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/keymap"
	"github.com/umputun/revdiff/app/ui/style"
)

// aidiff's toolbar is a row of buttons under the diff header. A button
// either runs a tool, or opens a dropdown menu of them under it, each item
// with its key on the right, so the keys can be learnt from it. F10 (or
// alt+m) opens the menus from the keyboard: left and right move between
// buttons, up and down between items, enter picks, esc closes. The mouse
// clicks a button open and an item to pick it.
//
// The toolbar is the review's top row, across the tree and the diff. The
// review lays itself out in the rows below it: a window size reaches it one
// row shorter, and a mouse event one row higher (reserveToolbarRow,
// handleToolbarAndMouse), so nothing else needs to know about the toolbar.

// toolbarState is the toolbar's state.
type toolbarState struct {
	on      bool           // the row is wanted (ModelConfig.Toolbar)
	shown   bool           // the row is drawn: on, and the window has room for it
	compare func() tea.Cmd // what the Compare button runs; nil hides it
	// showFiles switches the review to every file of its folder, or back to
	// its changes; nil hides the View menu's rows for it
	showFiles func(files bool) tea.Cmd
	open      bool // a button is active: its menu shows, keys go to it
	button    int  // the active button, an index of toolbarButtons
	item      int  // the highlighted item of its menu
}

// tbButton is one button of the toolbar.
type tbButton struct {
	icon, label string
	key         string                             // the button's key, shown after it
	action      keymap.Action                      // the action opening a dropdown; "" for none
	menu        func(m Model) []tbItem             // a dropdown's items; nil for a plain button
	run         func(m Model) (tea.Model, tea.Cmd) // a plain button's tool
	// segment draws the button as its icon alone, one of a toggle's
	// segments beside each other; on marks the segment in effect
	segment, on bool
}

// tbItem is one item of a dropdown menu.
type tbItem struct {
	label, key string
	checked    bool
	sep        bool // a divider line, not an item
	disabled   bool
	indent     bool // a choice under the item above it
	keepOpen   bool // the menu stays open after it, for settings tried in a row
	run        func(m Model) (tea.Model, tea.Cmd)
}

// toolbarShown reports whether the toolbar row is drawn.
func (m Model) toolbarShown() bool {
	return m.toolbar.shown
}

// minToolbarHeight is the window height below which the toolbar gives its
// row back to the review.
const minToolbarHeight = 8

// reserveToolbarRow takes the toolbar's row from a window size, when the
// toolbar is on and the window has room for it.
func (m *Model) reserveToolbarRow(msg tea.WindowSizeMsg) tea.WindowSizeMsg {
	m.toolbar.shown = m.toolbar.on && msg.Height >= minToolbarHeight
	if m.toolbar.shown {
		msg.Height--
	}
	return msg
}

// View draws the toolbar over the review, and the open menu over both.
func (m Model) View() string {
	view := m.reviewView()
	if !m.toolbarShown() || !m.ready || !m.filesLoaded && !m.refreshLoading() {
		return view
	}
	return m.composeMenu(m.renderToolbar(m.layout.width) + "\n" + view)
}

// handleToolbarAndMouse gives a mouse event to the toolbar, then to the
// review one row higher, in its own coordinates.
func (m Model) handleToolbarAndMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.revert.asking = false // an open revert popup would have taken the event
	if m.divider.dragging {
		// a drag takes the mouse wherever the pointer is: a release over
		// the toolbar row, dropped below, would leave it stuck
		shifted := msg
		if m.toolbarShown() {
			shifted.Y--
		}
		model, cmd, handled := m.treeDividerMouse(shifted)
		if handled {
			return model, cmd
		}
		m = model // a press ended a drag whose release was lost: it goes on as any other
	}
	if !m.toolbarShown() {
		return m.handleMouse(msg)
	}
	busy := m.inConfirmDiscard || m.reload.pending || m.annot.annotating || m.search.active || m.overlay.Active()
	if !busy {
		if model, cmd, handled := m.handleToolbarMouse(msg); handled {
			return model, cmd
		}
	}
	if msg.Y == toolbarRow {
		return m, nil
	}
	msg.Y--
	return m.handleMouse(msg)
}

// toolbarButtons are the toolbar's buttons as the review allows them.
func (m Model) toolbarButtons() []tbButton {
	buttons := append(m.viewToggle(), tbButton{icon: "◧", label: "View", menu: Model.viewMenu, action: keymap.ActionViewMenu,
		key: m.toolbarKey(keymap.ActionViewMenu)})
	if m.compact.applicable {
		label := fmt.Sprintf("%d lines", m.modes.compactContext)
		if !m.modes.compact {
			label = "Whole files"
		}
		buttons = append(buttons, tbButton{icon: "↕", label: label, menu: Model.contextMenu, action: keymap.ActionContextMenu,
			key: m.toolbarKey(keymap.ActionContextMenu)})
	}
	if m.toolbar.compare != nil {
		compare := m.toolbar.compare
		buttons = append(buttons, tbButton{icon: "⇄", label: "Compare…", run: func(m Model) (tea.Model, tea.Cmd) {
			return m, compare()
		}})
	}
	if m.reload.applicable {
		buttons = append(buttons, m.actionButton("⟳", "Reload", keymap.ActionReload))
	}
	return append(buttons,
		m.actionButton("✎", "Notes", keymap.ActionAnnotList),
		m.actionButton("?", "Help", keymap.ActionHelp))
}

// actionButton is a plain button running action, its key shown after it.
func (m Model) actionButton(icon, label string, action keymap.Action) tbButton {
	return tbButton{icon: icon, label: label, key: m.toolbarKey(action), run: func(m Model) (tea.Model, tea.Cmd) {
		return m.dispatchAction(action)
	}}
}

// toggleItem is a menu item switching a view setting with its action.
func (m Model) toggleItem(label string, action keymap.Action, on bool) tbItem {
	return tbItem{label: label, key: m.toolbarKey(action), checked: on, keepOpen: true, run: func(m Model) (tea.Model, tea.Cmd) {
		return m.dispatchAction(action)
	}}
}

// viewToggle is the toggle at the toolbar's start between the review's
// changes (±) and every file of its folder (☰), none for a review that
// cannot switch.
func (m Model) viewToggle() []tbButton {
	if m.toolbar.showFiles == nil {
		return nil
	}
	files := m.browsingFolders()
	return []tbButton{
		{icon: "±", label: "Changes", segment: true, on: !files, run: switchView(false)},
		{icon: "☰", label: "Project files", segment: true, on: files, run: switchView(true)},
	}
}

// switchView shows the review's changes, or every file of its folder.
func switchView(toFiles bool) func(m Model) (tea.Model, tea.Cmd) {
	return func(m Model) (tea.Model, tea.Cmd) {
		if m.toolbar.showFiles == nil || m.browsingFolders() == toFiles {
			return m, nil
		}
		return m, m.toolbar.showFiles(toFiles)
	}
}

// viewMenu is the View dropdown: changes or project files, then the toggles.
// Whole files or changed blocks is the Compare tool's choice.
func (m Model) viewMenu() []tbItem {
	var items []tbItem
	if m.toolbar.showFiles != nil {
		files := m.browsingFolders()
		items = append(items,
			tbItem{label: "Changes", checked: !files, run: switchView(false)},
			tbItem{label: "Project files", checked: files, run: switchView(true)},
			tbItem{sep: true})
	}
	items = append(items,
		m.toggleItem("Line numbers", keymap.ActionToggleLineNums, m.modes.lineNumbers),
		m.toggleItem("Wrap lines", keymap.ActionToggleWrap, m.modes.wrap),
		m.toggleItem("Word diff", keymap.ActionToggleWordDiff, m.modes.wordDiff),
		m.toggleItem("Collapsed", keymap.ActionToggleCollapsed, m.modes.collapsed.enabled))
	if m.blamer != nil {
		items = append(items, m.toggleItem("Blame", keymap.ActionToggleBlame, m.modes.showBlame))
	}
	if m.loadUntracked != nil {
		items = append(items, m.toggleItem("Untracked files", keymap.ActionToggleUntracked, m.modes.showUntracked))
	}
	return append(items, m.toggleItem("File tree", keymap.ActionToggleTree, !m.layout.treeHidden))
}

// contextSizes are the context menu's sizes, lines around each change.
var contextSizes = []int{1, 2, 3, 5, 10, 20, 50}

// contextMenu is the context dropdown: whole files, or how many lines
// around each change, picking one leaves whole files for the changed blocks.
func (m Model) contextMenu() []tbItem {
	items := make([]tbItem, 0, len(contextSizes)+5)
	items = append(items, tbItem{label: "Whole files", checked: !m.modes.compact,
		run: func(m Model) (tea.Model, tea.Cmd) { return m.handleSetCompact(SetCompactMsg{}) }}, tbItem{sep: true})
	for _, n := range contextSizes {
		label := fmt.Sprintf("%d lines", n)
		if n == 1 {
			label = "1 line"
		}
		items = append(items, tbItem{label: label, checked: m.modes.compact && m.modes.compactContext == n,
			run: func(m Model) (tea.Model, tea.Cmd) {
				cmd := m.setCompactContext(n) // before m is read: the order within a return is unspecified
				return m, cmd
			}})
	}
	more := func(more bool) func(m Model) (tea.Model, tea.Cmd) {
		return func(m Model) (tea.Model, tea.Cmd) {
			cmd := m.changeCompactContext(more)
			return m, cmd
		}
	}
	return append(items, tbItem{sep: true},
		tbItem{label: "One more line", key: m.toolbarKey(keymap.ActionMoreContext), disabled: !m.modes.compact, keepOpen: true, run: more(true)},
		tbItem{label: "One line less", key: m.toolbarKey(keymap.ActionLessContext), disabled: !m.modes.compact, keepOpen: true, run: more(false)})
}

// toolbarKey is the key shown for action: its shortest binding.
func (m Model) toolbarKey(action keymap.Action) string {
	keys := m.keymap.KeysFor(action)
	if len(keys) == 0 {
		return ""
	}
	return slices.MinFunc(keys, func(a, b string) int {
		if d := lipgloss.Width(a) - lipgloss.Width(b); d != 0 {
			return d
		}
		return shifted(a) - shifted(b) // alt+= before alt++, which takes shift
	})
}

// indentW is the cells an item is indented by.
func indentW(it tbItem) int {
	if it.indent {
		return 2
	}
	return 0
}

// shifted reports 1 for a key that needs shift on a US layout, such as
// alt++, and 0 otherwise.
func shifted(key string) int {
	if strings.HasSuffix(key, "++") {
		return 1
	}
	return 0
}

// tbSpan is where a button sits on the toolbar row, in cells from the
// pane's content edge.
type tbSpan struct {
	x, w int
	text string // the button's text, its key included
	key  string // the key part of text, drawn muted
	// segment and on are the button's: a toggle segment, drawn in the
	// accent color when on and muted when not
	segment, on bool
}

// toolbarDetail is how much each button says, from most to least.
type toolbarDetail int

const (
	tbFull     toolbarDetail = iota // icon, label and key
	tbNoKeys                        // icon and label
	tbIconOnly                      // the icon
)

// toolbarLayout places the buttons in width cells: with all they say when
// it fits, then without keys, then icons only, then as many icons as fit.
// The layout and the clicks read it alike, so a click hits what is drawn.
func (m Model) toolbarLayout(buttons []tbButton, width int) []tbSpan {
	for _, d := range []toolbarDetail{tbFull, tbNoKeys, tbIconOnly} {
		spans, fits := m.layoutAt(buttons, width, d)
		if fits || d == tbIconOnly {
			return spans
		}
	}
	return nil
}

// layoutAt places the buttons at one level of detail, keeping those that
// fit, and reports whether all did.
func (m Model) layoutAt(buttons []tbButton, width int, d toolbarDetail) ([]tbSpan, bool) {
	const gap = 2
	var spans []tbSpan
	x := 1 // the header's leading space
	for i, b := range buttons {
		if i > 0 && b.segment && buttons[i-1].segment {
			x -= gap - 1 // a toggle's segments sit one cell apart
		}
		text, key := b.icon, ""
		if d != tbIconOnly && !b.segment {
			text += " " + b.label
			if b.menu != nil {
				text += " ▾"
			}
			if d == tbFull && b.key != "" {
				key = b.key
				text += " " + key
			}
		}
		w := lipgloss.Width(text)
		if x+w > width {
			return spans, false
		}
		spans = append(spans, tbSpan{x: x, w: w, text: text, key: key, segment: b.segment, on: b.on})
		x += w + gap
	}
	return spans, true
}

// renderToolbar is the toolbar row, width cells wide, on the diff pane's
// background.
func (m Model) renderToolbar(width int) string {
	buttons := m.toolbarButtons()
	normal := string(m.resolver.Color(style.ColorKeyNormalFg))
	muted := string(m.resolver.Color(style.ColorKeyMutedFg))
	accent := string(m.resolver.Color(style.ColorKeyAccentFg))
	var b strings.Builder
	x := 0
	for i, s := range m.toolbarLayout(buttons, width) {
		b.WriteString(strings.Repeat(" ", s.x-x))
		active := m.toolbar.open && m.toolbar.button == i
		if active {
			b.WriteString("\033[7m")
		}
		label := strings.TrimSuffix(s.text, " "+s.key)
		if s.key == "" {
			label = s.text
		}
		switch {
		case s.segment && s.on:
			b.WriteString(accent + "\033[1m" + label + "\033[22m")
		case s.segment:
			b.WriteString(muted + label)
		default:
			b.WriteString(normal + label)
		}
		if s.key != "" {
			b.WriteString(muted + " " + s.key)
		}
		if active {
			b.WriteString("\033[27m")
		}
		x = s.x + s.w
	}
	b.WriteString(strings.Repeat(" ", max(0, width-x)))
	b.WriteString(string(style.ResetFg))
	bg := string(m.resolver.Color(style.ColorKeyDiffPaneBg))
	if bg == "" {
		return b.String()
	}
	return bg + b.String() + string(style.ResetBg)
}

// toolbarRow is the toolbar's screen row, the top one.
const toolbarRow = 0

// activeMenu is the open button's menu, nil when none is open or the
// button is a plain one.
func (m Model) activeMenu() []tbItem {
	if !m.toolbar.open {
		return nil
	}
	buttons := m.toolbarButtons()
	if m.toolbar.button >= len(buttons) || buttons[m.toolbar.button].menu == nil {
		return nil
	}
	return buttons[m.toolbar.button].menu(m)
}

// menuBox is where the open menu is drawn on screen, and its rows.
type menuBox struct {
	x, y, w int
	lines   []string
}

// menuGeometry lays the open menu out under its button, kept on screen.
func (m Model) menuGeometry() (menuBox, bool) {
	items := m.activeMenu()
	if len(items) == 0 {
		return menuBox{}, false
	}
	spans := m.toolbarLayout(m.toolbarButtons(), m.layout.width)
	if m.toolbar.button >= len(spans) {
		return menuBox{}, false
	}
	labelW, keyW := 0, 0
	for _, it := range items {
		labelW = max(labelW, lipgloss.Width(it.label)+indentW(it))
		keyW = max(keyW, lipgloss.Width(it.key))
	}
	inner := 3 + labelW + 1 // a space, the check, the label, a space
	if keyW > 0 {
		inner += 2 + keyW
	}
	inner++ // a closing space
	w := inner + 2
	x := spans[m.toolbar.button].x - 1
	x = max(0, min(x, m.layout.width-w))
	box := menuBox{x: x, y: toolbarRow + 1, w: w}

	accent := string(m.resolver.Color(style.ColorKeyAccentFg))
	normal := string(m.resolver.Color(style.ColorKeyNormalFg))
	muted := string(m.resolver.Color(style.ColorKeyMutedFg))
	bg := string(m.resolver.Color(style.ColorKeyDiffPaneBg))
	border := func(l, r string) string {
		return bg + accent + l + strings.Repeat("─", inner) + r + "\033[0m"
	}
	box.lines = append(box.lines, border("╭", "╮"))
	for i, it := range items {
		if it.sep {
			box.lines = append(box.lines, bg+accent+"├"+muted+strings.Repeat("─", inner)+accent+"┤"+"\033[0m")
			continue
		}
		check := "   "
		if it.checked {
			check = " ✓ "
		}
		check = strings.Repeat(" ", indentW(it)) + check
		text := check + it.label + strings.Repeat(" ", labelW-lipgloss.Width(it.label)-indentW(it)+1)
		key := ""
		if keyW > 0 {
			key = "  " + strings.Repeat(" ", keyW-lipgloss.Width(it.key)) + it.key
		}
		fg, keyFg := normal, muted
		if it.disabled {
			fg = muted
		}
		row := fg + text + keyFg + key + " "
		if i == m.toolbar.item {
			row = "\033[7m" + fg + text + key + " " + "\033[27m"
		}
		box.lines = append(box.lines, bg+accent+"│"+row+accent+"│"+"\033[0m")
	}
	box.lines = append(box.lines, border("╰", "╯"))
	return box, true
}

// composeMenu draws the open menu over the screen.
func (m Model) composeMenu(screen string) string {
	box, ok := m.menuGeometry()
	if !ok {
		return screen
	}
	lines := strings.Split(screen, "\n")
	for i, fg := range box.lines {
		y := box.y + i
		if y >= len(lines) {
			break
		}
		line := lines[y]
		if w := lipgloss.Width(line); w < box.x+box.w {
			line += strings.Repeat(" ", box.x+box.w-w)
		}
		lines[y] = ansi.Cut(line, 0, box.x) + fg + ansi.Cut(line, box.x+box.w, m.layout.width)
	}
	return strings.Join(lines, "\n")
}

// openToolbar activates button, its first item highlighted, and gives the
// diff pane focus: the tools act on the diff. The diff is repainted when it
// gains focus, as switchPane does, since its cursor is drawn with the focus.
func (m *Model) openToolbar(button int) tea.Cmd {
	m.clearPendingInputState()
	m.toolbar.open, m.toolbar.button, m.toolbar.item = true, button, -1
	if m.layout.focus != paneDiff {
		m.layout.focus = paneDiff
		if m.filesLoaded {
			m.layout.viewport.SetContent(m.renderDiff())
		}
	}
	m.moveMenuItem(1)
	return nil
}

// closeToolbar closes the open menu.
func (m *Model) closeToolbar() {
	m.toolbar.open = false
}

// moveMenuItem moves the highlight by delta, over dividers and disabled
// items, and stops at the ends.
func (m *Model) moveMenuItem(delta int) {
	items := m.activeMenu()
	for i := m.toolbar.item + delta; i >= 0 && i < len(items); i += delta {
		if !items[i].sep && !items[i].disabled {
			m.toolbar.item = i
			return
		}
	}
}

// moveButton activates the next (or previous) button, wrapping around.
func (m *Model) moveButton(delta int) tea.Cmd {
	n := len(m.toolbarButtons())
	if n == 0 {
		return nil
	}
	return m.openToolbar((m.toolbar.button + delta + n) % n)
}

// activate runs the active button, or the highlighted item of its menu.
func (m Model) activate() (tea.Model, tea.Cmd) {
	buttons := m.toolbarButtons()
	if m.toolbar.button >= len(buttons) {
		m.closeToolbar()
		return m, nil
	}
	b := buttons[m.toolbar.button]
	if b.menu == nil {
		m.closeToolbar()
		return b.run(m)
	}
	items := b.menu(m)
	if m.toolbar.item < 0 || m.toolbar.item >= len(items) {
		return m, nil
	}
	it := items[m.toolbar.item]
	if it.sep || it.disabled {
		return m, nil
	}
	if !it.keepOpen {
		m.closeToolbar()
	}
	return it.run(m)
}

// handleToolbarKey takes the keys while a button is active. A key the menu
// does not use closes it and does what it does anywhere else.
func (m Model) handleToolbarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "f10", "alt+m":
		m.closeToolbar()
		return m, nil
	case "left", "h", "shift+tab":
		cmd := m.moveButton(-1)
		return m, cmd
	case "right", "l", "tab":
		cmd := m.moveButton(1)
		return m, cmd
	case "up", "k":
		m.moveMenuItem(-1)
		return m, nil
	case "down", "j":
		m.moveMenuItem(1)
		return m, nil
	case "enter", " ":
		return m.activate()
	}
	if action := m.keymap.Resolve(msg.String()); action == keymap.ActionViewMenu || action == keymap.ActionContextMenu {
		return m.toggleMenu(action) // another menu's key switches to it, its own closes it
	}
	m.closeToolbar()
	return m.handleKey(msg)
}

// toggleMenu opens the dropdown action opens, or closes it when it is the
// one open.
func (m Model) toggleMenu(action keymap.Action) (tea.Model, tea.Cmd) {
	if !m.toolbarShown() {
		return m, nil
	}
	for i, b := range m.toolbarButtons() {
		if b.action != action {
			continue
		}
		if m.toolbar.open && m.toolbar.button == i {
			m.closeToolbar()
			return m, nil
		}
		cmd := m.openToolbar(i)
		return m, cmd
	}
	if action == keymap.ActionContextMenu {
		m.compact.hint = "context size not applicable in this mode"
	}
	return m, nil
}

// toggleToolbar opens the first button from the keyboard, or closes the
// open one.
func (m Model) toggleToolbar() (tea.Model, tea.Cmd) {
	if m.toolbar.open {
		m.closeToolbar()
		return m, nil
	}
	if !m.toolbarShown() {
		return m, nil
	}
	for i, b := range m.toolbarButtons() {
		if b.menu != nil { // the first menu, View, past the view toggle
			cmd := m.openToolbar(i)
			return m, cmd
		}
	}
	cmd := m.openToolbar(0)
	return m, cmd
}

// handleToolbarMouse takes a left click while a menu is open or on the
// toolbar row, and reports whether it did: a click on a button opens or
// closes it, on an item picks it, and anywhere else closes the menu.
func (m Model) handleToolbarMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	onRow := m.toolbarShown() && msg.Y == toolbarRow
	if !m.toolbar.open && !onRow {
		return m, nil, false
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil, m.toolbar.open // wheel and releases go nowhere under an open menu
	}
	if box, ok := m.menuGeometry(); ok && msg.X >= box.x && msg.X < box.x+box.w && msg.Y > box.y && msg.Y < box.y+len(box.lines)-1 {
		items := m.activeMenu()
		if i := msg.Y - box.y - 1; i < len(items) && !items[i].sep && !items[i].disabled {
			m.toolbar.item = i
			model, cmd := m.activate()
			return model, cmd, true
		}
		return m, nil, true
	}
	if onRow {
		x := msg.X
		for i, s := range m.toolbarLayout(m.toolbarButtons(), m.layout.width) {
			if x < s.x || x >= s.x+s.w {
				continue
			}
			if m.toolbar.open && m.toolbar.button == i {
				m.closeToolbar()
				return m, nil, true
			}
			load := m.openToolbar(i)
			if m.activeMenu() == nil {
				model, cmd := m.activate() // a plain button runs at once
				return model, cmd, true
			}
			return m, load, true
		}
	}
	m.closeToolbar()
	return m, nil, true
}
