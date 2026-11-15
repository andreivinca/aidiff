package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/ui"
)

type vcsSetup struct {
	renderer           ui.Renderer
	vcsType            diff.VCSType
	gitRoot            string // set only when VCS is git; used by history module to run git commands
	workDir            string
	folder             *diff.FolderReader
	allFiles           bool // plain folders implicitly browse all file contents
	blamer             ui.Blamer
	untrackedFn        func() ([]string, error)
	untrackedRenamesFn func([]string) ([]diff.FileEntry, error) // git-only; pairs untracked renames with their origin
	commitLogger       diff.CommitLogger                        // VCS-backed commit log source; nil when VCS lacks the capability
}

// sourceSetup is where the reviewed diff comes from: a local VCS, a compare
// pair, stdin, or a T3 Code server. Fields beyond vcsSetup are set only by
// the source that needs them.
type sourceSetup struct {
	vcsSetup
	tty *os.File  // stdin mode: the terminal to read keys from
	t3  *t3Review // T3 mode: the live server session
}

// setupSource picks the diff source for this run. T3 mode connects here,
// before the alt screen opens, so SSH and auth failures print plainly.
func setupSource(opts options) (sourceSetup, error) {
	switch {
	case opts.t3Mode():
		t3, err := setupT3Review(opts)
		if err != nil {
			return sourceSetup{}, err
		}
		return sourceSetup{vcsSetup: vcsSetup{renderer: t3.renderer}, t3: t3}, nil
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
	setup, err := setupVCSRenderer(opts)
	if err != nil {
		return sourceSetup{}, err
	}
	setup.untrackedFn = filterUntracked(setup.untrackedFn, opts.Include, opts.Exclude)
	return sourceSetup{vcsSetup: setup}, nil
}

// watch starts live refresh for the source and returns its stop function:
// file watching for git and plain folders, run tracking for a T3 thread.
func (s sourceSetup) watch(opts options, p *tea.Program) (stop func()) {
	switch {
	case opts.NoWatch:
		return func() {}
	case s.t3 != nil:
		return s.t3.watch(p)
	case s.vcsType == diff.VCSGit:
		return watchRepo(s.workDir, p)
	case s.allFiles && s.vcsType == diff.VCSNone:
		return watchLazyFolder(s.folder, s.workDir, p)
	}
	return func() {}
}

// close releases what the source holds open. A T3 session is not closed
// here: the palette adopts it and closes whichever session is live at exit.
func (s sourceSetup) close() {
	if s.tty != nil {
		_ = s.tty.Close()
	}
}

// setupVCSRenderer detects the VCS and creates the appropriate renderer, blamer, and untracked function.
func setupVCSRenderer(opts options) (vcsSetup, error) {
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		cwd = "."
	}
	vcsType, vcsRoot := diff.DetectVCS(cwd)

	switch vcsType {
	case diff.VCSGit:
		g := diff.NewGit(vcsRoot)
		r, workDir, err := makeGitRenderer(g, opts, vcsRoot)
		if err != nil {
			return vcsSetup{}, err
		}
		return vcsSetup{renderer: r, vcsType: diff.VCSGit, gitRoot: vcsRoot, workDir: workDir, blamer: g, untrackedFn: g.UntrackedFiles, untrackedRenamesFn: g.UntrackedRenames, commitLogger: g}, nil
	case diff.VCSHg:
		if opts.Staged {
			fmt.Fprintln(os.Stderr, "warning: --staged ignored in mercurial repository (no staging area)")
		}
		h := diff.NewHg(vcsRoot)
		r, workDir, err := makeHgRenderer(h, opts, vcsRoot)
		if err != nil {
			return vcsSetup{}, err
		}
		return vcsSetup{renderer: r, vcsType: diff.VCSHg, workDir: workDir, blamer: h, untrackedFn: h.UntrackedFiles, commitLogger: h}, nil
	case diff.VCSJJ:
		if opts.Staged {
			fmt.Fprintln(os.Stderr, "warning: --staged ignored in jujutsu repository (no staging area)")
		}
		jj := diff.NewJj(vcsRoot)
		r, workDir, err := makeJjRenderer(jj, opts, vcsRoot)
		if err != nil {
			return vcsSetup{}, err
		}
		return vcsSetup{renderer: r, vcsType: diff.VCSJJ, workDir: workDir, blamer: jj, untrackedFn: jj.UntrackedFiles, commitLogger: jj}, nil
	default:
		r, workDir, err := makeNoVCSRenderer(opts, cwd)
		if err != nil {
			return vcsSetup{}, err
		}
		folder, _ := r.(*diff.FolderReader)
		return vcsSetup{renderer: r, workDir: workDir, folder: folder, allFiles: len(opts.Only) == 0}, nil
	}
}

