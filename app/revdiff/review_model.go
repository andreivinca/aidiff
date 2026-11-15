package main

import (
	"fmt"
	"os"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/handoff"
	"github.com/umputun/revdiff/app/highlight"
	"github.com/umputun/revdiff/app/keymap"
	"github.com/umputun/revdiff/app/t3client"
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
		opts: opts,
		hl:   highlight.New(opts.ChromaStyle, !opts.NoColors),
		km:   keymap.LoadOrDefault(resolveKeysPath(opts)),
		res:  res,
		themes: &themeCatalog{
			catalog:    theme.NewCatalog(defaultThemesDir()),
			configPath: resolveFlagPath(os.Args[1:], "config", "REVDIFF_CONFIG", defaultConfigPath),
		},
	}
	// the optional post-flush handoff is configured apart from theme settings
	if hook := handoff.New(opts.PostFlushCommand); hook != nil {
		d.postFlushHook = hook
	}
	return d
}

// newModel builds the review of src, with its annotation store and the
// info-popup description.
func (d reviewDeps) newModel(src vcsSetup, store *annotation.Store, description string) (ui.Model, error) {
	opts := d.opts
	opts.AllFiles = opts.AllFiles || src.allFiles
	_, t3Mode := src.renderer.(*t3client.Renderer)
	model, err := ui.NewModel(ui.ModelConfig{
		T3Mode:               t3Mode,
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
		NoConfirmDiscard:     opts.NoConfirmDiscard,
		NoConfirmReload:      opts.NoConfirmReload,
		NoTree:               opts.NoTree,
		Wrap:                 opts.Wrap,
		WrapIndent:           opts.WrapIndent,
		PageOverlap:          opts.PageOverlap,
		Collapsed:            opts.Collapsed,
		Compact:              opts.Compact,
		CompactContext:       opts.CompactContext,
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
		SourceEditor:     sourceEditorPolicy(opts, src.workDir),
		ActiveThemeName:  d.themes.catalog.ActiveName(opts.Theme),
		AnnotationMarker: opts.AnnotationMarker,
		OutputPath:       opts.Output,
		NewFileTree: func(entries []diff.FileEntry) ui.FileTreeComponent {
			return sidepane.NewFileTree(entries)
		},
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
