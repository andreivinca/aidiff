package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTerminalLauncher_Argv(t *testing.T) {
	at := func(host, dir string) func() (string, string) {
		return func() (string, string) {
			return host, dir
		}
	}
	tests := []struct {
		name     string
		launcher terminalLauncher
		argv     []string
		dir      string
	}{
		{
			name:     "local shell in the reviewed folder",
			launcher: terminalLauncher{place: at("", "/home/me/wt")},
			dir:      "/home/me/wt",
		},
		{
			name:     "local command is read by a shell",
			launcher: terminalLauncher{command: `tmux new -A -s 'my review'`, place: at("", "/home/me/wt")},
			argv:     []string{"/bin/sh", "-c", `tmux new -A -s 'my review'`},
			dir:      "/home/me/wt",
		},
		{
			name:     "no folder known, e.g. a remote server without ssh",
			launcher: terminalLauncher{command: "zsh -l", place: at("", "")},
			argv:     []string{"/bin/sh", "-c", "zsh -l"},
		},
		{
			name:     "ssh login shell",
			launcher: terminalLauncher{place: at("box", "")},
			argv:     []string{"ssh", "-t", "--", "box", `exec "${SHELL:-/bin/sh}" -l`},
		},
		{
			name:     "ssh in a folder, quoted for the remote shell",
			launcher: terminalLauncher{place: at("box", "/srv/it's")},
			argv:     []string{"ssh", "-t", "--", "box", `cd '/srv/it'\''s' 2>/dev/null; exec "${SHELL:-/bin/sh}" -l`},
		},
		{
			name:     "ssh runs the terminal command",
			launcher: terminalLauncher{command: `tmux new -A -s 'my review'`, place: at("box", "/srv/wt")},
			argv:     []string{"ssh", "-t", "--", "box", `cd '/srv/wt' 2>/dev/null; tmux new -A -s 'my review'`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argv, dir := tt.launcher.argv()
			assert.Equal(t, tt.argv, argv)
			assert.Equal(t, tt.dir, dir)
		})
	}
}
