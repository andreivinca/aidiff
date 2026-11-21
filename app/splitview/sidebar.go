package splitview

import (
	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
)

// defaultSidebarWidth is the sidebar's width, its separator left out, when
// Config.SidebarWidth leaves it unset.
const defaultSidebarWidth = 30

// minSidebarWidth is the narrowest a sidebar is shown; a narrower window
// keeps it hidden.
const minSidebarWidth = 12

// focusSidebar is the Model.focus value when the sidebar has focus.
const focusSidebar = -2

// Sidebar is a column left of the wrapped model that the embedding program
// fills, such as a list of projects. It is hidden until shown (Keys.Sidebar
// or ShowSidebarMsg) and has focus while shown, until the user moves it.
// The split calls it on the UI goroutine only.
type Sidebar interface {
	// Available reports whether the sidebar has anything to show. While it
	// does not, the sidebar key reaches the focused pane as any other key.
	Available() bool
	// Shown tells the sidebar it was just shown, e.g. to select the current
	// row.
	Shown()
	// View draws the sidebar at width by height cells; focused is whether it
	// takes the keys.
	View(width, height int, focused bool) string
	// HandleKey acts on a key while the sidebar has focus. hide asks the
	// split to hide it.
	HandleKey(msg tea.KeyMsg) (cmd tea.Cmd, hide bool)
	// Click acts on a left click on row y of the sidebar.
	Click(y int) (cmd tea.Cmd, hide bool)
	// Select acts on the number key n (1-9), pressed with ctrl or alt
	// anywhere, as on row n of a numbered list.
	Select(n int) (cmd tea.Cmd, hide bool)
}

// ShowSidebarMsg shows the sidebar and gives it focus, when it is available.
type ShowSidebarMsg struct{}

// HideSidebarMsg hides the sidebar.
type HideSidebarMsg struct{}

// sidebarAvailable reports whether there is a sidebar with something to
// show.
func (m Model) sidebarAvailable() bool {
	return m.cfg.Sidebar != nil && m.cfg.Sidebar.Available()
}

// sideW is the sidebar's width, its separator left out: 0 while it is hidden
// or the window has no room for it next to the narrowest wrapped model.
func (m Model) sideW() int {
	if !m.sideOpen || m.cfg.Sidebar == nil {
		return 0
	}
	return m.sideFit()
}

// sideFit is the width the sidebar gets when shown, 0 when the window has no
// room for it.
func (m Model) sideFit() int {
	w := min(m.cfg.SidebarWidth, m.winW-minPaneWidth-1)
	if w < minSidebarWidth {
		return 0
	}
	return w
}

// tooNarrow is flashed when a sidebar has no room to be shown.
const tooNarrow = "window too narrow for the side panel"

// sideCols are the columns the sidebar takes, its separator included.
func (m Model) sideCols() int {
	if w := m.sideW(); w > 0 {
		return w + 1
	}
	return 0
}

// showSidebar shows the sidebar and focuses it. A window too narrow to show
// it keeps focus where it is: a sidebar no one sees must not take the keys.
func (m Model) showSidebar() (tea.Model, tea.Cmd) {
	if !m.sidebarAvailable() {
		return m, nil
	}
	if m.winW > 0 && m.sideFit() == 0 {
		m.flash = tooNarrow
		return m, nil
	}
	if !m.sideOpen {
		m.sideOpen = true
		m.sideReturn = m.focus
		if m.focus == focusOuter {
			m.sideReturn = m.outerReturn
		}
		m.cfg.Sidebar.Shown()
	}
	m.focus = focusSidebar
	m.outerOpen = false // it is shown only while it has focus
	return m.relayout()
}

// hideSidebar hides the sidebar; focus goes back where it was before.
func (m Model) hideSidebar() (tea.Model, tea.Cmd) {
	if !m.sideOpen {
		return m, nil
	}
	return m.leaveSidebar().relayout()
}

// leaveSidebar is hideSidebar without the relayout.
func (m Model) leaveSidebar() Model {
	m.sideOpen = false
	m.outerOpen = false // nothing is left to its right to come back to
	if m.focus == focusSidebar || m.focus == focusOuter {
		m.focus = focusDiff
		if m.sideReturn >= 0 && m.sideReturn < len(m.terms) {
			m.focus = m.sideReturn
		}
	}
	return m
}

// toggleSidebar shows a hidden sidebar and hides a shown one.
func (m Model) toggleSidebar() (tea.Model, tea.Cmd) {
	if m.sideOpen {
		return m.hideSidebar()
	}
	return m.showSidebar()
}

// sidebarResult applies what the sidebar asked for; its command acts for
// the wrapped model on screen (see scopedMsg).
func (m Model) sidebarResult(cmd tea.Cmd, hide bool) (tea.Model, tea.Cmd) {
	cmd = m.scoped(cmd)
	if !hide {
		return m, cmd
	}
	model, layoutCmd := m.hideSidebar()
	return model, tea.Batch(cmd, layoutCmd)
}

// sidebarKey routes a key to the focused sidebar.
func (m Model) sidebarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.sidebarAvailable() {
		return m.hideSidebar() // e.g. the connection it listed is gone
	}
	return m.sidebarResult(m.cfg.Sidebar.HandleKey(msg))
}

// selectNumber is a ctrl or alt number key for the sidebar's row n.
func (m Model) selectNumber(n int) (tea.Model, tea.Cmd) {
	return m.sidebarResult(m.cfg.Sidebar.Select(n))
}

// numberKey reports whether k is ctrl+1…ctrl+9 (alt+1…alt+9 too), the keys
// that select a sidebar row by its number, and which.
func numberKey(k uv.KeyPressEvent) (int, bool) {
	if k.Code < '1' || k.Code > '9' || k.Mod&(uv.ModCtrl|uv.ModAlt) == 0 {
		return 0, false
	}
	return int(k.Code - '0'), true
}

// altNumber reports whether msg is alt+1…alt+9, the number keys every
// terminal sends, and which.
func altNumber(msg tea.KeyMsg) (int, bool) {
	if !msg.Alt || msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return 0, false
	}
	r := msg.Runes[0]
	if r < '1' || r > '9' {
		return 0, false
	}
	return int(r - '0'), true
}

// sidebarLines are the sidebar's rows, each followed by its separator.
func (m Model) sidebarLines() []string {
	w, h := m.sideW(), m.bodyH()
	return m.sidebarBlock(slotSide, m.cfg.Sidebar.View(w, h, m.focus == focusSidebar), w, h)
}

// sidebarMouse handles the mouse over the sidebar's columns: a left click
// focuses it and acts on the row; the separator and other buttons do
// nothing.
func (m Model) sidebarMouse(msg tea.MouseMsg, x int) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || x >= m.sideW() {
		return m, nil
	}
	m.focus = focusSidebar
	return m.sidebarResult(m.cfg.Sidebar.Click(msg.Y))
}
