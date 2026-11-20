package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/ui/overlay"
)

// aidiff's @ list offers two tools acting on every annotation of the
// review: copy them to the clipboard (through ModelConfig.CopyAnnotations,
// as the palette's Copy annotations does) and delete them, after a
// confirmation.

// deleteAllAnnotationsMsg deletes every annotation, once confirmed.
type deleteAllAnnotationsMsg struct{}

// handleAnnotListTool runs the @ list's tool kind.
func (m Model) handleAnnotListTool(kind overlay.OutcomeKind) (tea.Model, tea.Cmd) {
	n := m.store.Count()
	if n == 0 {
		return m, nil
	}
	switch kind {
	case overlay.OutcomeAnnotCopyAll:
		if m.copyAnnotations == nil {
			return m, nil
		}
		return m, m.copyAnnotations(m.store.FormatOutput(), n)
	case overlay.OutcomeAnnotDeleteAll:
		m.overlay.Close() // the confirmation takes its place
		if m.revert.confirm == nil {
			return m.deleteAllAnnotations()
		}
		title := fmt.Sprintf("Delete all %d annotations?", n)
		if n == 1 {
			title = "Delete the annotation?"
		}
		yes := func() tea.Msg { return deleteAllAnnotationsMsg{} }
		return m, m.revert.confirm(title, "Removes every annotation of this review. They cannot be brought back.", "Delete", yes)
	default:
		return m, nil
	}
}

// deleteAllAnnotations removes every annotation and shows the diff without
// them; the annotated-only filter goes off, having nothing left to show.
func (m Model) deleteAllAnnotations() (tea.Model, tea.Cmd) {
	n := m.store.Count()
	if n == 0 {
		return m, nil
	}
	m.store.Clear()
	m.clearPendingJumps()
	m.annot.cursorOnAnnotation = false
	if m.tree.FilterActive() {
		m.tree.ToggleFilter(nil)
	}
	m.tree.RefreshFilter(m.annotatedFiles())
	m.output.hint = fmt.Sprintf("Deleted %d annotation%s", n, plural(n))
	if m.ready {
		m.syncViewportToCursor()
	}
	return m, nil
}
