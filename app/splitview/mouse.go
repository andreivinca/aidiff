package splitview

import tea "github.com/charmbracelet/bubbletea"

// dragMain is the Model.dragSep value for the divider between the wrapped
// model and the terminals.
const dragMain = -1

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.switcher.open {
		return m.switcherMouse(msg)
	}
	if m.modal() {
		return m, nil // modal: swallow
	}
	return m.routeMouse(msg)
}

// routeMouse hands a mouse event to what is under the pointer.
func (m Model) routeMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	cols := m.leftCols()
	if m.dragging && msg.Action == tea.MouseActionPress && !tea.MouseEvent(msg).IsWheel() {
		// a fresh press: the drag's release was lost (it happened outside
		// the window), so the drag ends where it was taken and the press
		// goes where it points
		m.dragging = false
		if m.dragMoved {
			model, resized := m.commitDrag(m.dragX)
			next, cmd := model.(Model).routeMouse(msg)
			return next, tea.Batch(resized, cmd)
		}
	}
	if m.top > 0 {
		if msg.Y < m.top {
			if msg.Action == tea.MouseActionPress && !m.dragging {
				return m.tabsMouse(msg)
			}
			msg.Y = m.top // a drag's motion or release over the strip: the row under it
		}
		msg.Y -= m.top // from here on y is relative to the rows under the strip
	}
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
			m.dragging, m.dragSep, m.dragX, m.dragMoved = true, sep, msg.X, false
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
// when a terminal coalesces the last motion event. Wheel events do not end
// it. While it moves only the divider is drawn where the pointer is
// (dragGhost): resizing the panes on every motion re-wrapped a chat's whole
// transcript and re-rendered the review each time, so the drag lagged. They
// resize once, on release.
func (m Model) dragMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Action {
	case tea.MouseActionMotion:
		if msg.X != m.dragX {
			m.dragX, m.dragMoved = msg.X, true
		}
		return m, nil
	case tea.MouseActionRelease:
		m.dragging, m.dragMoved = false, false
		return m.commitDrag(msg.X)
	}
	return m, nil
}

// commitDrag moves the dragged divider to column x and resizes the panes
// beside it.
func (m Model) commitDrag(x int) (tea.Model, tea.Cmd) {
	m.dragMoved = false
	if m.dragSep == dragMain {
		return m.dragTo(x)
	}
	return m.dragSepTo(m.dragSep, x)
}

// dragTarget is the column the dragged divider would land on for pointer
// column x, kept within the panes' minimum widths as dragTo and dragSepTo
// keep it; ok is false when the divider cannot move.
func (m Model) dragTarget(x int) (int, bool) {
	if m.dragSep == dragMain {
		if m.width < 2*minPaneWidth {
			return 0, false
		}
		return min(max(x+1, minPaneWidth), m.width-minPaneWidth) - 1, true
	}
	rects := m.termRects()
	k := m.dragSep
	if k+1 >= len(rects) {
		return 0, false
	}
	a, b := rects[k], rects[k+1]
	combined := a.w + b.w
	if combined < 2*minTermWidth {
		return 0, false
	}
	return m.leftW + a.x + min(max(x-m.leftW-a.x, minTermWidth), combined-minTermWidth), true
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

// mainGripAt reports whether row y under the strip shows the main
// divider's grip, drawn over the wrapped model's last column. Its bottom row
// (a status line) is left whole.
func (m Model) mainGripAt(y int) bool {
	return len(m.terms) > 0 && y < m.paneH()-1
}
