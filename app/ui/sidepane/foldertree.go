package sidepane

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/ui/style"
)

// FolderSource opens and closes the folders a FolderTree lists; the change
// shows in its next listing. Implemented by *diff.FolderReader.
type FolderSource interface {
	SetDirectory(path string, expanded bool)
}

// FolderTree browses a folder lazily: folders are rows of their own, which the
// cursor stops on and which open to list their children. It is a FileTree
// (reviewed marks, filters, file stepping) whose rows, while no filter is on,
// are the folder hierarchy; an active filter shows the FileTree's view of the
// listed files. Folder rows carry their path in treeEntry.path, so
// SelectedFile is overridden to report none on them.
type FolderTree struct {
	*FileTree
	source  FolderSource
	folders []diff.FileEntry // directory rows of the listing being shown, from SetFolders
}

// NewFolderTree creates an empty folder browser over source. Its rows arrive
// with SetFolders and Rebuild once the folder is listed.
func NewFolderTree(source FolderSource) *FolderTree {
	return &FolderTree{FileTree: NewFileTree(nil), source: source}
}

// SetFolders records the directory rows of a listing. Call it before the
// Rebuild that receives the same listing's files.
func (t *FolderTree) SetFolders(folders []diff.FileEntry) {
	t.folders = folders
}

// SetDirectory opens or closes a folder at the source. The caller reloads the
// listing to show it.
func (t *FolderTree) SetDirectory(path string, expanded bool) {
	t.source.SetDirectory(path, expanded)
}

// SelectedDirectory returns the folder row under the cursor and whether it is
// open. path is empty on a file row and while a filter is on.
func (t *FolderTree) SelectedDirectory() (path string, expanded bool) {
	if !t.browsing() || t.cursor < 0 || t.cursor >= len(t.entries) || !t.entries[t.cursor].isDir {
		return "", false
	}
	path = t.entries[t.cursor].path
	for _, folder := range t.folders {
		if folder.Path == path {
			return path, folder.Expanded
		}
	}
	return path, false
}

// SelectedFile returns the full path of the selected file, or "" when the
// cursor is on a folder row.
func (t *FolderTree) SelectedFile() string {
	if path, _ := t.SelectedDirectory(); path != "" {
		return ""
	}
	return t.FileTree.SelectedFile()
}

// Move navigates the cursor. While browsing, folder rows are stops too; with
// a filter on, the FileTree's file-to-file motions apply.
func (t *FolderTree) Move(m Motion, count ...int) {
	if !t.browsing() || len(t.entries) == 0 {
		t.FileTree.Move(m, count...)
		return
	}
	n := 1
	if len(count) > 0 {
		n = count[0]
	}
	last := len(t.entries) - 1
	switch m {
	case MotionUp:
		t.cursor = max(t.cursor-1, 0)
	case MotionDown:
		t.cursor = min(t.cursor+1, last)
	case MotionPageUp:
		t.cursor = max(t.cursor-n, 0)
	case MotionPageDown:
		t.cursor = min(t.cursor+n, last)
	case MotionFirst:
		t.cursor = 0
	case MotionLast:
		t.cursor = last
	}
}

// Rebuild shows a listing's files (its folders came through SetFolders),
// keeping the selected folder or file at the same row. Reviewed marks on files
// under closed folders survive: closing a folder only hides them.
func (t *FolderTree) Rebuild(entries []diff.FileEntry) {
	folder, _ := t.SelectedDirectory()
	file, row := t.SelectedFile(), t.visibleRow()
	hidden := t.hiddenReviewed()
	t.FileTree.Rebuild(entries)
	maps.Copy(t.reviewed, hidden)
	if t.browsing() {
		t.showFolderRows(folder, file, row)
	}
}

// ReconcileReviewed validates the marks captured when a listing began, except
// those on files under closed folders, which the listing could not see.
func (t *FolderTree) ReconcileReviewed(before, current map[string]string) {
	visible := maps.Clone(before)
	maps.DeleteFunc(visible, func(path, _ string) bool {
		return t.underClosedFolder(path)
	})
	t.FileTree.ReconcileReviewed(visible, current)
}

// ToggleFilter switches the annotated-only filter, restoring the folder rows
// when it goes off.
func (t *FolderTree) ToggleFilter(annotated map[string]bool) {
	browsing := t.browsing()
	t.FileTree.ToggleFilter(annotated)
	t.restoreFolderRows(browsing)
}

// ToggleUnreviewedFilter switches the unreviewed-only filter, restoring the
// folder rows when it goes off.
func (t *FolderTree) ToggleUnreviewedFilter() {
	browsing := t.browsing()
	t.FileTree.ToggleUnreviewedFilter()
	t.restoreFolderRows(browsing)
}

