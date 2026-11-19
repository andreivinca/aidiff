package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/t3palette"
	"github.com/umputun/revdiff/app/ui"
)

// localSession is a T3 session that only says where its server runs.
type localSession struct {
	t3palette.Session
	local bool
}

func (s localSession) Local() bool { return s.local }

func TestEditorPolicy(t *testing.T) {
	review := func(local bool) sourceSetup {
		return sourceSetup{t3: &t3palette.Review{Session: localSession{local: local}, Target: t3client.Target{Cwd: "/srv/wt"}}}
	}
	assert.Equal(t, ui.SourceEditorPolicy{Available: true, Root: "/srv/wt", DisallowAnnotatedFileEditing: true}, editorPolicy(options{}, review(true)),
		"a T3 server on this machine: the thread's folder")
	assert.Equal(t, ui.SourceEditorPolicy{}, editorPolicy(options{}, review(false)), "a remote one's files are on its host")

	local := sourceSetup{vcsSetup: vcsSetup{workDir: "/repo"}}
	assert.Equal(t, sourceEditorPolicy(options{}, "/repo"), editorPolicy(options{}, local), "a local review keeps upstream's")
}

func TestStartsWithTerminal(t *testing.T) {
	assert.True(t, startsWithTerminal(options{}, false), "a local folder opens with a terminal")
	assert.False(t, startsWithTerminal(options{}, true), "a thread opens with its chat instead")
	assert.False(t, startsWithTerminal(options{NoTerminal: true}, false), "--no-terminal")
	assert.True(t, startsWithTerminal(options{Terminal: true}, true), "--terminal always")
}
