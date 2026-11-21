package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os/exec"
	"strings"
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

// ignoreCheckTimeout bounds one git check-ignore of the folders created
// while the review runs.
const ignoreCheckTimeout = 10 * time.Second

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
	return watchInBackground(func(ctx context.Context) func() {
		return startRepoWatch(ctx, workDir, p)
	})
}

// startRepoWatch does watchRepo's setup and starts the watcher, unless ctx
// ends first.
func startRepoWatch(ctx context.Context, workDir string, p msgSender) (stop func()) {
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
	cfg := watch.Config{Root: root, GitDir: gitDir, Ignored: ignored, CheckIgnored: ignoredAmong(root), Settle: refreshSettle}
	return watchFiles(ctx, cfg, p, nil)
}

// ignoredAmong is a watch.Config.CheckIgnored for the repository at root:
// one git check-ignore for all of dirs. On failure it reports none ignored,
// and they are watched; the first failure is logged.
func ignoredAmong(root string) func(ctx context.Context, dirs []string) []string {
	var warn sync.Once
	return func(ctx context.Context, dirs []string) []string {
		var in bytes.Buffer
		for _, d := range dirs {
			in.WriteString(d)
			in.WriteByte(0)
		}
		ctx, cancel := context.WithTimeout(ctx, ignoreCheckTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "check-ignore", "--stdin", "-z")
		cmd.Dir = root
		cmd.Stdin = &in
		out, err := cmd.Output()
		if err != nil {
			exit, ok := errors.AsType[*exec.ExitError](err)
			if (!ok || exit.ExitCode() != 1) && ctx.Err() == nil { // 1: none is ignored; ctx: the watcher closed
				warn.Do(func() { log.Printf("[WARN] checking ignored directories: %v", err) })
			}
			return nil
		}
		var ignored []string
		for d := range strings.SplitSeq(string(out), "\x00") {
			if d != "" {
				ignored = append(ignored, d)
			}
		}
		return ignored
	}
}

// watchLazyFolder refreshes a plain folder when files are created, edited, or
// removed in the root or a directory opened in the tree. The watch is shallow
// and follows the directories each listing of reader actually read.
func watchLazyFolder(reader *diff.FolderReader, workDir string, p msgSender) (stop func()) {
	return watchFiles(context.Background(), watch.Config{Root: workDir, Shallow: true, Settle: refreshSettle}, p, reader)
}

// watchInBackground runs start off the caller's goroutine. The returned stop
// stops what start started; calling it again does nothing. It does not wait
// for a start still running, whose git calls and walk of the worktree take a
// while in a large repository and would hold up the exit: it ends start's
// ctx, and what start returns is stopped once it does.
func watchInBackground(start func(ctx context.Context) (stop func())) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan func(), 1)
	go func() {
		started <- start(ctx)
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			select {
			case stopWatch := <-started:
				stopWatch()
			default:
				go func() { (<-started)() }()
			}
		})
	}
}

// watchFiles starts a watcher for cfg, unless ctx ends during its setup, and
// sends the program a FilesChangedMsg for each settled change. With a reader,
// the watch follows the directories the reader lists. The returned stop may
// be called more than once.
func watchFiles(ctx context.Context, cfg watch.Config, p msgSender, reader *diff.FolderReader) (stop func()) {
	w, err := watch.New(ctx, cfg)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("[WARN] file watching disabled: %v", err)
		}
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
