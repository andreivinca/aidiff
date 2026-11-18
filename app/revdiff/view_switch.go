package main

import (
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/ui"
)

// comparisonList is what the changes of the review on screen can be
// compared as: Diff View…'s comparison rows, which of them it shows, and how
// one is picked with the review's annotations.
type comparisonList struct {
	items   []splitview.PickerItem
	current func(it splitview.PickerItem) bool
	pick    func(it splitview.PickerItem, store *annotation.Store) tea.Cmd
}

// viewSwitch takes the toolbar's View menu from a review's changes to every
// file of its folder and back. The review is built before the Diff View…
// commands that rebuild it, so it holds this switch, and the switch learns
// what to run once they exist (set); it asks at the time of the pick, so it
// acts on whichever review is on screen then.
type viewSwitch struct {
	mu   sync.Mutex
	to   func(inner tea.Model, files bool) tea.Cmd
	list func() (comparisonList, error)
}

// set is what the switch runs: to rebuilds inner as its folder's files, or
// as its changes; list lists what the changes can be compared as.
func (v *viewSwitch) set(to func(inner tea.Model, files bool) tea.Cmd, list func() (comparisonList, error)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.to, v.list = to, list
}

// comparisonsFor is the View menu's loader of comparisons for a review of
// src, nil where forSource has no switch. It runs off the event loop, as
// listing asks git or the T3 server; each comparison it brings picks itself
// at the split, with the annotations of the review on screen then.
func (v *viewSwitch) comparisonsFor(src sourceSetup) func() tea.Cmd {
	if v.forSource(src) == nil {
		return nil
	}
	return func() tea.Cmd {
		return func() tea.Msg {
			v.mu.Lock()
			list := v.list
			v.mu.Unlock()
			if list == nil {
				return ui.ComparisonsMsg{}
			}
			l, err := list()
			if err != nil {
				return ui.ComparisonsMsg{Err: err}
			}
			items := make([]ui.Comparison, 0, len(l.items))
			for _, it := range l.items {
				items = append(items, ui.Comparison{Label: it.Label, Current: l.current(it), Choose: func() tea.Msg {
					return splitview.RunMsg{Run: func(inner tea.Model) tea.Cmd {
						return l.pick(it, reviewStore(inner))
					}}
				}})
			}
			return ui.ComparisonsMsg{Items: items}
		}
	}
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
