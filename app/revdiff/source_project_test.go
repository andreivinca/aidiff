package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/t3palette"
)

func TestSourceSetup_ProjectName(t *testing.T) {
	t3 := func(p t3client.Project) sourceSetup {
		return sourceSetup{t3: &t3palette.Review{Target: t3client.Target{Project: p}}}
	}
	assert.Equal(t, "Sabra2", t3(t3client.Project{Title: "Sabra2", WorkspaceRoot: "/srv/other"}).projectName(), "a T3 project by its title")
	assert.Equal(t, "sabra2", t3(t3client.Project{WorkspaceRoot: "/srv/sabra2/"}).projectName(), "an untitled one by its folder")
	assert.Equal(t, "aidiff", sourceSetup{vcsSetup: vcsSetup{gitRoot: "/home/a/aidiff", workDir: "/home/a/aidiff/app"}}.projectName(),
		"a repository by its root, wherever the run started")
	assert.Equal(t, "notes", sourceSetup{vcsSetup: vcsSetup{workDir: "/home/a/notes"}}.projectName(), "a plain folder by its name")
	assert.Empty(t, sourceSetup{vcsSetup: vcsSetup{workDir: "/"}}.projectName(), "the root names nothing")
	assert.Empty(t, sourceSetup{}.projectName())
}