// RefreshFilter refreshes the annotated-only view, restoring the folder rows
// when no annotated file is left and the filter goes off.
func (t *FolderTree) RefreshFilter(annotated map[string]bool) {
	browsing := t.browsing()
	t.FileTree.RefreshFilter(annotated)
	t.restoreFolderRows(browsing)
}

// Render draws the visible rows; a selected folder row is highlighted like a
// selected file. With a filter on, the FileTree draws its own view.
func (t *FolderTree) Render(r FileTreeRender) string {
	if !t.browsing() {
		return t.FileTree.Render(r)
	}
	if len(t.entries) == 0 {
		return "  empty folder"
	}
	t.followCursor(r.Height)
	end := min(t.offset+r.Height, len(t.entries))
	rc := renderCtx{annotatedFiles: r.Annotated, res: r.Resolver, rnd: r.Renderer}
	var b strings.Builder
	for idx := t.offset; idx < end; idx++ {
		e := t.entries[idx]
		if e.isDir {
			b.WriteString(t.renderFolderRow(e, idx, r))
		} else {
			b.WriteString(t.renderFileEntry(e, idx, r.Width, rc))
		}
		if idx < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// browsing reports whether the rows are the folder hierarchy: no filter is on.
func (t *FolderTree) browsing() bool {
	return !t.filter && !t.unreviewed
}

// restoreFolderRows brings the folder rows back after a filter went off,
// keeping the file the FileTree selected.
func (t *FolderTree) restoreFolderRows(wasBrowsing bool) {
	if wasBrowsing || !t.browsing() {
		return
	}
	t.showFolderRows("", t.FileTree.SelectedFile(), t.visibleRow())
}

// showFolderRows replaces the rows with the folder hierarchy and selects
// folder, else file, else the first row, at the given row of the window.
func (t *FolderTree) showFolderRows(folder, file string, row int) {
	t.entries = t.folderRows()
	t.cursor = 0
	t.offset = 0
	for i, e := range t.entries {
		if (folder != "" && e.isDir && e.path == folder) || (folder == "" && file != "" && !e.isDir && e.path == file) {
			t.cursor = i
			t.offset = max(i-max(row, 0), 0)
			return
		}
	}
}

// folderRows lays out the folders and files depth first, folders before
// files at each level, indented by depth.
func (t *FolderTree) folderRows() []treeEntry {
	children := make(map[string][]treeEntry)
	for _, folder := range t.folders {
		marker := "▸ "
		if folder.Expanded {
			marker = "▾ "
		}
		parent := filepath.ToSlash(filepath.Dir(folder.Path))
		children[parent] = append(children[parent], treeEntry{name: marker + filepath.Base(folder.Path) + "/", path: folder.Path, isDir: true})
	}
	for _, path := range t.allFiles {
		parent := filepath.ToSlash(filepath.Dir(path))
		children[parent] = append(children[parent], treeEntry{name: filepath.Base(path), path: path})
	}
	return t.appendRows(nil, children, ".")
}

// appendRows appends the rows below parent, recursing into folders.
func (t *FolderTree) appendRows(rows []treeEntry, children map[string][]treeEntry, parent string) []treeEntry {
	level := children[parent]
	slices.SortFunc(level, func(a, b treeEntry) int {
		if a.isDir != b.isDir {
			if a.isDir {
				return -1
			}
			return 1
		}
		return strings.Compare(a.path, b.path)
	})
	for _, e := range level {
		e.depth = strings.Count(e.path, "/")
		e.name = strings.Repeat("  ", e.depth) + e.name
		rows = append(rows, e)
		if e.isDir {
			rows = t.appendRows(rows, children, e.path)
		}
	}
	return rows
}

// renderFolderRow draws one folder row, highlighted under the cursor. The
// name is sanitized: a newline or an escape sequence in it would add a row
// or reach the terminal, breaking the pane's height and its click mapping.
func (t *FolderTree) renderFolderRow(e treeEntry, idx int, r FileTreeRender) string {
	key := style.StyleKeyDirEntry
	if idx == t.cursor {
		key = style.StyleKeyFileSelected
	}
	return r.Resolver.Style(key).Render(" " + t.truncateDirName(style.SanitizeFilenameForDisplay(e.name), r.Width-3))
}

// hiddenReviewed returns the reviewed marks on files under closed folders.
func (t *FolderTree) hiddenReviewed() map[string]string {
	hidden := make(map[string]string)
	for path, fingerprint := range t.reviewed {
		if t.underClosedFolder(path) {
			hidden[path] = fingerprint
		}
	}
	return hidden
}

// underClosedFolder reports whether path lies below a closed folder of the listing.
func (t *FolderTree) underClosedFolder(path string) bool {
	for _, folder := range t.folders {
		if !folder.Expanded && strings.HasPrefix(path, folder.Path+"/") {
			return true
		}
	}
	return false
}
