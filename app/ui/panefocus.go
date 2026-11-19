package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/ui/style"
)

// aidiff's split moves focus across its panels with one pair of keys. The
// review's own panes, the tree (or markdown TOC) on the left and the diff on
// the right, take part: the keys move between them first, and leave the
// review only at its edge. Both moves go through togglePane, the switch tab
// makes, so a hidden tree and a diff with no file loaded are honored the
// same way.

// MovePaneFocus moves focus to the review's pane on the right (or left) and
// reports whether it moved. It does not while the review is busy with
// typing or a popup, nor past its edge: the split then moves on to its own
// panels.
func (m Model) MovePaneFocus(right bool) (tea.Model, bool) {
	if m.paneFocusBusy() {
		return m, false
	}
	if (right && m.layout.focus != paneTree) || (!right && m.layout.focus != paneDiff) {
		return m, false
	}
	return m.switchPane()
}

// FocusPaneEdge focuses the review's pane on the side focus comes in from:
// the diff from the right, the tree from the left.
func (m Model) FocusPaneEdge(right bool) tea.Model {
	if m.paneFocusBusy() {
		return m
	}
	if (right && m.layout.focus == paneDiff) || (!right && m.layout.focus == paneTree) {
		return m
	}
	model, _ := m.switchPane()
	return model
}

// switchPane is tab's pane switch, with the diff repainted so its cursor
// line shows whether the diff has focus. It reports whether focus moved.
func (m Model) switchPane() (Model, bool) {
	before := m.layout.focus
	m.togglePane()
	if m.layout.focus == before {
		return m, false
	}
	if m.filesLoaded {
		m.layout.viewport.SetContent(m.renderDiff())
	}
	return m, true
}

// paneFocusBusy reports whether the review is in a state where its keys
// belong to typing or a popup, and its pane focus stays where it is.
func (m Model) paneFocusBusy() bool {
	return !m.ready || m.annot.annotating || m.search.active || m.reload.pending ||
		m.inConfirmDiscard || m.overlay.Active() || m.toolbar.open
}

// FocusedColumns are the columns of the review's focused pane, the tree (or
// TOC) or the diff, within the review's own width: the split highlights
// them on the status bar. With the tree hidden the diff takes them all.
func (m Model) FocusedColumns() (from, to int) {
	if m.treePaneHidden() {
		return 0, m.layout.width
	}
	split := min(m.layout.treeWidth+2, m.layout.width) // the tree's content and its two borders
	if m.layout.focus == paneTree {
		return 0, split
	}
	return split, m.layout.width
}

// FocusMarkColors are the colors the split marks the focused panel with on
// the status bar: the theme's added lines, their background and their text,
// as select-graphic-rendition sequences ("" without colors).
func (m Model) FocusMarkColors() (bg, fg string) {
	return string(m.resolver.Color(style.ColorKeyAddLineBg)), string(m.resolver.Color(style.ColorKeyAddLineFg))
}
