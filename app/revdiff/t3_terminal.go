package main

import (
	"fmt"
	"strings"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/termpane"
)

// terminalLauncher starts terminal panes on the machine the review comes
// from: a shell over ssh when the T3 session tunnels through an SSH host,
// otherwise a local one, either in the reviewed folder when it is known.
type terminalLauncher struct {
	command string                       // --terminal-command, a shell command line; empty runs a login shell
	place   func() (sshHost, dir string) // where a new terminal opens; see t3palette.Palette.TerminalPlace
}

// start opens a terminal pane of the given size.
func (l terminalLauncher) start(width, height int) (splitview.Terminal, error) {
	argv, dir := l.argv()
	pane, err := termpane.Start(argv, dir, width, height)
	if err != nil {
		return nil, fmt.Errorf("start terminal: %w", err)
	}
	return pane, nil
}

// argv is what a new terminal runs, and the local folder it runs in. The
// command line goes to a shell, here or on the SSH host, so it is quoted the
// way a shell reads it; an empty argv runs $SHELL.
func (l terminalLauncher) argv() (argv []string, localDir string) {
	host, dir := l.place()
	if host != "" {
		return l.remoteArgv(host, dir), ""
	}
	if l.command == "" {
		return nil, dir
	}
	return []string{"/bin/sh", "-c", l.command}, dir
}

// remoteArgv runs the command, by default an interactive login shell, on
// host, in dir when it exists there. No BatchMode: the pane is a real
// terminal, so ssh may prompt.
func (l terminalLauncher) remoteArgv(host, dir string) []string {
	script := `exec "${SHELL:-/bin/sh}" -l`
	if l.command != "" {
		script = l.command
	}
	if dir != "" {
		script = "cd " + shellQuote(dir) + " 2>/dev/null; " + script
	}
	return []string{"ssh", "-t", "--", host, script}
}

// shellQuote quotes s as one word for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
