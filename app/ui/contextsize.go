package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// aidiff grows and shrinks the context around the changed blocks with
// alt+= (or alt++) and alt+-, so a block can be read in more of its file
// without switching to the whole file.

const (
	contextStep = 1   // lines added or removed per press
	contextMax  = 999 // well under the renderers' full-file sentinel
)

// changeCompactContext shows contextStep more (or fewer) lines around each
// change and re-fetches the file on screen, keeping the cursor where it is
// as the C toggle does. Other files pick the new size up when opened.
func (m *Model) changeCompactContext(more bool) tea.Cmd {
	if !m.modes.compact && m.compact.applicable {
		m.compact.hint = "showing whole files: C shows the changed blocks"
		return nil
	}
	n := m.modes.compactContext
	if more {
		n = min(n+contextStep, contextMax)
	} else {
		n = max(n-contextStep, 1)
	}
	return m.setCompactContext(n)
}

// setCompactContext shows n lines around each change, switching from whole
// files to the changed blocks when needed, and re-fetches the file on
// screen with the cursor kept where it is.
func (m *Model) setCompactContext(n int) tea.Cmd {
	switch {
	case !m.compact.applicable:
		m.compact.hint = "context size not applicable in this mode"
		return nil
	case m.file.requestedPath != "" && m.file.requestedPath != m.file.name:
		m.compact.hint = "context not changed: a file is still loading"
		return nil
	}
	n = min(max(n, 1), contextMax)
	m.compact.hint = fmt.Sprintf("%d lines around each change", n)
	if n == 1 {
		m.compact.hint = "1 line around each change"
	}
	if n == m.modes.compactContext && m.modes.compact {
		return nil
	}
	// the C toggle re-fetches the file keeping the cursor; turned off first,
	// it turns compact mode back on at the new size. The checks above only
	// word the refusals for a size change: should the toggle refuse anyway,
	// nothing changes
	prev, wasCompact := m.modes.compactContext, m.modes.compact
	m.modes.compactContext, m.modes.compact = n, false
	cmd := m.toggleCompactMode()
	if !m.modes.compact {
		m.modes.compactContext, m.modes.compact = prev, wasCompact
	}
	return cmd
}

// CompactContext is how many lines the changed blocks show around each change.
func (m Model) CompactContext() int {
	return m.modes.compactContext
}
