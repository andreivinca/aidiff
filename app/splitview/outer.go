package splitview

import (
	tea "github.com/charmbracelet/bubbletea"
)

// The outer sidebar (Config.OuterSidebar) is a second column left of the
// sidebar, such as a list of connections. It is hidden except while it has
// focus: alt+← (Keys.FocusLeft) reaches it from the sidebar, or from the
// leftmost panel when there is no sidebar to show, and it hides again as
// soon as focus leaves it. It takes no number keys.

// focusOuter is the Model.focus value when the outer sidebar has focus.
const focusOuter = -3

// outerAvailable reports whether there is an outer sidebar with something to
// show.
func (m Model) outerAvailable() bool {
	return m.cfg.OuterSidebar != nil && m.cfg.OuterSidebar.Available()
}

// outerW is the outer sidebar's width, its separator left out: 0 while it is
// hidden or there is no room for it beside the sidebar and the narrowest
// wrapped model.
func (m Model) outerW() int {
	if !m.outerOpen || m.cfg.OuterSidebar == nil {
		return 0
	}
	return m.outerFit()
}

// outerFit is the width the outer sidebar gets when shown beside the
// sidebar as it is, 0 when the window has no room for it.
func (m Model) outerFit() int {
	w := min(m.cfg.SidebarWidth, m.winW-m.sideCols()-minPaneWidth-1)
	if w < minSidebarWidth {
		return 0
	}
	return w
}

// outerCols are the columns the outer sidebar takes, its separator included.
func (m Model) outerCols() int {
	if w := m.outerW(); w > 0 {
		return w + 1
	}
	return 0
}

// leftCols are the columns both sidebars take, left of the wrapped model.
func (m Model) leftCols() int {
	return m.outerCols() + m.sideCols()
}

// showOuter shows the outer sidebar and focuses it, unless the window is
// too narrow to show it (see showSidebar).
func (m Model) showOuter() (tea.Model, tea.Cmd) {
	if !m.outerAvailable() {
		return m, nil
	}
	if m.winW > 0 && m.outerFit() == 0 {
		m.flash = tooNarrow
		return m, nil
	}
	if !m.outerOpen {
		m.outerOpen = true
		m.outerReturn = m.focus
		if !m.sideOpen {
			m.sideReturn = m.focus // what a workspace switch parks
		}
		m.cfg.OuterSidebar.Shown()
	}
	m.focus = focusOuter
	return m.relayout()
}

// hideOuter hides the outer sidebar; focus goes back where it came from.
func (m Model) hideOuter() (tea.Model, tea.Cmd) {
	if !m.outerOpen {
		return m, nil
	}
	return m.leaveOuter().relayout()
}

// leaveOuter is hideOuter without the relayout.
func (m Model) leaveOuter() Model {
	m.outerOpen = false
	if m.focus == focusOuter {
		m.focus = m.outerReturn
		switch {
		case m.focus == focusSidebar && !m.sideOpen, m.focus >= len(m.terms):
			m.focus = focusDiff
		case m.focus == focusSidebar:
		case m.focus < focusDiff:
			m.focus = focusDiff
		}
	}
	return m
}

// outerResult applies what the outer sidebar asked for; its command acts
// for the wrapped model on screen (see scopedMsg).
func (m Model) outerResult(cmd tea.Cmd, hide bool) (tea.Model, tea.Cmd) {
	cmd = m.scoped(cmd)
	if !hide {
		return m, cmd
	}
	model, layoutCmd := m.hideOuter()
	return model, tea.Batch(cmd, layoutCmd)
}

// outerKey routes a key to the focused outer sidebar.
func (m Model) outerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.outerAvailable() {
		return m.hideOuter()
	}
	return m.outerResult(m.cfg.OuterSidebar.HandleKey(msg))
}

// focusLeft is Keys.FocusLeft: the panel to the left, and past the leftmost
// one the hidden sidebar, then the outer sidebar. The wrapped model's own
// panes come first while it has focus (paneFocuser).
func (m Model) focusLeft() (tea.Model, tea.Cmd) {
	switch {
	case m.focus == focusOuter:
		return m, nil
	case m.focus == focusSidebar:
		return m.showOuter()
	case m.focus == 0:
		m = m.moveFocus(-1)
		return m.enterInner(true), nil
	case m.focus != focusDiff:
		return m.moveFocus(-1), nil
	}
	if next, moved := m.innerPaneMove(false); moved {
		return next, nil
	}
	switch {
	case m.sideOpen:
		return m.moveFocus(-1), nil
	case m.sidebarAvailable():
		return m.showSidebar()
	}
	return m.showOuter()
}

// focusRight is Keys.Focus: the panel to the right; leaving the outer
// sidebar hides it. The wrapped model's own panes come first while it has
// focus (paneFocuser).
func (m Model) focusRight() (tea.Model, tea.Cmd) {
	switch m.focus {
	case focusOuter:
		m.outerReturn = focusDiff
		if m.sideOpen {
			m.outerReturn = focusSidebar
		}
		model, cmd := m.hideOuter()
		if next := model.(Model); next.focus == focusDiff {
			return next.enterInner(false), cmd
		}
		return model, cmd
	case focusSidebar:
		m = m.moveFocus(1)
		return m.enterInner(false), nil
	case focusDiff:
		if next, moved := m.innerPaneMove(true); moved {
			return next, nil
		}
	}
	return m.moveFocus(1), nil
}

// outerLines are the outer sidebar's rows, each followed by its separator.
func (m Model) outerLines() []string {
	w, h := m.outerW(), m.bodyH()
	return m.sidebarBlock(slotOuter, m.cfg.OuterSidebar.View(w, h, m.focus == focusOuter), w, h)
}

// outerMouse handles a left click on the outer sidebar's row y.
func (m Model) outerMouse(msg tea.MouseMsg, x int) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || x >= m.outerW() {
		return m, nil
	}
	m.focus = focusOuter
	return m.outerResult(m.cfg.OuterSidebar.Click(msg.Y))
}

// closeUnfocused hides the sidebars focus has left, after any key, click
// or message: the outer sidebar once it lost focus, the sidebar once
// neither it nor the outer sidebar to its left has focus. Both are shown
// only while the user works in them.
func (m Model) closeUnfocused(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{cmd}
	if m.outerOpen && m.focus != focusOuter {
		model, layoutCmd := m.hideOuter()
		m, cmds = model.(Model), append(cmds, layoutCmd)
	}
	if m.sideOpen && m.focus != focusSidebar && m.focus != focusOuter {
		model, layoutCmd := m.hideSidebar()
		m, cmds = model.(Model), append(cmds, layoutCmd)
	}
	return m, tea.Batch(cmds...)
}
