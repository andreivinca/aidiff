package main

import (
	"log"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/watch"
)

// watchRepo starts the file watcher for the git repository at workDir and
// forwards its notifications to the program. It returns a stop function. If
// watching cannot start, it logs a warning and the review runs without it.
func watchRepo(workDir string, p *tea.Program) (stop func()) {
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
	w, err := watch.New(watch.Config{Root: root, GitDir: gitDir, Ignored: ignored, IsIgnored: g.IsIgnored})
	if err != nil {
		log.Printf("[WARN] file watching disabled: %v", err)
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-w.Changes():
				if paths, meta := w.Drain(); len(paths) > 0 || meta {
					p.Send(splitview.FilesChangedMsg{})
				}
			}
		}
	}()
	return func() {
		close(done)
		_ = w.Close()
	}
}
