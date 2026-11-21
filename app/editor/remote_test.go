package editor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
			cmd, ok := Editor{}.RemoteCommand("box", "", "/home/u/proj", tc.file, tc.line)
			require.True(t, ok)
			assert.Equal(t, []string{"/usr/bin/fresh", "--no-restore", tc.want}, cmd.Args)
			assert.Nil(t, cmd.Env, "no tunnel: the editor's own ssh, as it is")
		})
	}
}

func TestRemoteCommand_HostWithPort(t *testing.T) {
	t.Setenv("EDITOR", "fresh")
	cmd, ok := Editor{}.RemoteCommand("me@box:2222", "", "/srv", "a.go", 0)
	require.True(t, ok)
	assert.Equal(t, []string{"fresh", "ssh://me@box:2222/srv/a.go"}, cmd.Args, "host:port is the URL's own form")
}

func TestSSHOptions(t *testing.T) {
	assert.Equal(t, []string{"-o", "ClearAllForwardings=yes"}, SSHOptions(""), "no socket: a connection of its own")
	assert.Equal(t, []string{"-o", "ClearAllForwardings=yes", "-o", "ControlMaster=no", "-o", "ControlPath=/t/ssh.ctl"},
		SSHOptions("/t/ssh.ctl"))
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, `'a b'`, ShellQuote("a b"))
	assert.Equal(t, `'it'\''s'`, ShellQuote("it's"))
	out, err := exec.Command("sh", "-c", "printf %s "+ShellQuote(`$x "y" it's \z`)).Output() //nolint:gosec // the quoting under test
	require.NoError(t, err)
	assert.Equal(t, `$x "y" it's \z`, string(out))
}

func TestRemoteCommand_NotRemoteCapable(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	_, ok := Editor{}.RemoteCommand("box", "", "/home/u/proj", "a.go", 1)
	assert.False(t, ok, "vim edits the host's files only when run there")

	t.Setenv("EDITOR", "fresh")
	_, ok = Editor{}.RemoteCommand("", "", "/home/u/proj", "a.go", 1)
	assert.False(t, ok, "no host")
	_, ok = Editor{}.RemoteCommand("box", "", "", "a.go", 1)
	assert.False(t, ok, "no folder on the host")
}

func TestRemoteCommand_SharesTheTunnelsConnection(t *testing.T) {
	sys := t.TempDir() // the system's ssh: records what it was run with
	out := filepath.Join(t.TempDir(), "args")
	require.NoError(t, os.WriteFile(filepath.Join(sys, "ssh"), []byte("#!/bin/sh\nfor a; do echo \"$a\" >> "+out+"; done\n"), 0o700)) //nolint:gosec // test fixture
	t.Setenv("PATH", sys+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EDITOR", "fresh")
	tunnel := filepath.Join(t.TempDir(), "it's a dir")
	require.NoError(t, os.Mkdir(tunnel, 0o700))
	control := filepath.Join(tunnel, "ssh.ctl")

	cmd, ok := Editor{}.RemoteCommand("box", control, "/srv", "a.go", 3)
	require.True(t, ok)
	var path string
	for _, kv := range cmd.Env {
		if v, found := strings.CutPrefix(kv, "PATH="); found {
			path = v
		}
	}
	bin := filepath.Join(tunnel, "bin")
	require.True(t, strings.HasPrefix(path, bin+string(os.PathListSeparator)), "fresh finds the shared ssh first: %s", path)

	ssh := exec.Command("sh", "-c", `exec ssh -o BatchMode=yes box fresh-agent`) //nolint:gosec // the editor's own call, as fresh makes it
	ssh.Env = cmd.Env
	require.NoError(t, ssh.Run())
	got, err := os.ReadFile(out) //nolint:gosec // test-owned path
	require.NoError(t, err)
	assert.Equal(t, []string{"-o", "ClearAllForwardings=yes", "-o", "ControlMaster=no", "-o", "ControlPath=" + control,
		"-o", "BatchMode=yes", "box", "fresh-agent"}, strings.Split(strings.TrimSpace(string(got)), "\n"),
		"the system ssh, on the tunnel's connection, then what fresh asked")
	info, err := os.Stat(filepath.Join(bin, "ssh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}
