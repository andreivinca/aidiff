package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/localhist"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/t3palette"
)

// Where a run starts. A run that names what to review (a folder, refs, a
// flag that picks a review) starts there, the saved tabs around it, as
// before. A run that names nothing starts on the tab the last run had on
// screen, without adding the folder it was started in: a folder opens at
// once, as the history panel opens one; a T3 project is shown connecting and
// opens once its server answers (t3palette.StartTab). When that tab cannot
// come back, the next one that can is used, and with none, or no saved
// tabs, the working directory, as a run of revdiff opens it.

// runStart is how a run starts: its options, with the folder it reviews
// settled, the saved tabs, read once, and the saved T3 tab it starts on.
type runStart struct {
	opts options
	tabs t3palette.TabsConfig
	tab  *t3palette.StartTab // nil: the run reviews a folder or a target of its own
}

// reachWait bounds checking that a saved folder can be reviewed before the
// run starts on it: a stat of a stale network mount can hang.
const reachWait = 2 * time.Second

// planStart settles where the run starts (see above), with the tabs kept in
// store.
func planStart(opts options, store localhist.Tabs) (runStart, error) {
	cwd := workingDir()
	opts, err := resolveStartArg(opts, cwd)
	if err != nil {
		return runStart{}, err
	}
	load := sync.OnceValues(func() ([]t3palette.Tab, int) {
		return loadTabs(store)
	})
	plan := runStart{opts: opts, tabs: openTabs(store, load)}
	if opts.namesTarget() {
		return plan, nil
	}
	saved, current := load()
	dir, tab := startTab(saved, current, reachable)
	switch {
	case dir != "":
		plan.opts.folder = dir
	case tab != nil:
		plan.tab = &t3palette.StartTab{Tab: *tab, Fallback: workDirFolder(cwd)}
	}
	return plan, nil
}

// namesTarget reports whether the run names what to review: a folder or
// refs; a flag that picks a review (--staged, --all-files, --only, --stdin,
// a compare pair, a T3 server or thread); or one that hands the review of
// this folder to or from a caller (--annotations, -o), as a launcher does.
// --terminal and the view flags name nothing.
func (o options) namesTarget() bool {
	return o.folder != "" || o.Refs.Base != "" || o.Refs.Against != "" || o.Staged || o.AllFiles || len(o.Only) > 0 ||
		o.Stdin || o.CompareOld != "" || o.CompareNew != "" || o.t3Mode() || o.Annotations != "" || o.Output != ""
}

// startTab is the saved tab a run that names nothing starts on: the one at
// current, the tab the last run had on screen, else the next one that can
// come back, a folder reach says can be reviewed (dir) or a project of a
// server aidiff can name (tab); neither when none can.
func startTab(saved []t3palette.Tab, current int, reach func(dir string) bool) (dir string, tab *t3palette.Tab) {
	n := len(saved)
	for i := range n {
		t := saved[(max(current, 0)+i)%n]
		switch {
		case t.Dir != "":
			if reach(t.Dir) {
				return t.Dir, nil
			}
		case t.Server != "" && t.ProjectID != "":
			if _, err := t3client.ServerLabel(t.Server); err == nil {
				return "", &t
			}
		}
	}
	return "", nil
}

// reachable reports whether folder dir can be reviewed: a folder this user
// can list, answering within reachWait. A variable for the tests.
var reachable = func(dir string) bool {
	ok := make(chan bool, 1)
	go func() { ok <- canList(dir) }()
	select {
	case r := <-ok:
		return r
	case <-time.After(reachWait):
		return false
	}
}

// canList reports whether dir is a folder this user can list.
func canList(dir string) bool {
	f, err := os.Open(dir) //nolint:gosec // a folder the user had open in a tab
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if fi, serr := f.Stat(); serr != nil || !fi.IsDir() {
		return false
	}
	_, err = f.Readdirnames(1)
	return err == nil || errors.Is(err, io.EOF)
}

// workingDir is the folder the run was started in.
func workingDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// workDirFolder is the folder a run started in cwd reviews, as the history
// panel keeps it: its repository's root, else cwd itself.
func workDirFolder(cwd string) string {
	if vcs, root := diff.DetectVCS(cwd); vcs != diff.VCSNone {
		return filepath.Clean(root)
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return abs
	}
	return cwd
}

// source is the run's diff source: none for a saved T3 tab, which the
// palette opens once the program runs and its server answers.
func (r runStart) source() (sourceSetup, error) {
	if r.tab != nil {
		return sourceSetup{}, nil
	}
	return setupSource(r.opts)
}
