package sidepane

import (
	"path/filepath"
	"sort"
	"strings"
)

// SelectedDirectory reports the actionable folder row under the cursor.
func (ft *FileTree) SelectedDirectory() string {
	if !ft.lazy || ft.cursor < 0 || ft.cursor >= len(ft.entries) {
		return ""
	}
	e := ft.entries[ft.cursor]
	if e.isDir {
		return e.path
	}
	return ""
}

func (ft *FileTree) buildFolderEntries(files []string) []treeEntry {
	children := make(map[string][]treeEntry)
	for _, dir := range ft.directories {
		marker := "▸ "
		if dir.Expanded {
			marker = "▾ "
		}
		parent := filepath.ToSlash(filepath.Dir(dir.Path))
		children[parent] = append(children[parent], treeEntry{name: marker + filepath.Base(dir.Path) + "/", path: dir.Path, isDir: true})
	}
	for _, path := range files {
		parent := filepath.ToSlash(filepath.Dir(path))
		children[parent] = append(children[parent], treeEntry{name: filepath.Base(path), path: path})
	}
	var rows []treeEntry
	var visit func(string)
	visit = func(parent string) {
		list := children[parent]
		sort.Slice(list, func(i, j int) bool {
			if list[i].isDir != list[j].isDir {
				return list[i].isDir
			}
			return list[i].path < list[j].path
		})
		for _, e := range list {
			e.name = strings.Repeat("  ", strings.Count(e.path, "/")) + e.name
			rows = append(rows, e)
			if e.isDir {
				visit(e.path)
			}
		}
	}
	visit(".")
	return rows
}
