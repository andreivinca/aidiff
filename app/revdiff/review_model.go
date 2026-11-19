package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/handoff"
	"github.com/umputun/revdiff/app/highlight"
	"github.com/umputun/revdiff/app/keymap"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/theme"
	"github.com/umputun/revdiff/app/ui"
	"github.com/umputun/revdiff/app/ui/overlay"
	"github.com/umputun/revdiff/app/ui/sidepane"
	"github.com/umputun/revdiff/app/ui/style"
	"github.com/umputun/revdiff/app/ui/worddiff"
)

// reviewDeps are the dependencies every review model of a run shares, kept
// apart from the diff source so a review can be rebuilt for another source
// while the program runs: the T3 palette commands switch threads that way.
type reviewDeps struct {
	opts          options
	hl            *highlight.Highlighter
	km            *keymap.Keymap
	res           style.Resolver
	themes        *themeCatalog
	postFlushHook ui.PostFlushHook
	hostname      string      // this machine, shown in the status bar for local reviews
	views         *viewSwitch // the toolbar's switch between changes and every file (view_switch.go)
}

// newReviewDeps builds the shared dependencies from the options.
func newReviewDeps(opts options) reviewDeps {
	// construct the three style types per D15: Resolver first, Renderer from
	// Resolver (in newModel), SGR is zero-value
	var res style.Resolver
	if opts.NoColors {
		res = style.PlainResolver()
	} else {
		res = style.NewResolver(optsToStyleColors(opts))
	}
	d := reviewDeps{
		views: &viewSwitch{},
		opts:  opts,
		hl:    highlight.New(opts.ChromaStyle, !opts.NoColors),
		km:    keymap.LoadOrDefault(resolveKeysPath(opts)),
		res:   res,
		themes: &themeCatalog{
			catalog:    theme.NewCatalog(defaultThemesDir()),
			configPath: resolveFlagPath(os.Args[1:], "config", "REVDIFF_CONFIG", defaultConfigPath),
		},
	}
	if host, err := os.Hostname(); err == nil {
		d.hostname = host
	}
	// the optional post-flush handoff is configured apart from theme settings
	if hook := handoff.New(opts.PostFlushCommand); hook != nil {
		d.postFlushHook = hook
	}
	return d
}

