package main

import (
	"log"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/localhist"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3palette"
)

// The tabs open at exit are kept in tabs.json, beside the folders'
// history.json, and come back, not opened yet, in the next run
// (t3palette/restore.go).

// tabsStore is where the open tabs are kept; its Path is "" when there is
// no home folder to keep them in.
func tabsStore() localhist.Tabs {
	config := defaultConfigPath()
	if config == "" {
		return localhist.Tabs{}
	}
	return localhist.Tabs{Path: filepath.Join(filepath.Dir(config), "tabs.json")}
}

// loadTabs reads the tabs kept in store and the index of the one that was
// on screen: a file it cannot read is said once and brings no tabs back,
// and the next exit replaces it.
func loadTabs(store localhist.Tabs) ([]t3palette.Tab, int) {
	if store.Path == "" {
		return nil, -1
	}
	list, current, err := store.Load()
	if err != nil {
		log.Printf("[WARN] %v; no tabs brought back", err)
	}
	tabs := make([]t3palette.Tab, 0, len(list))
	for _, tab := range list {
		tabs = append(tabs, paletteTab(tab))
	}
	return tabs, current
}

// openTabs is how the palette keeps the open tabs from one run to the next:
// load reads them (once, shared with where the run starts, planStart), and
// they are saved to store.
func openTabs(store localhist.Tabs, load func() ([]t3palette.Tab, int)) t3palette.TabsConfig {
	if store.Path == "" {
		return t3palette.TabsConfig{}
	}
	return t3palette.TabsConfig{
		Load: load,
		Save: func(tabs []t3palette.Tab, current int) error {
			list := make([]localhist.Tab, 0, len(tabs))
			for _, tab := range tabs {
				list = append(list, fileTab(tab))
			}
			return store.Save(list, current)
		},
	}
}

// paletteTab is a tab of the file as the palette reopens it.
func paletteTab(tab localhist.Tab) t3palette.Tab {
	if tab.Kind == localhist.TabLocal {
		return t3palette.Tab{Dir: tab.Dir}
	}
	return t3palette.Tab{Server: tab.Server, Machine: tab.Machine, ProjectID: tab.Project, ProjectTitle: tab.Title, ThreadID: tab.Thread}
}

// fileTab is a tab of the palette as the file keeps it.
func fileTab(tab t3palette.Tab) localhist.Tab {
	if tab.Dir != "" {
		return localhist.Tab{Kind: localhist.TabLocal, Dir: tab.Dir}
	}
	return localhist.Tab{Kind: localhist.TabT3, Server: tab.Server, Machine: tab.Machine, Project: tab.ProjectID,
		Title: tab.ProjectTitle, Thread: tab.ThreadID}
}

// saveTabs keeps the tabs of final, the split the program ended with, for
// the next run, in their order, with the one on screen. It runs as the
// review's history is filed, and before the panes close: a small local
// write whose lock it waits for briefly, so it never holds up the exit.
func saveTabs(t3p *t3palette.Palette, final tea.Model) {
	split, ok := final.(splitview.Model)
	if !ok {
		return
	}
	if err := t3p.SaveTabs(split.Tabs(), split.Workspace()); err != nil {
		log.Printf("[WARN] tabs not saved: %v", err)
	}
}
