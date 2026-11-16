package main

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/t3palette"
)

// newT3Palette builds the T3 commands of the ctrl+p palette for this run.
// From here on the palette owns the T3 session src opened, if any; the
// caller ends it with Close.
func newT3Palette(deps reviewDeps, description string, src sourceSetup) *t3palette.Palette {
	opts := deps.opts
	return t3palette.New(t3palette.Config{
		Store: t3TokenStore(),
		NewReview: func(r *t3palette.Review, store *annotation.Store) (tea.Model, error) {
			model, err := deps.newModel(t3Source(opts, r), store, r.Description(description))
			if err != nil {
				return nil, err
			}
			return model, nil
		},
		SaveHistory: func(r *t3palette.Review, annotations string, files []string) {
			h := src.history(opts, r)
			h.annotations, h.files = annotations, files
			saveHistory(h)
		},
		NoChat:  opts.NoChat,
		NoWatch: opts.NoWatch,
		Chat:    chatConfig(opts),
	}, src.t3)
}

// history is how the annotations of a review are filed: by r's T3 server and
// folder, or as this run's own source when r is nil.
func (s sourceSetup) history(opts options, r *t3palette.Review) histReq {
	if r == nil {
		return histReq{opts: opts, gitRoot: s.gitRoot, workDir: s.workDir}
	}
	return histReq{opts: opts, t3Path: t3HistoryPath(r)}
}
