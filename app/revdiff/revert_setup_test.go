package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/revert"
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
