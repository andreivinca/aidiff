package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/revdiff/app/t3client"
)

func TestT3PaletteTerminalTarget(t *testing.T) {
	t.Run("no session runs locally", func(t *testing.T) {
		host, dir := (&t3Palette{}).terminalTarget()
		assert.Empty(t, host)
		assert.Empty(t, dir)
	})
	t.Run("ssh session opens in the reviewed worktree", func(t *testing.T) {
		p := &t3Palette{cur: &t3Review{
			session: &t3client.Session{Label: "ssh://box"},
			target:  t3client.Target{Cwd: "/srv/wt", Project: t3client.Project{WorkspaceRoot: "/srv/repo"}},
		}}
		host, dir := p.terminalTarget()
		assert.Equal(t, "box", host)
		assert.Equal(t, "/srv/wt", dir)
	})
	t.Run("connected without a thread uses the selected project", func(t *testing.T) {
		p := &t3Palette{cur: &t3Review{session: &t3client.Session{Label: "ssh://box"}},
			project: &t3client.Project{WorkspaceRoot: "/srv/repo"}}
		host, dir := p.terminalTarget()
		assert.Equal(t, "box", host)
		assert.Equal(t, "/srv/repo", dir)
	})
	t.Run("local session stays local", func(t *testing.T) {
		p := &t3Palette{cur: &t3Review{session: &t3client.Session{Label: "local"}, target: t3client.Target{Cwd: "/home/me/wt"}}}
		host, dir := p.terminalTarget()
		assert.Empty(t, host)
		assert.Equal(t, "/home/me/wt", dir)
	})
}

func TestRemoteShellArgv(t *testing.T) {
	assert.Equal(t, []string{"ssh", "-t", "--", "box", `exec "${SHELL:-/bin/sh}" -l`}, remoteShellArgv("box", ""))
	assert.Equal(t, []string{"ssh", "-t", "--", "box", `cd '/srv/it'\''s' 2>/dev/null; exec "${SHELL:-/bin/sh}" -l`},
		remoteShellArgv("box", "/srv/it's"))
}
