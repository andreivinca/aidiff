package t3client

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useLoginDir points the login sockets at a fresh directory for the test.
func useLoginDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ls-") // short: a socket path is limited
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	logins.mu.Lock()
	old := logins.dir
	logins.dir = dir
	logins.mu.Unlock()
	t.Cleanup(func() {
		logins.mu.Lock()
		logins.dir = old
		logins.mu.Unlock()
	})
	return dir
}

func TestNeedsLogin(t *testing.T) {
	assert.True(t, NeedsLogin(errors.New("read T3 state on workpc: exit status 255: u@h: Permission denied (publickey,password).")))
	assert.True(t, NeedsLogin(errors.New("ssh: Host key verification failed.")))
	assert.False(t, NeedsLogin(errors.New("T3 Code is not running on workpc")))
	assert.False(t, NeedsLogin(nil))
}

func TestLoginCommand(t *testing.T) {
	dir := useLoginDir(t)
	cmd, cleanup, err := LoginCommand("ssh://user@workpc:2222")
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	args := cmd.Args
	assert.Equal(t, "/bin/sh", args[0])
	i := slices.Index(args, "ssh")
	require.Positive(t, i)
	assert.Equal(t, []string{"user@workpc:2222", filepath.Join(dir, mustBase(t, "user@workpc:2222")) + ".log"}, args[i-2:i], "the script's host and log")
	ssh := strings.Join(args[i:], " ")
	assert.NotContains(t, ssh, "BatchMode", "the user answers it")
	assert.Contains(t, ssh, "-f -N")
	assert.Contains(t, ssh, "-o ControlMaster=yes")
	assert.Contains(t, ssh, "-o ControlPersist="+loginPersist)
	assert.True(t, strings.HasSuffix(ssh, "-p 2222 -- user@workpc"), ssh)

	_, _, err = LoginCommand("https://box.ts.net")
	require.Error(t, err, "only an SSH host has a login")
	_, _, err = LoginCommand("local")
	require.Error(t, err)
}

func mustBase(t *testing.T, host string) string {
	t.Helper()
	p, err := loginPath(host)
	require.NoError(t, err)
	return filepath.Base(p)
}

func TestSSHCommandsUseALoginOnlyWhileItIsUp(t *testing.T) {
	useLoginDir(t)
	argv := strings.Join(sshArgv("workpc", "true"), " ")
	assert.NotContains(t, argv, "ControlPath", "no login: ssh on its own")

	p, err := loginPath("workpc")
	require.NoError(t, err)
	l, err := net.Listen("unix", p) // the login's control socket
	require.NoError(t, err)
	argv = strings.Join(sshArgv("workpc", "true"), " ")
	assert.Contains(t, argv, "-o ControlMaster=no -o ControlPath="+p)
	assert.True(t, strings.HasSuffix(argv, "-- workpc true"), argv)
	assert.NotContains(t, strings.Join(sshArgv("box", "true"), " "), "ControlPath", "another host has its own")

	require.NoError(t, l.Close()) // removes the socket
	assert.Empty(t, loginControl("workpc"), "gone with its socket")
	require.NoError(t, os.WriteFile(p, nil, 0o600))
	assert.Empty(t, loginControl("workpc"), "a plain file is no login")
}

func TestLoginDirIsPrivate(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir, err := loginDir()
	require.NoError(t, err)
	fi, err := os.Lstat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
	again, err := loginDir()
	require.NoError(t, err)
	assert.Equal(t, dir, again, "one per user, reused")

	require.NoError(t, os.Chmod(dir, 0o755))
	other, err := loginDir()
	require.NoError(t, err)
	assert.NotEqual(t, dir, other, "one others can enter is not used")
}
