// Package watch reports file changes in a repository as they happen, using the
// operating system's change notifications (inotify, kqueue, FSEvents via
// fsnotify). Nothing is polled: with no changes, it does no work.
//
// The worktree is watched directory by directory, skipping git-ignored trees
// (node_modules, build output) and .git; the git directory itself and the
// branch refs are watched separately so commits and staging are noticed.
package watch

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Config says what to watch.
type Config struct {
	Root    string   // worktree root
	GitDir  string   // absolute git directory; "" to skip
	Ignored []string // absolute directories to skip (git-ignored trees)
	// IsIgnored decides for directories created after startup. Nil means only
	// Ignored and .git are skipped.
	IsIgnored func(dir string) bool
}

// Watcher collects changes until Drain is called.
type Watcher struct {
	cfg     Config
	fsw     *fsnotify.Watcher
	ignored map[string]struct{}

	mu    sync.Mutex
	paths map[string]struct{} // changed worktree paths, slash-separated, relative to Root
	meta  bool                // git state changed (HEAD, index, refs) or events were lost

	notify chan struct{}
	done   chan struct{}
	exited chan struct{}
}

// New starts watching. Close it when done.
func New(cfg Config) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		cfg:     cfg,
		fsw:     fsw,
		ignored: make(map[string]struct{}, len(cfg.Ignored)),
		paths:   make(map[string]struct{}),
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
		exited:  make(chan struct{}),
	}
	for _, d := range cfg.Ignored {
		w.ignored[filepath.Clean(d)] = struct{}{}
	}
	if err := w.addTree(cfg.Root); err != nil {
		_ = fsw.Close()
		return nil, err
	}
	if cfg.GitDir != "" {
		// the git dir itself (HEAD, index) and branch refs, not objects/ or logs/
		if err := fsw.Add(cfg.GitDir); err != nil {
			_ = fsw.Close()
			return nil, err
		}
		_ = w.addTree(filepath.Join(cfg.GitDir, "refs", "heads"))
	}
	go w.loop()
	return w, nil
}

// Changes delivers a value when changes are waiting to be drained. Bursts
// collapse into one value: the channel holds at most one.
func (w *Watcher) Changes() <-chan struct{} { return w.notify }

// Drain returns and clears what changed since the last call: worktree paths
// relative to Root (slash-separated), and whether git state changed.
func (w *Watcher) Drain() (paths []string, meta bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	paths = make([]string, 0, len(w.paths))
	for p := range w.paths {
		paths = append(paths, p)
	}
	clear(w.paths)
	meta, w.meta = w.meta, false
	return paths, meta
}

// Close stops watching.
func (w *Watcher) Close() error {
	select {
	case <-w.done:
		return nil
	default:
	}
	close(w.done)
	err := w.fsw.Close()
	<-w.exited
	return err
}

func (w *Watcher) loop() {
	defer close(w.exited)
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handle(ev)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				w.record("", true) // events were dropped: force a full refresh
			}
		}
	}
}

func (w *Watcher) handle(ev fsnotify.Event) {
	if ev.Op == fsnotify.Chmod {
		return // permission/timestamp-only changes do not change a diff
	}
	if w.cfg.GitDir != "" && within(ev.Name, w.cfg.GitDir) {
		if strings.HasSuffix(ev.Name, ".lock") {
			return // git's transient lock files
		}
		if ev.Has(fsnotify.Create) && within(ev.Name, filepath.Join(w.cfg.GitDir, "refs", "heads")) && isDir(ev.Name) {
			_ = w.addTree(ev.Name) // new branch namespace, e.g. refs/heads/feature/
		}
		w.record("", true)
		return
	}
	if ev.Has(fsnotify.Create) && isDir(ev.Name) {
		if w.skipNewDir(ev.Name) {
			return
		}
		_ = w.addTree(ev.Name)
	}
	rel, err := filepath.Rel(w.cfg.Root, ev.Name)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return
	}
	w.record(filepath.ToSlash(rel), false)
}

func (w *Watcher) record(path string, meta bool) {
	w.mu.Lock()
	if path != "" {
		w.paths[path] = struct{}{}
	}
	w.meta = w.meta || meta
	w.mu.Unlock()
	select {
	case w.notify <- struct{}{}:
	default: // a notification is already pending
	}
}

// addTree watches dir and every directory below it that is not skipped.
func (w *Watcher) addTree(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			return nil // vanished or unreadable subdirectory: skip it
		}
		if !d.IsDir() {
			return nil
		}
		if path != dir && w.skipKnown(path) {
			return filepath.SkipDir
		}
		if addErr := w.fsw.Add(path); addErr != nil && path == dir {
			return addErr
		}
		return nil
	})
}

// skipKnown skips .git directories and the trees git ignored at startup.
func (w *Watcher) skipKnown(dir string) bool {
	if filepath.Base(dir) == ".git" || (w.cfg.GitDir != "" && filepath.Clean(dir) == filepath.Clean(w.cfg.GitDir)) {
		return true
	}
	_, ok := w.ignored[filepath.Clean(dir)]
	return ok
}

// skipNewDir also asks the VCS about directories that did not exist at startup.
func (w *Watcher) skipNewDir(dir string) bool {
	if w.skipKnown(dir) {
		return true
	}
	return w.cfg.IsIgnored != nil && w.cfg.IsIgnored(dir)
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isDir(path string) bool {
	fi, err := os.Lstat(path) // Lstat: never follow symlinks into other trees
	return err == nil && fi.IsDir()
}