// newModel builds the review of src, with its annotation store and the
// info-popup description.
func (d reviewDeps) newModel(src sourceSetup, store *annotation.Store, description string) (ui.Model, error) {
	opts := d.opts
	opts.AllFiles = opts.AllFiles || src.folder != nil // a plain folder browses every file
	model, err := ui.NewModel(ui.ModelConfig{
		T3Mode:               src.t3 != nil,
		MachineName:          src.machineName(d.hostname),
		Project:              src.projectName(),
		Branch:               src.branchName(),
		LoadBranch:           src.branchLoader(),
		KeepFileTree:         true, // aidiff has no single-file view
		Renderer:             src.renderer,
		Store:                store,
		Highlighter:          d.hl,
		StyleResolver:        d.res,
		StyleRenderer:        style.NewRenderer(d.res),
		SGR:                  style.SGR{},
		WordDiffer:           worddiff.New(),
		Overlay:              overlay.NewManager(),
		Themes:               d.themes,
		Blamer:               src.blamer,
		LoadUntracked:        src.untrackedFn,
		LoadUntrackedRenames: src.untrackedRenamesFn,
		Keymap:               d.km,
		PostFlushHook:        d.postFlushHook,
		CommitLog:            src.commitLogger,
		CommitsApplicable:    commitsApplicable(opts, src.commitLogger),
		ReloadApplicable:     reloadApplicable(opts),
		CompactApplicable:    compactApplicable(opts, src.renderer),
		NoColors:             opts.NoColors,
		MouseTracking:        !opts.NoMouse,
		NoStatusBar:          opts.NoStatusBar,
		Toolbar:              !opts.NoToolbar,
		Compare:              openDiffView,
		ShowFiles:            d.views.forSource(src),
		Comparisons:          d.views.comparisonsFor(src),
		NoConfirmDiscard:     opts.NoConfirmDiscard,
		NoConfirmReload:      opts.NoConfirmReload,
		NoTree:               opts.NoTree,
		Wrap:                 opts.Wrap,
		WrapIndent:           opts.WrapIndent,
		PageOverlap:          opts.PageOverlap,
		Collapsed:            opts.Collapsed,
		Compact:              compactAtStart(opts, src),
		CompactContext:       compactContextAtStart(opts, src),
		CrossFileHunks:       opts.CrossFileHunks,
		CrossFileMotion:      opts.CrossFileMotion,
		StopAtAnnotations:    opts.StopAtAnnotations,
		StartAtChange:        opts.StartAtChange,
		LineNumbers:          opts.LineNumbers,
		ShowBlame:            opts.Blame,
		ShowUntracked:        opts.startupUntracked(),
		WordDiff:             opts.WordDiff,
		FilterUnreviewed:     opts.FilterUnreviewed,
		VimMotion:            opts.VimMotion,
		ReviewInfo: reviewInfoFromOptions(opts, reviewInfoInputs{
			workDir:     src.workDir,
			vcsType:     src.vcsType,
			description: description,
		}),
		TabWidth:         opts.TabWidth,
		Ref:              opts.ref(),
		Staged:           opts.Staged,
		TreeWidthRatio:   opts.TreeWidth,
		Only:             opts.Only,
		WorkDir:          src.workDir,
		SourceEditor:     editorPolicy(opts, src),
		ActiveThemeName:  d.themes.catalog.ActiveName(opts.Theme),
		AnnotationMarker: opts.AnnotationMarker,
		OutputPath:       opts.Output,
		NewFileTree:      src.newFileTree,
		ParseTOC: func(lines []diff.DiffLine, filename string) ui.TOCComponent {
			toc := sidepane.ParseTOC(lines, filename)
			if toc == nil {
				return nil // collapse typed-nil *TOC into truly nil interface
			}
			return toc
		},
	})
	if err != nil {
		return ui.Model{}, fmt.Errorf("create model: %w", err)
	}
	return model, nil
}

// compactAtStart reports whether a review starts in the changed blocks: a T3
// review does unless T3: Diff View… was showing whole files, as its patches
// did before they could be expanded; any other follows --compact.
func compactAtStart(opts options, src sourceSetup) bool {
	if src.t3 != nil {
		return !src.t3.View.WholeFiles
	}
	return opts.Compact
}

// compactContextAtStart is the lines around each change a review starts
// with: what T3: Diff View… last saw for a T3 review, else --compact-context.
func compactContextAtStart(opts options, src sourceSetup) int {
	if src.t3 != nil && src.t3.View.Context > 0 {
		return src.t3.View.Context
	}
	return opts.CompactContext
}

// openDiffView is the toolbar's Compare button: the palette's Diff View…,
// the local one or T3's, whichever the review has.
func openDiffView() tea.Cmd {
	return func() tea.Msg {
		return splitview.RunCommandMsg{Suffix: "Diff View…", Missing: "Diff View… is not available for this review"}
	}
}

// editorPolicy is where e / f12 open the editor: the review's folder, or a
// T3 review's folder when its server runs on this machine. A remote T3
// review's files are on its host, so it has none.
func editorPolicy(opts options, src sourceSetup) ui.SourceEditorPolicy {
	if src.t3 != nil {
		if !src.t3.Session.Local() || src.t3.Target.Cwd == "" {
			return ui.SourceEditorPolicy{}
		}
		// the thread's changes are in its working tree: like a worktree review,
		// a file with line annotations is not opened for editing
		return ui.SourceEditorPolicy{Available: true, Root: src.t3.Target.Cwd, DisallowAnnotatedFileEditing: true}
	}
	return sourceEditorPolicy(opts, src.workDir)
}
