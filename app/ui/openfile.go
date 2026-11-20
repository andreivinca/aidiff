package ui

import (
	"context"
	"path"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
)

// aidiff's palette finds files by name (ctrl+p, no '>') and opens them, and
// the View menu keeps the file on screen when it switches between a review's
// changes and every file of its folder.

// OpenFileMsg selects the file at Path in the tree and shows it. A folder
// browser opens the folders above it first. A review that does not list the
// file stays as it is; one still loading its file list selects the file once
// the list arrives, if it is there.
type OpenFileMsg struct {
	Path  string
	Focus bool // move the focus to the diff pane
}

// FileSearcher is a folder browser's source that finds files of folders the
// tree has not opened yet.
type FileSearcher interface {
	SearchFiles(ctx context.Context, query string, limit int) (diff.FoundPaths, error)
}

// CurrentFile is the file the diff pane shows, "" for none.
func (m Model) CurrentFile() string {
	return m.file.name
}

// ListsChanges reports whether the review lists the changed files, not
// every file of a folder: the palette then also searches the whole project.
func (m Model) ListsChanges() bool {
	return !m.browsingFolders()
}

// FindFiles returns the work that finds the files matching query, at most
// limit of them, best first. A review of changes ranks the files its tree
// lists, now, so the work only hands them over; a folder browser asks its
// source, which also knows the files of the folders that are not open.
func (m Model) FindFiles(query string, limit int) func(context.Context) (diff.FoundPaths, error) {
	if s, ok := m.diffRenderer.(FileSearcher); ok && m.browsingFolders() {
		return func(ctx context.Context) (diff.FoundPaths, error) {
			return s.SearchFiles(ctx, query, limit) //nolint:wrapcheck // the source says what failed
		}
	}
	found := diff.RankPaths(query, m.tree.VisibleFiles(), limit)
	return func(context.Context) (diff.FoundPaths, error) {
		return found, nil
	}
}

// handleOpenFile shows the file msg names, or remembers it until the file
// list it may be on has loaded.
func (m Model) handleOpenFile(msg OpenFileMsg) (tea.Model, tea.Cmd) {
	m.openFile = OpenFileMsg{}
	if msg.Path == "" {
		return m, nil
	}
	if m.filesLoaded && m.tree.SelectByPath(msg.Path) {
		m.clearPendingJumps()
		m.tree.EnsureVisible(m.treePageSize())
		if msg.Focus {
			m.layout.focus = paneDiff
		}
		return m.loadSelectedIfChanged()
	}
	browser, ok := m.tree.(folderBrowser)
	if !ok {
		if !m.filesLoaded {
			m.openFile = msg // the first list says whether it is among the files
		}
		return m, nil
	}
	for dir := path.Dir(msg.Path); dir != "." && dir != "/"; dir = path.Dir(dir) {
		browser.SetDirectory(dir, true)
	}
	m.openFile = msg
	m.filesLoadSeq++ // a listing still under way lacks the folders just opened
	return m, m.loadFiles()
}

// applyOpenFile selects the file an OpenFileMsg left waiting for the file
// list, when the list has it; handleFilesLoaded then shows it.
func (m *Model) applyOpenFile() {
	msg := m.openFile
	m.openFile = OpenFileMsg{}
	if msg.Path == "" || !m.tree.SelectByPath(msg.Path) {
		return
	}
	m.clearPendingJumps()
	m.tree.EnsureVisible(m.treePageSize())
	if msg.Focus {
		m.layout.focus = paneDiff
	}
}
