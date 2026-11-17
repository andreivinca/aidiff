package splitview

import tea "github.com/charmbracelet/bubbletea"

// dragMain is the Model.dragSep value for the divider between the wrapped
// model and the terminals.
const dragMain = -1

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.palette.open || m.quitDialog {
		return m, nil // modal: swallow
	}
	return m.hideOuterOffFocus(m.routeMouse(msg))
}

// routeMouse hands a mouse event to what is under the pointer.
func (m Model) routeMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	cols := m.leftCols()
	msg.X -= cols // from here on x is relative to the area right of the sidebars
	if m.dragging {
		return m.dragMouse(msg)
	}
	press := msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft
	rects := m.termRects()
	if m.routeCapturedMouse(msg, rects) {
		return m, nil
	}
	if msg.Y >= m.bodyH() {
		return m, nil // the status line under the whole window does nothing
	}
	if x := msg.X + cols; x < m.outerCols() {
		return m.outerMouse(msg, x)
	}
	if msg.X < 0 {
		return m.sidebarMouse(msg, msg.X+cols-m.outerCols())
	}

	if press {
		if sep, ok := m.dividerAt(msg.X, msg.Y, rects); ok {
			m.dragging, m.dragSep = true, sep
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
		capture, ok := pane.(mouseCapturer)
		if !ok || !capture.MouseCaptured() {
			continue
		}
		if ev, ok := toTermMouse(msg, msg.X-m.leftW-rects[i].x, msg.Y); ok {
			pane.SendMouse(ev)
		}
		return true
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
		return m.dragTo(msg.X)
	}
	return m.dragSepTo(m.dragSep, msg.X)
}

// dividerAt tells which divider's grip is drawn at (x, y): dragMain for the
// main grip, k for the separator right of terminal k. The cells around a grip
// belong to the panes beside it.
func (m Model) dividerAt(x, y int, rects []rect) (int, bool) {
	if x == m.leftW-1 && m.mainGripAt(y) {
		return dragMain, true
	}
	for k := 0; k+1 < len(rects); k++ {
		if x == m.leftW+rects[k].x+rects[k].w {
			return k, true
		}
	}
	return 0, false
}

// mainGripAt reports whether row y shows the main divider's grip, drawn over
// the wrapped model's last column. Its bottom row (a status line) is left whole.
func (m Model) mainGripAt(y int) bool {
	return len(m.terms) > 0 && y < m.height-1
}
