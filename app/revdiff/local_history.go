package main

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/localhist"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/t3palette"
)

// The history panel lists the folders of this machine aidiff reviewed, kept
// in history.json beside the config file. A folder opened from it is
// reviewed the way a run started there with no arguments would be: its
// uncommitted changes, or every file of a plain folder.

// localHistoryStore is where the reviewed folders are kept.
func localHistoryStore() localhist.Store {
	return localhist.Store{Path: filepath.Join(filepath.Dir(defaultConfigPath()), "history.json")}
}

// historyDir is the folder the run's review shows, to list in the history:
// a repository's root or a plain folder; "" for stdin, a compare pair, a T3
// thread, or files reviewed with no repository around them.
func (s sourceSetup) historyDir(opts options) string {
	if s.t3 != nil || opts.Stdin || opts.CompareOld != "" || s.workDir == "" {
		return ""
	}
	if s.folder == nil && s.vcsType == diff.VCSNone {
		return ""
	}
	if s.gitRoot != "" {
		return filepath.Clean(s.gitRoot)
	}
	if abs, err := filepath.Abs(s.workDir); err == nil {
		return abs
	}
	return ""
}

// folderOptions are the run's options for a folder opened from the history:
// what the run was asked to compare (refs, --staged), and the files it was
// narrowed to (--only, --include, --exclude), are the run's own folder's, so
// they are left out.
func folderOptions(opts options) options {
	opts.Refs.Base, opts.Refs.Against, opts.Staged = "", "", false
	opts.Only, opts.AllFiles = nil, false
	opts.Include, opts.Exclude = nil, nil
	opts.Stdin, opts.StdinName = false, ""
	opts.CompareOld, opts.CompareNew, opts.compareAbsOld, opts.compareAbsNew = "", "", "", ""
	opts.T3, opts.T3Thread = "", ""
	opts.Annotations = ""
	return opts
}

// setupFolderSource is the diff source of folder dir: its repository, else
// the folder itself.
func setupFolderSource(opts options, dir string) (sourceSetup, error) {
	if folder, ok, err := setupFolderIn(opts, dir); ok || err != nil {
		return folder, err
	}
	setup, err := setupVCSRendererIn(opts, dir)
	if err != nil {
		return sourceSetup{}, err
	}
	setup.untrackedFn = filterUntracked(setup.untrackedFn, opts.Include, opts.Exclude)
	return sourceSetup{vcsSetup: setup}, nil
}

// localDiffs are the Diff View… commands of the local folders' reviews, by
// folder.
type localDiffs struct {
	mu    sync.Mutex
	byDir map[string]*localDiff
}

// put records l as the Diff View… of dir; a nil l is not recorded.
func (d *localDiffs) put(dir string, l *localDiff) {
	if dir == "" || l == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byDir == nil {
		d.byDir = map[string]*localDiff{}
	}
	d.byDir[dir] = l
}

// get is the Diff View… of dir, nil when it has none or d is nil.
func (d *localDiffs) get(dir string) *localDiff {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.byDir[dir]
}

// newLocalConfig is how the palette reaches this machine's folders: the
// history, and the review of each folder. src is the run's own source, whose
// folder starts on top of the list with start, the Diff View… of the run's
// review, recorded in diffs.
func newLocalConfig(deps reviewDeps, src sourceSetup, diffs *localDiffs, start *localDiff) t3palette.LocalConfig {
	store := localHistoryStore()
	fdeps := deps
	fdeps.opts = folderOptions(deps.opts)
	cfg := t3palette.LocalConfig{
		History: func() []string {
			list, err := store.List()
			if err != nil {
				log.Printf("[WARN] %v", err)
			}
			dirs := make([]string, 0, len(list))
			for _, e := range list {
				dirs = append(dirs, e.Dir)
			}
			return dirs
		},
		Remember: func(dir string) {
			if err := store.Add(dir); err != nil {
				log.Printf("[WARN] %v", err)
			}
		},
		Open: func(dir string) (t3palette.LocalReview, error) {
			if rd, ok := t3client.ParseRemoteDir(dir); ok {
				// reached, and its shell known before a terminal opens there
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				_, serr := t3client.DetectShell(ctx, rd.Host)
				cancel()
				if serr != nil {
					return t3palette.LocalReview{}, serr
				}
				rsrc := remoteFolderSource(rd)
				model, err := fdeps.newModel(rsrc, annotation.NewStore(), "")
				if err != nil {
					return t3palette.LocalReview{}, err
				}
				return t3palette.LocalReview{Model: model, Watch: remoteWatch(fdeps.opts, rsrc)}, nil
			}
			fsrc, err := setupFolderSource(fdeps.opts, dir)
			if err != nil {
				return t3palette.LocalReview{}, err
			}
			model, err := fdeps.newModel(fsrc, annotation.NewStore(), "")
			if err != nil {
				return t3palette.LocalReview{}, err
			}
			diffs.put(dir, newLocalDiff(fdeps, fsrc, ""))
			return t3palette.LocalReview{Model: model, Watch: sourceWatch(fdeps.opts, fsrc)}, nil
		},
	}
	startDir := src.historyDir(deps.opts)
	cfg.SaveHistory = func(dir, annotations string, files []string) {
		if t3client.IsRemoteDir(dir) {
			return // history files a review by a folder of this machine
		}
		h, err := folderHistory(diffs.get(dir).compared(fdeps.opts), dir)
		if dir == startDir {
			h, err = src.history(diffs.get(dir).compared(deps.opts), nil), nil // the run's own source
		}
		if err != nil {
			log.Printf("[WARN] history: %v", err)
			return
		}
		h.annotations, h.files = annotations, files
		saveHistory(h)
	}
	if dir := startDir; dir != "" {
		diffs.put(dir, start)
		cfg.Start = &t3palette.LocalWorkspace{Dir: dir, Watch: sourceWatch(deps.opts, src)}
	}
	return cfg
}

// sourceWatch starts src's live refresh for the palette.
func sourceWatch(opts options, src sourceSetup) func(p t3palette.Program) func() {
	return func(p t3palette.Program) func() {
		return src.watch(opts, p)
	}
}

// folderHistory is how the annotations of folder dir's review are filed.
func folderHistory(opts options, dir string) (histReq, error) {
	vcsType, root := diff.DetectVCS(dir)
	switch {
	case dir == "":
		return histReq{}, errors.New("no folder")
	case vcsType == diff.VCSGit:
		return histReq{opts: opts, gitRoot: root, workDir: root}, nil
	case vcsType != diff.VCSNone:
		return histReq{opts: opts, workDir: root}, nil
	}
	return histReq{opts: opts, workDir: dir}, nil
}

// exitHistory is how the annotations of the review on screen at exit are
// filed: a T3 review's by its server, a folder opened from the history by
// that folder, else as the run's own source; a local one compared as its
// Diff View… in diffs last left it.
func (s sourceSetup) exitHistory(opts options, t3p *t3palette.Palette, diffs *localDiffs) histReq {
	if r := t3p.OnScreen(); r != nil {
		return s.history(opts, r)
	}
	startDir := s.historyDir(opts)
	if dir := t3p.LocalDir(); dir != "" && dir != startDir {
		if h, err := folderHistory(diffs.get(dir).compared(folderOptions(opts)), dir); err == nil {
			return h
		}
	}
	return s.history(diffs.get(startDir).compared(opts), nil)
}
