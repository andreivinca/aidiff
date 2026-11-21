// Package watch reports file changes in a repository as they happen, using the
// operating system's change notifications (inotify, kqueue, FSEvents via
// fsnotify). Nothing is polled: with no changes, it does no work.
//
// The worktree is watched directory by directory, skipping git-ignored trees
// (node_modules, build output) and .git; the git directory itself and the
// branch refs are watched separately so commits and staging are noticed.
package watch

import (
	"context"
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
	// CheckIgnored returns those of dirs the VCS ignores, for directories
	// created after startup: a new tree is asked about a level at a time, a
	// level in one call. It runs off the event loop; ctx ends with the
	// watcher. Nil means only Ignored and .git are skipped.
	CheckIgnored func(ctx context.Context, dirs []string) []string
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

	// directories created after startup wait in newDirs for grow, which
	// watches them off the event loop and reports through grown
	newMu   sync.Mutex
	newDirs []string
	newDir  chan struct{} // holds a value while newDirs has work
	grown   chan struct{}
	ctx     context.Context // ends with Close, for CheckIgnored
	cancel  context.CancelFunc

	notify    chan struct{}
	done      chan struct{}
	exited    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// New starts watching. ctx bounds the setup, a walk of the whole worktree
// that takes a while in a large one: once ctx ends, New stops walking and
// returns its error. Close the watcher when done.
func New(ctx context.Context, cfg Config) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify: %w", err)
	}
	w := &Watcher{
		cfg:     cfg,
		fsw:     fsw,
		ignored: make(map[string]struct{}, len(cfg.Ignored)),
		newDir:  make(chan struct{}, 1),
		grown:   make(chan struct{}, 1),
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
		exited:  make(chan struct{}),
	}
	for _, d := range cfg.Ignored {
		w.ignored[filepath.Clean(d)] = struct{}{}
	}
	if err := w.watchRoot(ctx); err != nil {
		_ = fsw.Close()
		return nil, err
	}
	if cfg.GitDir != "" {
		// the git dir itself (HEAD, index) and branch refs, not objects/ or logs/
		if err := fsw.Add(cfg.GitDir); err != nil {
			_ = fsw.Close()
			return nil, fmt.Errorf("watch %s: %w", cfg.GitDir, err)
		}
		_ = w.addTree(ctx, filepath.Join(cfg.GitDir, "refs", "heads"))
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	go w.loop()
	if !cfg.Shallow {
		go w.grow()
	}
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
		w.cancel()
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
func (w *Watcher) watchRoot(ctx context.Context) error {
	if !w.cfg.Shallow {
		return w.addTree(ctx, w.cfg.Root)
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
		case <-w.grown:
			w.changed(settle)
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
			_ = w.addTree(context.Background(), ev.Name) // new branch namespace, e.g. refs/heads/feature/
		}
		return true // HEAD, index or a branch ref: git state changed
	}
	if !w.cfg.Shallow && ev.Has(fsnotify.Create) && isDir(ev.Name) {
		if !w.skipKnown(ev.Name) {
			w.queueNewDir(ev.Name)
		}
		return false // grow reports it once watched, unless it is ignored
	}
	return filepath.Clean(ev.Name) != filepath.Clean(w.cfg.Root) && within(ev.Name, w.cfg.Root)
}

// queueNewDir hands a directory created after startup to grow.
func (w *Watcher) queueNewDir(dir string) {
	w.newMu.Lock()
	w.newDirs = append(w.newDirs, dir)
	w.newMu.Unlock()
	select {
	case w.newDir <- struct{}{}:
	default: // grow already has work waiting
	}
}

// grow watches the directories created after startup, every one queued
// since its last pass in one go, and reports a change once a tree is
// watched. It runs apart from the event loop, which must not wait for the
// VCS: a checkout creates folders by the hundred.
func (w *Watcher) grow() {
	for {
		select {
		case <-w.done:
			return
		case <-w.newDir:
		}
		w.newMu.Lock()
		dirs := w.newDirs
		w.newDirs = nil
		w.newMu.Unlock()
		if w.addNewTrees(dirs) {
			select {
			case w.grown <- struct{}{}:
			default: // a report is already pending
			}
		}
	}
}

// addNewTrees watches the new directories dirs and the directories below
// them that are neither skipped nor ignored, reporting whether it watched
// any. It goes a level at a time, asking about each level in one
// CheckIgnored call before going below it, so an ignored tree moved in
// whole, node_modules say, is neither watched nor walked.
func (w *Watcher) addNewTrees(dirs []string) bool {
	watched := false
	for level := dirs; len(level) > 0; {
		var next []string
		for _, dir := range w.dropIgnored(level) {
			select {
			case <-w.done:
				return watched
			default:
			}
			if err := w.fsw.Add(dir); err != nil {
				if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fsnotify.ErrClosed) {
					w.warnUnwatched(dir, err) // gone already: nothing to watch, nothing to say
				}
				continue
			}
			watched = true
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if sub := filepath.Join(dir, e.Name()); e.IsDir() && !w.skipKnown(sub) { // IsDir: symlinks are not followed
					next = append(next, sub)
				}
			}
		}
		level = next
	}
	return watched
}

// dropIgnored is dirs without the ones the VCS ignores.
func (w *Watcher) dropIgnored(dirs []string) []string {
	if w.cfg.CheckIgnored == nil {
		return dirs
	}
	ignored := w.cfg.CheckIgnored(w.ctx, dirs)
	if len(ignored) == 0 {
		return dirs
	}
	skip := make(map[string]bool, len(ignored))
	for _, d := range ignored {
		skip[filepath.Clean(d)] = true
	}
	kept := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if !skip[filepath.Clean(d)] {
			kept = append(kept, d)
		}
	}
	return kept
}

// addTree watches dir and every directory below it that is not skipped,
// until ctx ends. Only a failure on dir itself is an error, and ctx ending;
// one below it is logged once and skipped.
func (w *Watcher) addTree(ctx context.Context, dir string) error {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr //nolint:wrapcheck // wrapped below with the walk
		}
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

// within reports whether path is dir or lies below it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isDir(path string) bool {
	fi, err := os.Lstat(path) // Lstat: never follow symlinks into other trees
	return err == nil && fi.IsDir()
}
