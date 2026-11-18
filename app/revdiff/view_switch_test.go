package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/ui"
)

func TestViewSwitch(t *testing.T) {
	v := &viewSwitch{}
	assert.Nil(t, v.forSource(sourceSetup{}), "stdin, a compare pair or a plain folder has no other view")
	show := v.forSource(sourceSetup{vcsSetup: vcsSetup{vcsType: diff.VCSGit}})
	require.NotNil(t, show)

	run, ok := show(true)().(splitview.RunMsg)
	require.True(t, ok, "the switch runs at the split, with the review on screen")
	assert.Nil(t, run.Run(nil), "nothing set yet: nothing runs")

	var got []bool
	v.set(func(_ tea.Model, files bool) tea.Cmd {
		got = append(got, files)
		return nil
	}, nil)
	run, _ = show(true)().(splitview.RunMsg)
	run.Run(nil)
	run, _ = show(false)().(splitview.RunMsg)
	run.Run(nil)
	assert.Equal(t, []bool{true, false}, got, "it asks what to run at the time of the pick")
}

func TestViewSwitch_Comparisons(t *testing.T) {
	v := &viewSwitch{}
	git := sourceSetup{vcsSetup: vcsSetup{vcsType: diff.VCSGit}}
	assert.Nil(t, v.comparisonsFor(sourceSetup{}))
	load := v.comparisonsFor(git)
	require.NotNil(t, load)
	assert.Equal(t, ui.ComparisonsMsg{}, load()(), "nothing set yet: nothing listed")

	var picked []string
	var pickedStore *annotation.Store
	v.set(nil, func() (comparisonList, error) {
		return comparisonList{
			items:   []splitview.PickerItem{{Label: "Uncommitted changes", Value: "u"}, {Label: "Unpushed changes", Value: "p"}},
			current: func(it splitview.PickerItem) bool { return it.Value == "p" },
			pick: func(it splitview.PickerItem, store *annotation.Store) tea.Cmd {
				picked = append(picked, it.Label)
				pickedStore = store
				return nil
			},
		}, nil
	})
	msg, ok := load()().(ui.ComparisonsMsg)
	require.True(t, ok)
	require.Len(t, msg.Items, 2)
	assert.Equal(t, "Uncommitted changes", msg.Items[0].Label)
	assert.False(t, msg.Items[0].Current)
	assert.True(t, msg.Items[1].Current, "the one on screen is marked")

	run, ok := msg.Items[0].Choose().(splitview.RunMsg)
	require.True(t, ok, "a pick runs at the split")
	run.Run(nil)
	assert.Equal(t, []string{"Uncommitted changes"}, picked)
	assert.NotNil(t, pickedStore, "with the annotations of the review on screen")
}
