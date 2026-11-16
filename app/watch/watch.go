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
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Config says what to watch.
type Config struct {
	Shallow bool     // watch only explicitly supplied directories
	Root    string   // worktree root
	GitDir  string   // absolute git directory; "" to skip
	Ignored []string // absolute directories to skip (git-ignored trees)
	// IsIgnored decides for directories created after startup. Nil means only
	// Ignored and .git are skipped.
	IsIgnored func(dir string) bool
	// Settle is how long changes must stop before they are reported, so a
	// burst (a checkout, a build, an editor's save) is one notification
	// rather than many. Zero reports every change at once.
	Settle time.Duration
}

// Watcher reports through Changes that something changed.
type Watcher struct {
	cfg     Config
	fsw     *fsnotify.Watcher
	ignored map[string]struct{}

	addWarning sync.Once // a directory that cannot be watched is logged once

	notify    chan struct{}
	done      chan struct{}
	exited    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// New starts watching. Close it when done.
func New(cfg Config) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify: %w", err)
	}
	w := &Watcher{
		cfg:     cfg,
		fsw:     fsw,
		ignored: make(map[string]struct{}, len(cfg.Ignored)),
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
		exited:  make(chan struct{}),
	}
	for _, d := range cfg.Ignored {
		w.ignored[filepath.Clean(d)] = struct{}{}
	}
	if err := w.watchRoot(); err != nil {
		_ = fsw.Close()
		return nil, err
	}
	if cfg.GitDir != "" {
		// the git dir itself (HEAD, index) and branch refs, not objects/ or logs/
		if err := fsw.Add(cfg.GitDir); err != nil {
			_ = fsw.Close()
			return nil, fmt.Errorf("watch %s: %w", cfg.GitDir, err)
		}
		_ = w.addTree(filepath.Join(cfg.GitDir, "refs", "heads"))
	}
	go w.loop()
	return w, nil
}

// Changes delivers a value once changes have settled. Bursts collapse into
// one value: the channel holds at most one.
func (w *Watcher) Changes() <-chan struct{} {
	return w.notify
}

// Close stops watching. It is safe to call more than once, concurrently too.
func (w *Watcher) Close() error {
	w.closeOnce.Do(func() {
		close(w.done)
		if err := w.fsw.Close(); err != nil {
			w.closeErr = fmt.Errorf("close watcher: %w", err)
		}
		<-w.exited
	})
	return w.closeErr
}

// SetDirectories reconciles a shallow watch with the directories currently browsed.
func (w *Watcher) SetDirectories(dirs []string) {
	wanted := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		wanted[filepath.Clean(dir)] = true
		if err := w.fsw.Add(dir); err != nil {
			w.warnUnwatched(dir, err)
		}
	}
	for _, dir := range w.fsw.WatchList() {
		if !wanted[filepath.Clean(dir)] {
			_ = w.fsw.Remove(dir)
		}
	}
}

// watchRoot starts watching the root: the whole tree, or only the root itself
// in shallow mode, where SetDirectories adds what is browsed below it.
func (w *Watcher) watchRoot() error {
	if !w.cfg.Shallow {
		return w.addTree(w.cfg.Root)
	}
	if err := w.fsw.Add(w.cfg.Root); err != nil {
		return fmt.Errorf("watch %s: %w", w.cfg.Root, err)
	}
	return nil
}

// loop turns events into notifications. It alone owns the settle timer, so
// none of its state needs a lock.
func (w *Watcher) loop() {
	defer close(w.exited)
	settle := time.NewTimer(time.Hour)
	settle.Stop()
	defer settle.Stop()
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if w.handle(ev) {
				w.changed(settle)
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				w.changed(settle) // events were dropped: force a full refresh
			}
		case <-settle.C:
			w.signal()
		}
	}
}

// changed reports a change now, or once Settle passes without another one.
func (w *Watcher) changed(settle *time.Timer) {
	if w.cfg.Settle <= 0 {
		w.signal()
		return
	}
	settle.Reset(w.cfg.Settle)
}

// signal makes a notification pending unless one already is.
func (w *Watcher) signal() {
	select {
	case w.notify <- struct{}{}:
	default: // a notification is already pending
	}
}

// handle reports whether ev changes what is reviewed, watching directories
// created under the root on the way.
func (w *Watcher) handle(ev fsnotify.Event) bool {
	if ev.Op == fsnotify.Chmod {
		return false // permission/timestamp-only changes do not change a diff
	}
	if w.cfg.GitDir != "" && within(ev.Name, w.cfg.GitDir) {
		if strings.HasSuffix(ev.Name, ".lock") {
			return false // git's transient lock files
		}
		if ev.Has(fsnotify.Create) && within(ev.Name, filepath.Join(w.cfg.GitDir, "refs", "heads")) && isDir(ev.Name) {
			_ = w.addTree(ev.Name) // new branch namespace, e.g. refs/heads/feature/
		}
		return true // HEAD, index or a branch ref: git state changed
	}
	if !w.cfg.Shallow && ev.Has(fsnotify.Create) && isDir(ev.Name) {
		if w.skipNewDir(ev.Name) {
			return false
		}
		_ = w.addTree(ev.Name)
	}
	return filepath.Clean(ev.Name) != filepath.Clean(w.cfg.Root) && within(ev.Name, w.cfg.Root)
}

// addTree watches dir and every directory below it that is not skipped. Only
// a failure on dir itself is an error; one below it is logged once and skipped.
func (w *Watcher) addTree(dir string) error {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
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
		if addErr := w.fsw.Add(path); addErr != nil {
			if path == dir {
				return fmt.Errorf("watch %s: %w", path, addErr)
			}
			w.warnUnwatched(path, addErr)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", dir, err)
	}
	return nil
}

// warnUnwatched logs the first directory that could not be watched. The
// usual cause is the inotify watch limit (ENOSPC), which then hits every
// directory after it, so one line says it all.
func (w *Watcher) warnUnwatched(dir string, err error) {
	w.addWarning.Do(func() {
		log.Printf("[WARN] file watching is incomplete, changes in %s and possibly other directories go unnoticed: %v", dir, err)
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

// within reports whether path is dir or lies below it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isDir(path string) bool {
	fi, err := os.Lstat(path) // Lstat: never follow symlinks into other trees
	return err == nil && fi.IsDir()
}
