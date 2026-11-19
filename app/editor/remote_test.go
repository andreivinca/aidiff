package editor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteCommand_Fresh(t *testing.T) {
	t.Setenv("EDITOR", "/usr/bin/fresh --no-restore")
	t.Setenv("VISUAL", "")
	tests := []struct {
		name, file string
		line       int
		want       string
	}{
		{name: "file at line", file: "app/x.go", line: 12, want: "ssh://box/home/u/proj/app/x.go:12"},
		{name: "file without line", file: "app/x.go", want: "ssh://box/home/u/proj/app/x.go"},
		{name: "folder", line: 3, want: "ssh://box/home/u/proj"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd, ok := Editor{}.RemoteCommand("box", "/home/u/proj", tc.file, tc.line)
			require.True(t, ok)
			assert.Equal(t, []string{"/usr/bin/fresh", "--no-restore", tc.want}, cmd.Args)
		})
	}
}

func TestRemoteCommand_NotRemoteCapable(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	_, ok := Editor{}.RemoteCommand("box", "/home/u/proj", "a.go", 1)
	assert.False(t, ok, "vim edits the host's files only when run there")

	t.Setenv("EDITOR", "fresh")
	_, ok = Editor{}.RemoteCommand("", "/home/u/proj", "a.go", 1)
	assert.False(t, ok, "no host")
	_, ok = Editor{}.RemoteCommand("box", "", "a.go", 1)
	assert.False(t, ok, "no folder on the host")
}
