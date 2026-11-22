package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/splitview"
)

func TestViewSwitch(t *testing.T) {
	v := &viewSwitch{}
	git := sourceSetup{vcsSetup: vcsSetup{vcsType: diff.VCSGit}}
	assert.Nil(t, v.forSource(options{}, sourceSetup{}), "stdin, a compare pair or a plain folder has no other view")
	assert.Nil(t, v.forSource(options{AllFiles: true}, git), "--all-files has no Diff View… to switch through")
	assert.Nil(t, newLocalDiff(reviewDeps{opts: options{AllFiles: true}}, git, ""), "the switch is offered exactly when Diff View… exists")
	tree := git
	tree.folder = diff.NewFolderReader(t.TempDir(), nil, nil)
	assert.NotNil(t, v.forSource(options{}, tree), "Diff View…'s file tree switches back to the changes")
	show := v.forSource(options{}, git)
	require.NotNil(t, show)

	run, ok := show(true)().(splitview.RunMsg)
	require.True(t, ok, "the switch runs at the split, with the review on screen")
	assert.Nil(t, run.Run(nil), "nothing set yet: nothing runs")

	var got []bool
	v.set(func(_ tea.Model, files bool) tea.Cmd {
		got = append(got, files)
		return nil
	})
	run, _ = show(true)().(splitview.RunMsg)
	run.Run(nil)
	run, _ = show(false)().(splitview.RunMsg)
	run.Run(nil)
	assert.Equal(t, []bool{true, false}, got, "it asks what to run at the time of the pick")
}

func TestPickerMenu(t *testing.T) {
	var picked []string
	picker := splitview.ShowPickerMsg{Items: []splitview.PickerItem{
		{Label: "Uncommitted changes", Detail: "staged and unstaged", Value: "u"},
		{Label: "Staged changes", Detail: "what the next commit takes", Value: "s", Current: true},
	}, OnPick: func(it splitview.PickerItem) tea.Cmd {
		picked = append(picked, it.Value.(string))
		return nil
	}}
	asking := tea.Batch(func() tea.Msg { return splitview.FlashMsg{Text: "T3: listing branches…"} },
		func() tea.Msg { return picker })
	got := pickerMenu(asking)
	require.Len(t, got.Items, 2, "the picker's rows, from inside the batch T3's ask makes")
	assert.Empty(t, got.Err)
	assert.Equal(t, "Staged changes", got.Items[1].Label)
	assert.Equal(t, "what the next commit takes", got.Items[1].Detail)
	assert.True(t, got.Items[1].Current)
	got.Items[0].Run()
	got.Items[1].Run()
	assert.Equal(t, []string{"u", "s"}, picked, "each row picks its own item")

	failed := pickerMenu(tea.Batch(func() tea.Msg { return splitview.FlashMsg{Text: "T3: listing branches…"} },
		func() tea.Msg {
			return splitview.FlashMsg{Text: "T3: box is not a git repository: there is no diff to choose"}
		}))
	assert.Equal(t, "T3: box is not a git repository: there is no diff to choose", failed.Err, "the reason, not the progress note")
	assert.Equal(t, "nothing to compare", pickerMenu(nil).Err)
}

func TestSplitKeys_FocusKeysBothWays(t *testing.T) {
	k := splitKeys()
	assert.Equal(t, "alt+h", k.FocusLeftAlt, "the Linux console's keymap takes alt+left to switch consoles")
	assert.Equal(t, "alt+l", k.FocusAlt, "and they work everywhere else too")
}
