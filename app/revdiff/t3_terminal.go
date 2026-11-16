package main

import (
	"fmt"
	"strings"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3palette"
	"github.com/umputun/revdiff/app/termpane"
)

// terminalLauncher starts terminal panes on the machine the review comes
// from: a shell over ssh when the T3 session tunnels through an SSH host,
// otherwise a local one, either in the reviewed folder when it is known.
type terminalLauncher struct {
	command string                 // --terminal-command, a shell command line; empty runs a login shell
	place   func() t3palette.Place // where a new terminal opens; see t3palette.Palette.TerminalPlace
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
	p := l.place()
	if p.SSHHost != "" {
		return l.remoteArgv(p), ""
	}
	if l.command == "" {
		return nil, p.Dir
	}
	return []string{"/bin/sh", "-c", l.command}, p.Dir
}

// remoteArgv runs the command, by default an interactive login shell, on
// p's host, in its folder when that exists there. No BatchMode: the pane is
// a real terminal, so ssh may prompt.
//
// The T3 tunnel's ssh already holds the host's forwards from ~/.ssh/config,
// so the terminal asks for none (ClearAllForwardings): a second bind of the
// same local ports would fail, and fail the login under ExitOnForwardFailure.
// With the tunnel's control socket the terminal is a session on the tunnel's
// own connection, with no second login; when the socket is gone ssh falls
// back to a connection of its own.
func (l terminalLauncher) remoteArgv(p t3palette.Place) []string {
	script := `exec "${SHELL:-/bin/sh}" -l`
	if l.command != "" {
		script = l.command
	}
	if p.Dir != "" {
		script = "cd " + shellQuote(p.Dir) + " 2>/dev/null; " + script
	}
	argv := []string{"ssh", "-t", "-o", "ClearAllForwardings=yes"}
	if p.SSHControlPath != "" {
		argv = append(argv, "-o", "ControlMaster=no", "-S", p.SSHControlPath)
	}
	return append(argv, "--", p.SSHHost, script)
}

// shellQuote quotes s as one word for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
