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
