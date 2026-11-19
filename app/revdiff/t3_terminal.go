package main

import (
	"fmt"
	"strconv"
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
	return sshArgv(p, script)
}

// sshArgv runs script on p's host, in its folder when that exists there,
// on the T3 tunnel's own connection when its control socket is known (see
// remoteArgv).
func sshArgv(p t3palette.Place, script string) []string {
	if p.Dir != "" {
		script = "cd " + shellQuote(p.Dir) + " 2>/dev/null; " + script
	}
	argv := []string{"ssh", "-t", "-o", "ClearAllForwardings=yes"}
	if p.SSHControlPath != "" {
		argv = append(argv, "-o", "ControlMaster=no", "-S", p.SSHControlPath)
	}
	return append(argv, "--", p.SSHHost, script)
}

// remoteEditorArgv opens the host's editor on file (relative to p's folder;
// "" for none), at line when it is above 0, for e / f12 in a T3 review over
// SSH. The editor is the host's $EDITOR, then $VISUAL, then vi, read by an
// interactive login shell so the variable set in the user's profile or rc
// file counts. The line goes as revdiff passes it here: +N to vi, vim,
// nvim, nano and helix, --goto file:N to VS Code and its forks, nothing to
// other editors.
func remoteEditorArgv(p t3palette.Place, file string, line int) []string {
	args := ""
	switch {
	case file == "":
	case line > 0:
		n := strconv.Itoa(line)
		args = `case "$p" in vi|vim|nvim|nano|hx|helix) set -- +` + n + " " + shellQuote(file) + ` ;; ` +
			`code|code-insiders|codium|cursor) set -- --goto ` + shellQuote(file+":"+n) + ` ;; ` +
			`*) set -- ` + shellQuote(file) + ` ;; esac; `
	default:
		args = "set -- " + shellQuote(file) + "; "
	}
	script := `e=${EDITOR:-${VISUAL:-vi}}; p=${e%% *}; p=${p##*/}; ` + args + `exec $e "$@"`
	return sshArgv(p, `exec "${SHELL:-/bin/sh}" -lic `+shellQuote(script))
}

// shellQuote quotes s as one word for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
