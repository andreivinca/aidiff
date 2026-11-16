package diff

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
)

// FolderReader browses only the root and directories explicitly opened by the user.
// Symlinks are displayed as files and never traversed.
type FolderReader struct {
	root    string
	include []string
	exclude []string

	mu       sync.Mutex
	expanded map[string]bool // open directories, slash-separated and relative to root
	listings uint64          // listings started; each takes the next number

	watchMu sync.Mutex     // serializes watcher callbacks; never held together with mu
	watch   func([]string) // receives the directories a listing read; nil when detached
	watched uint64         // number of the listing whose directories watch last received
}

// NewFolderReader creates a reader for the folder at root, listing only paths
// permitted by the include/exclude prefixes.
func NewFolderReader(root string, include, exclude []string) *FolderReader {
	return &FolderReader{
		root:     root,
		include:  normalizePrefixes(include),
		exclude:  normalizePrefixes(exclude),
		expanded: make(map[string]bool),
	}
}

// SetDirectory opens or closes a folder. Closing it closes its descendants
// too. No filesystem work is done here: the next ChangedFiles lists the result.
func (r *FolderReader) SetDirectory(path string, expanded bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
// Passing nil detaches it; once SetDirectoryWatcher(nil) returns, the old
// callback is not running and will not be called again.
func (r *FolderReader) SetDirectoryWatcher(fn func([]string)) {
	r.watchMu.Lock()
	defer r.watchMu.Unlock()
	r.watch = fn
}

// ChangedFiles lists the root and every open directory: files, and directories
// as IsDir entries (Expanded when open), sorted by path. ref and staged are
// ignored, there being no VCS. An open directory that vanished or became
// unreadable is listed closed and forgotten; only a root failure is an error.
func (r *FolderReader) ChangedFiles(_ string, _ bool) ([]FileEntry, error) {
	r.mu.Lock()
	r.listings++
	listing := folderListing{reader: r, number: r.listings, expanded: maps.Clone(r.expanded)}
	r.mu.Unlock()

	if err := listing.read(""); err != nil {
		return nil, err
	}
	for _, path := range listing.unreadable {
		r.SetDirectory(path, false)
	}
	r.reportWatched(listing.number, listing.dirs)
	slices.SortFunc(listing.entries, func(a, b FileEntry) int {
		return strings.Compare(a.Path, b.Path)
	})
	return listing.entries, nil
}

// FileDiff returns the file's content as context lines.
func (r *FolderReader) FileDiff(req FileDiffRequest) ([]DiffLine, error) {
	return readFileAsContext(resolvePath(r.root, req.Path))
}

// reportWatched hands a listing's directories to the watcher unless a listing
// started later already did. It runs outside r.mu, so SetDirectory never
// waits behind the watcher's system calls.
func (r *FolderReader) reportWatched(number uint64, dirs []string) {
	r.watchMu.Lock()
	defer r.watchMu.Unlock()
	if r.watch == nil || number <= r.watched {
		return
	}
	r.watched = number
	r.watch(dirs)
}

// shows reports whether the include/exclude prefixes permit path. A directory
// is permitted while an include prefix lies below it, so the way to an
// included file stays browsable.
func (r *FolderReader) shows(path string, isDir bool) bool {
	if matchesPrefix(path, r.exclude) {
		return false
	}
	if len(r.include) == 0 || matchesPrefix(path, r.include) {
		return true
	}
	if !isDir {
		return false
	}
	for _, prefix := range r.include {
		if strings.HasPrefix(prefix, path+"/") {
			return true
		}
	}
	return false
}

// folderListing is one ChangedFiles pass over a snapshot of the open directories.
type folderListing struct {
	reader     *FolderReader
	number     uint64          // the listing's place in start order
	expanded   map[string]bool // open directories when the listing started
	entries    []FileEntry
	dirs       []string // absolute directories read, for the watcher
	unreadable []string // open directories that vanished or became unreadable
}

// read lists the directory rel (relative to the root, "" for the root) and
// recurses into its open subdirectories.
func (l *folderListing) read(rel string) error {
	full := filepath.Join(l.reader.root, filepath.FromSlash(rel))
	children, err := os.ReadDir(full)
	if err != nil {
		return fmt.Errorf("list folder %s: %w", rel, err)
	}
	l.dirs = append(l.dirs, full)
	for _, child := range children {
		if isVCSMetadata(child.Name()) {
			continue
		}
		path := filepath.ToSlash(filepath.Join(rel, child.Name()))
		if !l.reader.shows(path, child.IsDir()) {
			continue
		}
		entry := FileEntry{Path: path, IsDir: child.IsDir(), Expanded: child.IsDir() && l.expanded[path]}
		if entry.Expanded {
			if err := l.read(path); err != nil {
				if !isVanished(err) {
					return err
				}
				entry.Expanded = false
				l.unreadable = append(l.unreadable, path)
			}
		}
		l.entries = append(l.entries, entry)
	}
	return nil
}

// isVanished reports whether a directory read failed because the directory
// is gone, was replaced by a file, or is no longer readable.
func isVanished(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.ENOTDIR)
}

// isVCSMetadata reports whether name is a VCS metadata directory, which the
// folder browser never lists.
func isVCSMetadata(name string) bool {
	return name == ".git" || name == ".hg" || name == ".jj"
}
