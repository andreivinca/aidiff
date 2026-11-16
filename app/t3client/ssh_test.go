package t3client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runStateScript runs remoteStateScript here, against a T3 home holding state.
func runStateScript(t *testing.T, state string) (string, int) {
	t.Helper()
	home := t.TempDir()
	if state != "" {
		require.NoError(t, os.MkdirAll(filepath.Join(home, "userdata"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(home, "userdata", "server-runtime.json"), []byte(state), 0o600))
	}
	cmd := exec.Command("sh", "-c", remoteStateScript)
	cmd.Env = append(os.Environ(), "T3CODE_HOME="+home)
	out, err := cmd.Output()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return string(out), exit.ExitCode()
	}
	require.NoError(t, err)
	return string(out), 0
}

func TestRemoteStateScript(t *testing.T) {
	live := `{"pid": ` + strconv.Itoa(os.Getpid()) + `, "host": "127.0.0.1", "port": 3773}`
	out, code := runStateScript(t, live)
	assert.Equal(t, 0, code)
	assert.Equal(t, live, out)

	// a pid far above any pid_max, so no process has it
	_, code = runStateScript(t, "{\n  \"pid\": 999999999,\n  \"port\": 3773\n}\n")
	assert.Equal(t, exitNotRunning, code)

	_, code = runStateScript(t, `{"port": 3773}`)
	assert.Equal(t, 0, code, "a state without a pid is taken as is")

	_, code = runStateScript(t, "")
	assert.Equal(t, exitNoState, code)
}

// tunnelTo is a tunnel whose socket serves h, as ssh's forward would.
func tunnelTo(t *testing.T, h http.Handler) *sshTunnel {
	t.Helper()
	dir, err := os.MkdirTemp("", "t3-test-") // short: socket paths are limited
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	tun := &sshTunnel{dir: dir, socket: filepath.Join(dir, "t3.sock"), stderr: &strings.Builder{}, exited: make(chan struct{})}
	tun.transport = &http.Transport{DialContext: tun.dialSocket}
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "unix", tun.socket)
	require.NoError(t, err)
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return tun
}

func TestSSHTunnel_CarriesTheConnectionThroughItsSocket(t *testing.T) {
	tun := tunnelTo(t, &fakeServer{t: t, token: "tok", handler: func(ws *websocket.Conn, req map[string]any) {
		send(t, ws, success(req["id"], nil))
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, tun.waitListening(ctx, time.Second))
	c, err := dial(ctx, endpoint{origin: "http://127.0.0.1:3773", client: tun.httpClient()}, "tok")
	require.NoError(t, err)
	defer func() {
		_ = c.close()
	}()
	require.NoError(t, c.call(ctx, "x", nil, nil))
}

func TestSSHTunnel_WaitReportsSSHExiting(t *testing.T) {
	tun := &sshTunnel{socket: filepath.Join(t.TempDir(), "t3.sock"), stderr: &strings.Builder{}, exited: make(chan struct{})}
	tun.stderr.WriteString("Permission denied (publickey).\n")
	close(tun.exited)
	err := tun.waitListening(context.Background(), time.Second)
	require.ErrorContains(t, err, "ssh exited: Permission denied (publickey).")
}
