package ui

import tea "github.com/charmbracelet/bubbletea"

// aidiff's Diff View… switches between whole files and changed blocks
// (compact mode, the C key) from the palette.

// SetCompactMsg shows the changed blocks only (On) or whole files.
type SetCompactMsg struct {
	On bool
}

// Compact reports whether the review shows only the changed blocks.
func (m Model) Compact() bool {
	return m.modes.compact && m.compact.applicable
}

// CompactApplicable reports whether the review can show only the changed
// blocks: not a file browser, stdin, or every file of a repository.
func (m Model) CompactApplicable() bool {
	return m.compact.applicable
}

// handleSetCompact switches compact mode to msg.On, as C would.
func (m Model) handleSetCompact(msg SetCompactMsg) (tea.Model, tea.Cmd) {
	if m.modes.compact == msg.On {
		return m, nil
	}
	cmd := m.toggleCompactMode()
	return m, cmd
}
