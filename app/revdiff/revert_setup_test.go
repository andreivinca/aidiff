package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/revert"
	"github.com/umputun/revdiff/app/splitview"
)

func TestSourceSetup_Reverter(t *testing.T) {
	git := sourceSetup{vcsSetup: vcsSetup{vcsType: diff.VCSGit, gitRoot: "/repo"}}
	var o options
	assert.Equal(t, revert.Git{Root: "/repo"}, git.reverter(o), "unstaged: against the index")
	o.Refs.Base = "HEAD"
	assert.Equal(t, revert.Git{Root: "/repo", Ref: "HEAD"}, git.reverter(o))

	for name, opts := range map[string]options{
		"staged":    {Staged: true},
		"two refs":  func() options { var x options; x.Refs.Base, x.Refs.Against = "a", "b"; return x }(),
		"range":     func() options { var x options; x.Refs.Base = "a..b"; return x }(),
		"all files": {AllFiles: true},
		"stdin":     {Stdin: true},
	} {
		assert.Nil(t, git.reverter(opts), name)
	}
	assert.Nil(t, sourceSetup{vcsSetup: vcsSetup{vcsType: diff.VCSHg, gitRoot: "/repo"}}.reverter(options{}), "hg")
}

func TestConfirmPopup_CarriesBothAnswers(t *testing.T) {
	yes := func() tea.Msg { return "yes" }
	no := func() tea.Msg { return "no" }
	msg, ok := confirmPopup("Revert a.go?", "Puts a.go back.", "Revert", yes, no)().(splitview.ConfirmMsg)
	require.True(t, ok, "it asks in the split's popup")
	assert.Equal(t, "Revert a.go?", msg.Title)
	assert.Equal(t, "Puts a.go back.", msg.Detail)
	assert.Equal(t, "Revert", msg.Confirm)
	assert.Equal(t, "yes", msg.Yes())
	require.NotNil(t, msg.No, "a cancel reaches the review, which stops holding refreshes")
	assert.Equal(t, "no", msg.No())
}
