package ui

import tea "github.com/charmbracelet/bubbletea"

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
		m.inConfirmDiscard || m.overlay.Active()
}
