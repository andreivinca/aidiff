package main

import (
	"strings"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/termpane"
)

// startTerminal opens a terminal pane on the machine the review comes from:
// a shell over ssh when the live T3 session tunnels through an SSH host,
// otherwise a local one. Either starts in the reviewed folder when known.
func (t *t3Palette) startTerminal(width, height int) (splitview.Terminal, error) {
	host, dir := t.terminalTarget()
	if host != "" {
		return termpane.Start(remoteShellArgv(host, dir), "", width, height)
	}
	return termpane.Start(strings.Fields(t.opts.TerminalCommand), dir, width, height)
}

// terminalTarget is the live session's SSH host (empty when local) and the
// folder a new terminal starts in: the reviewed worktree, else the selected
// project's root, else empty.
func (t *t3Palette) terminalTarget() (host, dir string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur == nil {
		return "", ""
	}
	if t.cur.session != nil {
		host = t.cur.session.SSHHost()
	}
	switch {
	case t.cur.target.Cwd != "":
		dir = t.cur.target.Cwd
	case t.project != nil:
		dir = t.project.WorkspaceRoot
	}
	return host, dir
}

// remoteShellArgv runs an interactive login shell on host, in dir when it
// exists there. No BatchMode: the pane is a real terminal, so ssh may prompt.
func remoteShellArgv(host, dir string) []string {
	script := `exec "${SHELL:-/bin/sh}" -l`
	if dir != "" {
		script = "cd " + shellQuote(dir) + " 2>/dev/null; " + script
	}
	return []string{"ssh", "-t", "--", host, script}
}

// shellQuote quotes s as one word for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
