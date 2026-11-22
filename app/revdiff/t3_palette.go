package main

import (
	"log"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/localhist"
	"github.com/umputun/revdiff/app/t3palette"
)

// newT3Palette builds the T3 commands of the ctrl+p palette and the projects
// sidebar for this run; store holds the annotations of the review the run
// opens with, local how it reaches this machine's folders, plan the saved
// tabs and the saved T3 tab the run starts on. From here on the palette
// owns the T3 session src opened, if any; the caller ends it with Close.
func newT3Palette(deps reviewDeps, description string, src sourceSetup, store *annotation.Store, local t3palette.LocalConfig, plan runStart) *t3palette.Palette {
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
		NoChat:     opts.NoChat,
		NoTerminal: opts.NoTerminal,
		NoWatch:    opts.NoWatch,
		DiffLabel:  opts.T3Source,
		Chat:       chatConfig(opts),
		StartStore: store,
		LocalName:  deps.hostname,
		Local:      local,
		Saved:      savedConnections(),
		Tabs:       plan.tabs,
		StartTab:   plan.tab,
	}, src.t3)
}

// savedConnections keeps the T3 servers connected to in connections.json,
// beside the folders' history.json, for the connections panel.
func savedConnections() t3palette.SavedConfig {
	store := localhist.Connections{Path: filepath.Join(filepath.Dir(defaultConfigPath()), "connections.json")}
	return t3palette.SavedConfig{
		List: func() []t3palette.SavedServer {
			list, err := store.List()
			if err != nil {
				log.Printf("[WARN] %v", err)
			}
			out := make([]t3palette.SavedServer, 0, len(list))
			for _, c := range list {
				out = append(out, t3palette.SavedServer{Spec: c.Spec, Machine: c.Machine})
			}
			return out
		},
		Remember: func(spec, machine string) {
			if err := store.Add(spec, machine); err != nil {
				log.Printf("[WARN] %v", err)
			}
		},
		Forget: store.Remove,
	}
}

// history is how the annotations of a review are filed: by r's T3 server and
// folder, or as this run's own source when r is nil.
func (s sourceSetup) history(opts options, r *t3palette.Review) histReq {
	if r == nil {
		return histReq{opts: opts, gitRoot: s.gitRoot, workDir: s.workDir}
	}
	return histReq{opts: opts, t3Path: t3HistoryPath(r)}
}
