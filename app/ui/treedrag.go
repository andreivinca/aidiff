package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// minDiffWidth is the narrowest the diff content gets when the tree pane is dragged wider.
const minDiffWidth = 20

// dividerState is the tree/diff divider the mouse can drag.
type dividerState struct {
	share    float64 // tree width as a share of the window once dragged; 0 uses the configured ratio
	dragging bool    // a drag is in progress
}

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
// column x, and remembers the share for later resizes.
func (m Model) dragTreeTo(x int) Model {
	if m.layout.width < minTreeWidth+4+minDiffWidth {
		return m
	}
	w := m.clampTreeWidth(x - 1)
	if w == m.layout.treeWidth {
		return m
	}
	m.layout.treeWidth = w
	m.divider.share = float64(w) / float64(m.layout.width)
	m.layout.viewport.Width = m.layout.width - w - 4
	return m
}

// treeDividerMouse handles the tree/diff divider: a press on the tree's right
// border starts a drag, motion moves it, anything else ends it. handled
// reports whether the event was the divider's.
func (m Model) treeDividerMouse(msg tea.MouseMsg) (_ Model, handled bool) {
	if m.divider.dragging {
		if msg.Action == tea.MouseActionMotion {
			return m.dragTreeTo(msg.X), true
		}
		m.divider.dragging = false
		return m, true
	}
	onBorder := !m.treePaneHidden() && msg.X == m.layout.treeWidth+1 &&
		msg.Y >= 0 && msg.Y < m.layout.height-m.statusBarHeight()
	if onBorder && msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		m.divider.dragging = true
		return m, true
	}
	return m, false
}
