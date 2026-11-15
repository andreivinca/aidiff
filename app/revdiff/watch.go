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
	return watchFiles(watch.Config{Root: root, GitDir: gitDir, Ignored: ignored, IsIgnored: g.IsIgnored}, p)
}

// watchFolder refreshes a plain folder when files are created, edited, or removed.
func watchFolder(workDir string, p *tea.Program) (stop func()) {
	return watchFiles(watch.Config{Root: workDir}, p)
}

func watchLazyFolder(reader *diff.FolderReader, workDir string, p *tea.Program) func() {
	if reader == nil {
		return watchFolder(workDir, p)
	}
	return watchFilesWithReader(watch.Config{Root: workDir, Shallow: true}, p, reader)
}

func watchFiles(cfg watch.Config, p *tea.Program) func() { return watchFilesWithReader(cfg, p, nil) }

func watchFilesWithReader(cfg watch.Config, p *tea.Program, reader *diff.FolderReader) (stop func()) {
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
				if paths, meta := w.Drain(); len(paths) > 0 || meta {
					p.Send(splitview.FilesChangedMsg{})
				}
			}
		}
	}()
	return func() {
		close(done)
		if reader != nil {
			reader.SetDirectoryWatcher(nil)
		}
		_ = w.Close()
	}
}
