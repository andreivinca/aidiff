package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/t3palette"
	"github.com/umputun/revdiff/app/ui"
	"github.com/umputun/revdiff/app/ui/sidepane"
)

// sourceSetup is where the reviewed diff comes from: a local VCS, a plain
// folder, a compare pair, stdin, or a T3 Code server. Fields beyond vcsSetup
// are set only by the source that needs them.
type sourceSetup struct {
	vcsSetup
	folder *diff.FolderReader // plain-folder mode: the folder browsed lazily
	// remote is a T3 host's folder browsed lazily, every file of it rather
	// than its changes (t3client.SourceFiles)
	remote *t3client.FolderReader
	tty    *os.File          // stdin mode: the terminal to read keys from
	t3     *t3palette.Review // T3 mode: the thread under review and its session
}

// setupSource picks the diff source for this run. T3 mode connects here,
// before the alt screen opens, so SSH and auth failures print plainly.
func setupSource(opts options) (sourceSetup, error) {
	switch {
	case opts.t3Mode():
		r, err := setupT3Review(opts)
		if err != nil {
			return sourceSetup{}, err
		}
		return t3Source(opts, r), nil
	case opts.compareAbsOld != "":
		return sourceSetup{vcsSetup: vcsSetup{
			renderer: diff.NewCompareReader(opts.compareAbsOld, opts.compareAbsNew),
			workDir:  filepath.Dir(opts.compareAbsNew),
		}}, nil
	case opts.Stdin:
		r, tty, err := prepareStdinMode(opts, os.Stdin)
		if err != nil {
			return sourceSetup{}, err
		}
		return sourceSetup{vcsSetup: vcsSetup{renderer: r}, tty: tty}, nil
	}
	if folder, ok, err := setupFolder(opts); ok || err != nil {
		return folder, err
	}
	setup, err := setupVCSRenderer(opts)
	if err != nil {
		return sourceSetup{}, err
	}
	setup.untrackedFn = filterUntracked(setup.untrackedFn, opts.Include, opts.Exclude)
	return sourceSetup{vcsSetup: setup}, nil
}

// setupFolder browses the current folder when it is in no repository and
// --only names no files (with --only, setupVCSRenderer reviews just those).
// ok reports whether this is the source; refs and --staged need a repository.
func setupFolder(opts options) (src sourceSetup, ok bool, err error) {
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		cwd = "."
	}
	return setupFolderIn(opts, cwd)
}

// setupFolderIn is setupFolder for the folder cwd.
func setupFolderIn(opts options, cwd string) (src sourceSetup, ok bool, err error) {
	if len(opts.Only) > 0 {
		return sourceSetup{}, false, nil
	}
	if vcsType, _ := diff.DetectVCS(cwd); vcsType != diff.VCSNone {
		return sourceSetup{}, false, nil
	}
	if opts.ref() != "" || opts.Staged {
		return sourceSetup{}, true, errors.New("refs and --staged require a repository")
	}
	folder := diff.NewFolderReader(cwd, opts.Include, opts.Exclude)
	return sourceSetup{vcsSetup: vcsSetup{renderer: folder, workDir: cwd}, folder: folder}, true, nil
}

// watch starts live refresh for the source and returns its stop function:
// file watching for git and plain folders. A T3 thread's refresh is the T3
// palette's, since it follows the thread on screen.
func (s sourceSetup) watch(opts options, p msgSender) (stop func()) {
	switch {
	case opts.NoWatch:
		return func() {}
	case s.vcsType == diff.VCSGit:
		return watchRepo(s.workDir, p)
	case s.folder != nil:
		return watchLazyFolder(s.folder, s.workDir, p)
	}
	return func() {}
}

// close releases what the source holds open. A T3 session is not closed
// here: the T3 palette, made right after the source, owns it.
func (s sourceSetup) close() {
	if s.tty != nil {
		_ = s.tty.Close()
	}
}

// machineName is the host the reviewed files live on: the T3 session's
// machine, else this one (localHost).
func (s sourceSetup) machineName(localHost string) string {
	if s.t3 != nil {
		return s.t3.Session.MachineName()
	}
	return localHost
}

// projectName is the project the reviewed files belong to: the T3
// project's title, else the folder of the repository or the one browsed.
func (s sourceSetup) projectName() string {
	if s.t3 != nil {
		p := s.t3.Target.Project
		if p.Title != "" {
			return p.Title
		}
		return baseName(p.WorkspaceRoot)
	}
	if s.gitRoot != "" {
		return baseName(s.gitRoot)
	}
	return baseName(s.workDir)
}

// baseName is the last element of a path, "" for no path or the root.
func baseName(dir string) string {
	if dir == "" {
		return ""
	}
	base := path.Base(filepath.ToSlash(dir))
	if base == "/" || base == "." {
		return ""
	}
	return base
}

// reviewDescription is the info popup's description: the user's, under the
// T3 thread's own.
func (s sourceSetup) reviewDescription(user string) string {
	if s.t3 != nil {
		return s.t3.Description(user)
	}
	return user
}

// newFileTree builds the tree pane: a folder browser for a plain folder, the
// changed-file tree otherwise.
func (s sourceSetup) newFileTree(entries []diff.FileEntry) ui.FileTreeComponent {
	if s.folder != nil {
		return sidepane.NewFolderTree(s.folder)
	}
	if s.remote != nil {
		return sidepane.NewFolderTree(s.remote)
	}
	return sidepane.NewFileTree(entries)
}

// branchName resolves the current branch in the reviewed repository, ""
// when there is none or it cannot be read.
func (s sourceSetup) branchName() string {
	name, _ := s.branch()
	return name
}

// branchLoader re-reads the branch for the status bar, nil when the source
// has no branch to follow (stdin, a compare pair, a folder outside git).
func (s sourceSetup) branchLoader() func() (string, error) {
	if s.t3 == nil && s.vcsType != diff.VCSGit {
		return nil
	}
	return s.branch
}

// branch is the current branch of the reviewed repository: "" on a detached
// HEAD, an error when it could not be read.
func (s sourceSetup) branch() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if s.t3 != nil {
		refs, err := s.t3.Session.Conn().ListRefs(ctx, s.t3.Target.Cwd)
		if err != nil {
			return "", fmt.Errorf("list refs: %w", err)
		}
		for _, ref := range refs {
			if ref.Current {
				return ref.Name, nil
			}
		}
		return "", nil
	}
	if s.vcsType != diff.VCSGit {
		return "", nil
	}
	g := localGit{dir: s.workDir}
	name, err := g.run(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		return name, nil
	}
	if _, headErr := g.run(ctx, "rev-parse", "--verify", "--quiet", "HEAD"); headErr == nil {
		return "", nil // detached: no branch
	}
	return "", err
}
