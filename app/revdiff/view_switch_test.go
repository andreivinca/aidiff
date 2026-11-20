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
	})
	run, _ = show(true)().(splitview.RunMsg)
	run.Run(nil)
	run, _ = show(false)().(splitview.RunMsg)
	run.Run(nil)
	assert.Equal(t, []bool{true, false}, got, "it asks what to run at the time of the pick")
}
