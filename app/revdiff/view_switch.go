package main

import (
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/splitview"
)

// viewSwitch takes the toolbar's View menu from a review's changes to every
// file of its folder and back. The review is built before the Diff View…
// commands that rebuild it, so it holds this switch, and the switch learns
// what to run once they exist (set); it asks at the time of the pick, so it
// acts on whichever review is on screen then.
type viewSwitch struct {
	mu sync.Mutex
	to func(inner tea.Model, files bool) tea.Cmd
}

// set is what the switch runs: to rebuilds inner as its folder's files, or
// as its changes.
func (v *viewSwitch) set(to func(inner tea.Model, files bool) tea.Cmd) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.to = to
}

// forSource is the toolbar's switch for a review of src, nil for a source
// that has no changes to go back to or no folder to browse: stdin, a
// compare pair, a plain folder outside git.
func (v *viewSwitch) forSource(src sourceSetup) func(files bool) tea.Cmd {
	if v == nil || (src.t3 == nil && src.vcsType != diff.VCSGit) {
		return nil
	}
	return func(files bool) tea.Cmd {
		return func() tea.Msg {
			return splitview.RunMsg{Run: func(inner tea.Model) tea.Cmd {
				v.mu.Lock()
				to := v.to
				v.mu.Unlock()
				if to == nil {
					return nil
				}
				return to(inner, files)
			}}
		}
	}
}
