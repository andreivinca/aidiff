package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/umputun/revdiff/app/editor"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
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
// a real terminal, so ssh may prompt. Like a local one, the command is read
// by /bin/sh.
//
// The T3 tunnel's ssh already holds the host's forwards from ~/.ssh/config,
// so the terminal asks for none (editor.SSHOptions). With the tunnel's
// control socket the terminal is a session on the tunnel's own connection,
// with no second login; when the socket is gone ssh falls back to a
// connection of its own.
func (l terminalLauncher) remoteArgv(p t3palette.Place) []string {
	if p.Windows { // PowerShell or cmd there: a PowerShell, in the folder
		dest, port := sshDestination(p.SSHHost)
		argv := []string{"ssh", "-t"}
		if port != "" {
			argv = append(argv, "-p", port)
		}
		argv = append(argv, editor.SSHOptions(p.SSHControlPath)...)
		return append(argv, "--", dest, t3client.InteractivePowerShell(p.Dir, l.command))
	}
	script := `exec "${SHELL:-/bin/sh}" -l`
	if l.command != "" {
		script = l.command
	}
	return sshArgv(p, script)
}

// sshArgv runs script, POSIX shell code with args as $1…, on p's host, in
// its folder when that exists there, on the T3 tunnel's own connection when
// its control socket is known (see remoteArgv).
func sshArgv(p t3palette.Place, script string, args ...string) []string {
	if p.Dir != "" {
		script = `cd "$1" 2>/dev/null; shift; ` + script
		args = append([]string{p.Dir}, args...)
	}
	dest, port := sshDestination(p.SSHHost)
	argv := []string{"ssh", "-t"}
	if port != "" {
		argv = append(argv, "-p", port)
	}
	argv = append(argv, editor.SSHOptions(p.SSHControlPath)...)
	return append(argv, "--", dest, shCommand(script, args...))
}

// shCommand is a command line that runs script, POSIX shell code, with
// /bin/sh, args as $1…. The host reads what ssh sends with the user's login
// shell, which may be fish, csh or nushell, where POSIX syntax such as
// ${SHELL:-/bin/sh} is an error: so the script and its args go as quoted
// words to an exec of /bin/sh, which every shell reads alike. Our scripts
// hold no single quote and take the paths as args, so each word is one plain
// single-quoted string unless a path or a --terminal-command has a quote.
func shCommand(script string, args ...string) string {
	words := []string{"exec", "/bin/sh", "-c", editor.ShellQuote(script), "sh"}
	for _, a := range args {
		words = append(words, editor.ShellQuote(a))
	}
	return strings.Join(words, " ")
}

// sshDestination splits the port off an ssh host given as [user@]host:port,
// the form an ssh://host:port server names, since ssh takes the port as -p;
// an IPv6 address, with colons of its own, is left whole.
func sshDestination(host string) (dest, port string) {
	at := strings.LastIndex(host, "@") + 1
	name, port, found := strings.Cut(host[at:], ":")
	if !found || port == "" || strings.Contains(port, ":") || strings.Trim(port, "0123456789") != "" {
		return host, ""
	}
	return host[:at] + name, port
}

// remoteEditorArgv opens the host's editor on file (relative to p's folder;
// "" for none), at line when it is above 0, for e / f12 in a T3 review over
// SSH. The editor is the host's $EDITOR, then $VISUAL, then vi, read by an
// interactive login shell of the user's so the variable set in their profile
// or rc file counts; that shell runs the POSIX script below through /bin/sh
// in turn (shCommand), as it may not read POSIX itself. Its command line goes
// as an arg, quotes and all, which fish and csh read but nushell does not:
// there only the terminal panes work. The line goes as
// revdiff passes it here: +N to vi, vim, nvim, nano and helix, --goto
// file:N to VS Code and its forks, nothing to other editors.
func remoteEditorArgv(p t3palette.Place, file string, line int) []string {
	n := ""
	if line > 0 {
		n = strconv.Itoa(line)
	}
	// $1 the file, $2 the line, either one "" for none
	edit := `e=${EDITOR:-${VISUAL:-vi}}; p=${e%% *}; p=${p##*/}; ` +
		`if [ -z "$1" ]; then set --; elif [ -z "$2" ]; then set -- "$1"; else case "$p" in ` +
		`vi|vim|nvim|nano|hx|helix) set -- "+$2" "$1" ;; ` +
		`code|code-insiders|codium|cursor) set -- --goto "$1:$2" ;; ` +
		`*) set -- "$1" ;; esac; fi; exec $e "$@"`
	return sshArgv(p, `exec "${SHELL:-/bin/sh}" -lic "$1"`, shCommand(edit, file, n))
}
