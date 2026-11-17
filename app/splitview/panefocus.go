package splitview

import tea "github.com/charmbracelet/bubbletea"

// paneFocuser is a wrapped model with panes of its own, such as a review's
// file tree and diff, that the focus keys walk through as they do the
// split's panels: while it has focus they move between its panes first and
// leave it only at its edge, and focus coming in lands on the pane nearest
// to where it came from.
type paneFocuser interface {
	// MovePaneFocus moves focus to its pane on the right (or left) and
	// reports whether it moved; false at its edge or while it is busy.
	MovePaneFocus(right bool) (tea.Model, bool)
	// FocusPaneEdge focuses its rightmost (or leftmost) pane.
	FocusPaneEdge(right bool) tea.Model
}

// innerPaneMove moves focus between the wrapped model's own panes and
// reports whether it did.
func (m Model) innerPaneMove(right bool) (Model, bool) {
	f, ok := m.inner.(paneFocuser)
	if !ok {
		return m, false
	}
	next, moved := f.MovePaneFocus(right)
	if !moved {
		return m, false
	}
	m.inner = next
	m.dropLeftCache()
	return m, true
}

// enterInner lands focus, just come to the wrapped model, on its pane on
// the side it came from: the rightmost from a pane, the leftmost from a
// sidebar.
func (m Model) enterInner(fromRight bool) Model {
	if f, ok := m.inner.(paneFocuser); ok && m.focus == focusDiff {
		m.inner = f.FocusPaneEdge(fromRight)
		m.dropLeftCache()
	}
	return m
}
