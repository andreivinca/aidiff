package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/keymap"
)

// folderBrowser is a file tree that browses a folder lazily
// (sidepane.FolderTree): its folder rows can be selected and opened. Every
// folder hook in the model goes through it; with a plain FileTree they are no-ops.
type folderBrowser interface {
	// SetFolders records a listing's directory rows ahead of the Rebuild with its files.
	SetFolders(folders []diff.FileEntry)
	// SelectedDirectory returns the folder row under the cursor and whether it is open.
	SelectedDirectory() (path string, expanded bool)
	// SetDirectory opens or closes a folder at the source; the listing must be reloaded.
	SetDirectory(path string, expanded bool)
}

// splitFolders hands a listing's directory rows to the folder browser and
// returns the files, which is all the rest of the model works with.
func (m Model) splitFolders(entries []diff.FileEntry) []diff.FileEntry {
	browser, ok := m.tree.(folderBrowser)
	if !ok {
		return entries
	}
	var folders, files []diff.FileEntry
	for _, entry := range entries {
		if entry.IsDir {
			folders = append(folders, entry)
		} else {
			files = append(files, entry)
		}
	}
	browser.SetFolders(folders)
	return files
}

// browsingFolders reports whether the tree is a folder browser, which stays
// on screen even when the folder lists a single file.
func (m Model) browsingFolders() bool {
	_, ok := m.tree.(folderBrowser)
	return ok
}

// selectedDirectory returns the folder row under the tree cursor, "" when
// the cursor is on a file or the tree browses no folders.
func (m Model) selectedDirectory() string {
	browser, ok := m.tree.(folderBrowser)
	if !ok {
		return ""
	}
	path, _ := browser.SelectedDirectory()
	return path
}

// handleFolderAction applies a tree action to the folder row under the
// cursor: confirm (enter, click) toggles it, scroll right opens it, scroll left
// closes it. handled is false when the cursor is not on a folder row or the
// action means nothing for folders, so the caller proceeds as usual.
func (m Model) handleFolderAction(action keymap.Action) (_ tea.Model, _ tea.Cmd, handled bool) {
	browser, ok := m.tree.(folderBrowser)
	if !ok {
		return m, nil, false
	}
	path, expanded := browser.SelectedDirectory()
	if path == "" {
		return m, nil, false
	}
	var cmd tea.Cmd
	switch action {
	case keymap.ActionConfirm:
		cmd = m.setDirectory(browser, path, !expanded)
	case keymap.ActionScrollRight:
		cmd = m.openDirectory(browser, path, expanded)
	case keymap.ActionScrollLeft:
		cmd = m.closeDirectory(browser, path, expanded)
	default:
		return m, nil, false
	}
	return m, cmd, true
}

// openDirectory opens a closed folder; an open one stays as it is.
func (m *Model) openDirectory(browser folderBrowser, path string, expanded bool) tea.Cmd {
	if expanded {
		return nil
	}
	return m.setDirectory(browser, path, true)
}

// closeDirectory closes an open folder; a closed one stays as it is.
func (m *Model) closeDirectory(browser folderBrowser, path string, expanded bool) tea.Cmd {
	if !expanded {
		return nil
	}
	return m.setDirectory(browser, path, false)
}

// setDirectory opens or closes a folder and reloads the listing. The tree
// keeps its cursor on the folder across the reload.
func (m *Model) setDirectory(browser folderBrowser, path string, expanded bool) tea.Cmd {
	browser.SetDirectory(path, expanded)
	m.filesLoadSeq++
	return m.loadFiles()
}

// toggleFocusedFolder toggles the folder row under the cursor when the tree
// has focus, for enter and a click; handled is false otherwise.
func (m Model) toggleFocusedFolder() (_ tea.Model, _ tea.Cmd, handled bool) {
	if m.layout.focus != paneTree {
		return m, nil, false
	}
	return m.handleFolderAction(keymap.ActionConfirm)
}
