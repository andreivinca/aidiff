package diff

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FolderReader browses only the root and directories explicitly opened by the user.
// Symlinks are displayed as files and never traversed.
type FolderReader struct {
	root     string
	mu       sync.Mutex
	include  []string
	exclude  []string
	expanded map[string]bool
	watch    func([]string)
}

func NewFolderReader(root string, include, exclude []string) *FolderReader {
	return &FolderReader{root: root, expanded: make(map[string]bool), include: normalizePrefixes(include), exclude: normalizePrefixes(exclude)}
}

// ToggleDirectory changes visibility without doing filesystem work on the UI goroutine.
func (r *FolderReader) ToggleDirectory(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setDirectory(path, !r.expanded[path])
}

// SetDirectory opens or closes a folder without toggling an already matching state.
func (r *FolderReader) SetDirectory(path string, expanded bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setDirectory(path, expanded)
}

func (r *FolderReader) setDirectory(path string, expanded bool) {
	if expanded {
		r.expanded[path] = true
		return
	}
	for dir := range r.expanded {
		if dir == path || strings.HasPrefix(dir, path+"/") {
			delete(r.expanded, dir)
		}
	}
}

// SetDirectoryWatcher installs a callback for the directories actually read.
// Passing nil detaches it before closing the watcher.
func (r *FolderReader) SetDirectoryWatcher(fn func([]string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.watch = fn
}

func (r *FolderReader) ChangedFiles(_ string, _ bool) ([]FileEntry, error) {
	r.mu.Lock()
	expanded := maps.Clone(r.expanded)
	r.mu.Unlock()
	var entries []FileEntry
	var dirs []string
	var read func(string) error
	read = func(rel string) error {
		full := filepath.Join(r.root, filepath.FromSlash(rel))
		children, err := os.ReadDir(full)
		if err != nil {
			return fmt.Errorf("list folder %s: %w", rel, err)
		}
		dirs = append(dirs, full)
		for _, child := range children {
			if isVCSMetadata(child.Name()) {
				continue
			}
			path := filepath.ToSlash(filepath.Join(rel, child.Name()))
			if matchesPrefix(path, r.exclude) {
				continue
			}
			allowed := len(r.include) == 0 || matchesPrefix(path, r.include)
			if child.IsDir() {
				for _, prefix := range r.include {
					if strings.HasPrefix(prefix, path+"/") {
						allowed = true
					}
				}
			}
			if !allowed {
				continue
			}
			opened := child.IsDir() && expanded[path]
			entries = append(entries, FileEntry{Path: path, IsDir: child.IsDir(), Expanded: opened})
			if opened {
				if err := read(path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	err := read("")
	r.mu.Lock()
	if r.watch != nil && maps.Equal(expanded, r.expanded) {
		r.watch(dirs)
	}
	r.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, err
}

func (r *FolderReader) FileDiff(req FileDiffRequest) ([]DiffLine, error) {
	return readFileAsContext(resolvePath(r.root, req.Path))
}
