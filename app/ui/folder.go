package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/umputun/revdiff/app/keymap"
)

// toggleSelectedDirectory schedules a shallow folder reload, retaining the tree cursor.
func (m Model) toggleSelectedDirectory(expanded ...bool) (tea.Model, tea.Cmd) {
	tree, ok := m.tree.(interface{ SelectedDirectory() string })
	if !ok {
		return m, nil
	}
	path := tree.SelectedDirectory()
	source, ok := m.diffRenderer.(interface{ ToggleDirectory(string) })
	if !ok || path == "" {
		return m, nil
	}
	if len(expanded) > 0 {
		if setter, ok := m.diffRenderer.(interface{ SetDirectory(string, bool) }); ok {
			setter.SetDirectory(path, expanded[0])
		}
	} else {
		source.ToggleDirectory(path)
	}
	m.filesLoadSeq++
	return m, m.loadFiles()
}

func (m Model) selectedDirectory() string {
	if tree, ok := m.tree.(interface{ SelectedDirectory() string }); ok {
		return tree.SelectedDirectory()
	}
	return ""
}

// handleDirectoryAction opens or closes the selected folder.
func (m Model) handleDirectoryAction(action keymap.Action) (tea.Model, tea.Cmd) {
	switch action {
	case keymap.ActionScrollRight:
		return m.toggleSelectedDirectory(true)
	case keymap.ActionScrollLeft:
		return m.toggleSelectedDirectory(false)
	default:
		return m.toggleSelectedDirectory()
	}
}

func (m Model) isDirectoryAction(action keymap.Action) bool {
	return action == keymap.ActionConfirm || action == keymap.ActionScrollRight || action == keymap.ActionScrollLeft
}
