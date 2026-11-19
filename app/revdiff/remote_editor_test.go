package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/t3palette"
)

// sshSession is a T3 session over SSH to host.
type sshSession struct {
	t3palette.Session
	host, ctl string
}

func (s sshSession) Local() bool            { return false }
func (s sshSession) SSHHost() string        { return s.host }
func (s sshSession) SSHControlPath() string { return s.ctl }

// runRemote runs what argv would run on the SSH host with a local shell,
// EDITOR set to editor: a fake named name that records its folder and
// arguments, one per line.
func runRemote(t *testing.T, argv []string, name string) []string {
	t.Helper()
	require.Equal(t, "ssh", argv[0])
	bin, out := t.TempDir(), filepath.Join(t.TempDir(), "args")
	fake := filepath.Join(bin, name)
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\npwd > "+out+"\nfor a; do echo \"$a\" >> "+out+"; done\n"), 0o700)) //nolint:gosec // test fixture
	cmd := exec.Command("/bin/sh", "-c", argv[len(argv)-1])                                                                     //nolint:gosec // the command under test
	cmd.Env = []string{"HOME=" + t.TempDir(), "SHELL=/bin/sh", "PATH=" + os.Getenv("PATH"), "EDITOR=" + fake + " --flag"}
	got, err := cmd.CombinedOutput()
	require.NoError(t, err, string(got))
	data, err := os.ReadFile(out) //nolint:gosec // test-owned path
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestRemoteEditorArgv(t *testing.T) {
	dir := t.TempDir()
	p := t3palette.Place{SSHHost: "box", SSHControlPath: "/tmp/ctl", Dir: dir}
	argv := remoteEditorArgv(p, "src/it's.go", 12)
	assert.Equal(t, []string{"ssh", "-t", "-o", "ClearAllForwardings=yes", "-o", "ControlMaster=no", "-S", "/tmp/ctl", "--", "box"}, argv[:len(argv)-1],
		"on the terminal's own SSH connection")

	assert.Equal(t, []string{dir, "--flag", "+12", "src/it's.go"}, runRemote(t, argv, "nvim"), "in the folder, the line as nvim takes it")
	assert.Equal(t, []string{dir, "--flag", "--goto", "src/it's.go:12"}, runRemote(t, argv, "code"))
	assert.Equal(t, []string{dir, "--flag", "src/it's.go"}, runRemote(t, argv, "fresh"), "an editor revdiff does not know gets the file alone")
	assert.Equal(t, []string{dir, "--flag", "a.go"}, runRemote(t, remoteEditorArgv(p, "a.go", 0), "nvim"), "no line")
	assert.Equal(t, []string{dir, "--flag"}, runRemote(t, remoteEditorArgv(p, "", 0), "nvim"), "no file: the editor in the folder")
}

func TestRemoteEditor(t *testing.T) {
	assert.Nil(t, remoteEditor(sourceSetup{}), "a local review edits here")
	r := &t3palette.Review{Session: localSession{local: true}, Target: t3client.Target{Cwd: "/srv"}}
	assert.Nil(t, remoteEditor(sourceSetup{t3: r}), "a T3 server on this machine edits here")
	r.Session = sshSession{host: ""}
	assert.Nil(t, remoteEditor(sourceSetup{t3: r}), "a remote host reached without SSH: no way to its files")
	r.Session = sshSession{host: "box", ctl: "/tmp/ctl"}
	edit := remoteEditor(sourceSetup{t3: r})
	require.NotNil(t, edit)

	t.Setenv("EDITOR", "nvim")
	t.Setenv("VISUAL", "")
	cmd := edit("a.go", 3)
	assert.Equal(t, remoteEditorArgv(t3palette.Place{SSHHost: "box", SSHControlPath: "/tmp/ctl", Dir: "/srv"}, "a.go", 3), cmd.Args,
		"an editor that edits only its own machine's files runs on the host")

	t.Setenv("EDITOR", "fresh")
	assert.Equal(t, []string{"fresh", "ssh://box/srv/a.go:3"}, edit("a.go", 3).Args,
		"fresh runs here and opens the host's file over SSH")
}