// makeGitRenderer selects the appropriate git renderer based on flags.
// reuses the provided *Git instance as the default renderer to avoid double allocation.
func makeGitRenderer(g *diff.Git, opts options, repoRoot string) (ui.Renderer, string, error) { //nolint:unparam // error kept for consistency with makeHgRenderer/makeNoVCSRenderer
	var r ui.Renderer
	switch {
	case opts.AllFiles:
		r = diff.NewDirectoryReader(repoRoot)
	case len(opts.Only) > 0:
		r = diff.NewFallbackRenderer(g, opts.Only, repoRoot)
	default:
		r = g
	}
	return wrapFilters(r, opts), repoRoot, nil
}

// makeHgRenderer selects the appropriate mercurial renderer based on flags.
// reuses the provided *Hg instance as the default renderer to avoid double allocation.
func makeHgRenderer(h *diff.Hg, opts options, repoRoot string) (ui.Renderer, string, error) {
	var r ui.Renderer
	switch {
	case opts.AllFiles:
		return nil, "", errors.New("--all-files is not supported in mercurial repositories")
	case len(opts.Only) > 0:
		r = diff.NewFallbackRenderer(h, opts.Only, repoRoot)
	default:
		r = h
	}
	return wrapFilters(r, opts), repoRoot, nil
}

// makeJjRenderer selects the appropriate jujutsu renderer based on flags.
// reuses the provided *Jj instance as the default renderer to avoid double allocation.
func makeJjRenderer(j *diff.Jj, opts options, repoRoot string) (ui.Renderer, string, error) { //nolint:unparam // error kept for consistency with makeGitRenderer/makeHgRenderer
	var r ui.Renderer
	switch {
	case opts.AllFiles:
		r = diff.NewJjDirectoryReader(repoRoot)
	case len(opts.Only) > 0:
		r = diff.NewFallbackRenderer(j, opts.Only, repoRoot)
	default:
		r = j
	}
	return wrapFilters(r, opts), repoRoot, nil
}

// wrapFilters applies include/exclude filters to a renderer based on opts.
func wrapFilters(r ui.Renderer, opts options) ui.Renderer {
	if len(opts.Include) > 0 {
		r = diff.NewIncludeFilter(r, opts.Include)
	}
	if len(opts.Exclude) > 0 {
		r = diff.NewExcludeFilter(r, opts.Exclude)
	}
	return r
}

// filterUntracked wraps an untracked-files function so its results honor the
// --include / --exclude prefixes. The raw VCS UntrackedFiles call bypasses the
// renderer's IncludeFilter/ExcludeFilter (those only filter ChangedFiles), so
// without this wrap untracked files leak into a scoped review. Returns fn
// unchanged when fn is nil or no prefixes are set.
func filterUntracked(fn func() ([]string, error), include, exclude []string) func() ([]string, error) {
	if fn == nil || (len(include) == 0 && len(exclude) == 0) {
		return fn
	}
	return func() ([]string, error) {
		paths, err := fn()
		if err != nil {
			return nil, err
		}
		return diff.FilterPaths(paths, include, exclude), nil
	}
}

// makeNoVCSRenderer creates a renderer when no VCS is detected.
// Plain folders browse all files by default; --only selects standalone files.
func makeNoVCSRenderer(opts options, cwd string) (ui.Renderer, string, error) {
	if opts.ref() != "" || opts.Staged {
		return nil, "", errors.New("refs and --staged require a repository")
	}
	if len(opts.Only) > 0 {
		return diff.NewFileReader(opts.Only, cwd), cwd, nil
	}
	return diff.NewFolderReader(cwd, opts.Include, opts.Exclude), cwd, nil
}
