package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// minDiffWidth is the narrowest the diff content gets when the tree pane is dragged wider.
const minDiffWidth = 20

// dividerRenderInterval bounds how often a drag re-renders the diff: a new
// width misses every cached line, so on a large file one render per motion
// event would leave the divider lagging behind the pointer.
const dividerRenderInterval = 50 * time.Millisecond

// dividerState is the tree/diff divider the mouse can drag.
type dividerState struct {
	share    float64 // tree width as a share of the window once dragged; 0 uses the configured ratio
	dragging bool    // a drag is in progress
	// renderOwed marks a width the diff is not rendered at yet; renderedAt is
	// when a drag last rendered it, and tickInFlight that a dividerRenderMsg
	// is scheduled to render what motion left owed
	renderOwed   bool
	renderedAt   time.Time
	tickInFlight bool
}

// dividerRenderMsg renders the diff at the width a drag left it owing.
type dividerRenderMsg struct{}

// fitTreeWidth is the tree pane's width for the current window: the share the
// user last dragged it to, else the configured ratio (N of 10 units).
func (m Model) fitTreeWidth() int {
	if m.divider.share > 0 {
		return m.clampTreeWidth(int(float64(m.layout.width)*m.divider.share + 0.5))
	}
	return max(minTreeWidth, m.layout.width*m.cfg.treeWidthRatio/10)
}

// clampTreeWidth keeps a tree width between minTreeWidth and what leaves the
// diff its minimum, borders included.
func (m Model) clampTreeWidth(w int) int {
	return max(minTreeWidth, min(w, m.layout.width-4-minDiffWidth))
}

// dragTreeTo moves the tree/diff divider so the tree's right border sits at
// column x, and remembers the share for later resizes. The diff is
// re-rendered at its new width at most every dividerRenderInterval, the
// rest when the drag ends or the scheduled render comes.
func (m Model) dragTreeTo(x int) (Model, tea.Cmd) {
	if m.layout.width < minTreeWidth+4+minDiffWidth {
		return m, nil
	}
	w := m.clampTreeWidth(x - 1)
	if w == m.layout.treeWidth {
		return m, nil
	}
	m.layout.treeWidth = w
	m.divider.share = float64(w) / float64(m.layout.width)
	m.layout.viewport.Width = m.layout.width - w - 4
	m.divider.renderOwed = true
	if time.Since(m.divider.renderedAt) >= dividerRenderInterval {
		m.renderAtDraggedWidth()
		return m, nil
	}
	if m.divider.tickInFlight {
		return m, nil
	}
	m.divider.tickInFlight = true
	return m, tea.Tick(dividerRenderInterval, func(time.Time) tea.Msg { return dividerRenderMsg{} })
}

// renderAtDraggedWidth re-renders the diff at the width the divider gives
// it, as a resize does: wrapped rows and the cursor's visual range follow
// the width, so the content must be rendered at it.
func (m *Model) renderAtDraggedWidth() {
	m.divider.renderOwed = false
	m.divider.renderedAt = time.Now()
	if m.file.name == "" {
		return
	}
	m.flushWheelPending() // anchor to the wheeled-to position, as handleResize does
	m.syncViewportToCursor()
}

// handleDividerRender renders what the drag left owed.
func (m Model) handleDividerRender() (tea.Model, tea.Cmd) {
	m.divider.tickInFlight = false
	if m.divider.renderOwed {
		m.renderAtDraggedWidth()
	}
	return m, nil
}

// endDrag ends a drag, rendering the width it left owed.
func (m Model) endDrag() Model {
	m.divider.dragging = false
	if m.divider.renderOwed {
		m.renderAtDraggedWidth()
	}
	return m
}

// treeDividerMouse handles the tree/diff divider: a press on the tree's right
// border starts a drag, motion with the button held moves it, a release ends
// it. handled reports whether the event was the divider's. A drag whose
// release was lost, over the toolbar or outside the window, ends on the next
// press, which then counts as an ordinary one, or on motion with no button
// held.
func (m Model) treeDividerMouse(msg tea.MouseMsg) (_ Model, _ tea.Cmd, handled bool) {
	if m.divider.dragging {
		switch {
		case msg.Action == tea.MouseActionMotion && msg.Button == tea.MouseButtonLeft:
			next, cmd := m.dragTreeTo(msg.X)
			return next, cmd, true
		case msg.Action == tea.MouseActionPress:
			m = m.endDrag()
		default:
			return m.endDrag(), nil, true
		}
	}
	onBorder := !m.treePaneHidden() && msg.X == m.layout.treeWidth+1 &&
		msg.Y >= 0 && msg.Y < m.layout.height-m.statusBarHeight()
	if onBorder && msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		m.divider.dragging = true
		return m, nil, true
	}
	return m, nil, false
}
