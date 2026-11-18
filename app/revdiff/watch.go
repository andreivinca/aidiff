package main

import (
	"log"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/watch"
)

// refreshSettle is how long file changes must pause before the review
// refreshes, so a burst (an editor's save, a checkout, a build) refreshes once.
const refreshSettle = 150 * time.Millisecond

// msgSender is the program the watchers report to; *tea.Program is one.
type msgSender interface {
	Send(msg tea.Msg)
}

// watchRepo starts the file watcher for the git repository at workDir and
// forwards its notifications to the program. Setup lists git-ignored trees and
// walks the worktree, which takes a while in a large repository, so it runs in
// the background and the first frame does not wait for it. It returns a stop
// function. If watching cannot start, it logs a warning and the review runs
// without it.
func watchRepo(workDir string, p msgSender) (stop func()) {
	return watchInBackground(func() func() {
		return startRepoWatch(workDir, p)
	})
}

// startRepoWatch does watchRepo's setup and starts the watcher.
func startRepoWatch(workDir string, p msgSender) (stop func()) {
	g := diff.NewGit(workDir)
	root, gitDir, err := g.WatchDirs()
	if err != nil {
		log.Printf("[WARN] file watching disabled: %v", err)
		return func() {}
	}
	ignored, err := g.IgnoredDirs(root)
	if err != nil {
		log.Printf("[WARN] listing ignored directories: %v", err)
	}
	cfg := watch.Config{Root: root, GitDir: gitDir, Ignored: ignored, IsIgnored: g.IsIgnored, Settle: refreshSettle}
	return watchFiles(cfg, p, nil)
}

// watchLazyFolder refreshes a plain folder when files are created, edited, or
// removed in the root or a directory opened in the tree. The watch is shallow
// and follows the directories each listing of reader actually read.
func watchLazyFolder(reader *diff.FolderReader, workDir string, p msgSender) (stop func()) {
	return watchFiles(watch.Config{Root: workDir, Shallow: true, Settle: refreshSettle}, p, reader)
}

// watchInBackground runs start off the caller's goroutine. The returned stop
// waits for start to finish, then stops what it started; calling it again
// does nothing.
func watchInBackground(start func() (stop func())) (stop func()) {
	started := make(chan func(), 1)
	go func() {
		started <- start()
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			stopWatch := <-started
			stopWatch()
		})
	}
}

// watchFiles starts a watcher for cfg and sends the program a FilesChangedMsg
// for each settled change. With a reader, the watch follows the directories
// the reader lists. The returned stop may be called more than once.
func watchFiles(cfg watch.Config, p msgSender, reader *diff.FolderReader) (stop func()) {
	w, err := watch.New(cfg)
	if err != nil {
		log.Printf("[WARN] file watching disabled: %v", err)
		return func() {}
	}
	if reader != nil {
		reader.SetDirectoryWatcher(w.SetDirectories)
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-w.Changes():
				p.Send(splitview.FilesChangedMsg{})
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			if reader != nil {
				reader.SetDirectoryWatcher(nil) // detach before closing: listings may still run
			}
			_ = w.Close()
		})
	}
}
