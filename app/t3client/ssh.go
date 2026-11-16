package t3client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// remoteT3 runs the t3 CLI on an SSH host, where a non-interactive shell often
// lacks ~/.local/bin on PATH.
const remoteT3 = `PATH="$HOME/.local/bin:$HOME/.t3/bin:$PATH" t3`

// Exit codes of remoteStateScript, apart from ssh's own 255.
const (
	exitNoState    = 3 // no runtime state file
	exitNotRunning = 4 // the recorded server process is gone
)

// remoteStateScript prints the remote runtime state, exiting with
// exitNoState when there is none and with exitNotRunning when the server
// process it records is gone, so one SSH login answers both.
var remoteStateScript = `f="${T3CODE_HOME:-$HOME/.t3}/userdata/server-runtime.json"; ` +
	`[ -r "$f" ] || { echo "no T3 server state at $f" >&2; exit ` + strconv.Itoa(exitNoState) + `; }; ` +
	`pid=$(sed -n 's/.*"pid"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$f" | head -n 1); ` +
	`if [ -n "$pid" ] && ! kill -0 "$pid" 2>/dev/null; then exit ` + strconv.Itoa(exitNotRunning) + `; fi; ` +
	`cat "$f"`

// sshOptions apply to every ssh command: no prompts, and a dead link is
// noticed within 45s instead of hanging until TCP gives up.
var sshOptions = []string{"-o", "BatchMode=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}

// validSSHHost accepts a host for the ssh command line: [user@]host[:port]
// spelled with letters, digits and . _ @ : -, not starting with a dash, so
// it can be neither an option nor shell syntax.
func validSSHHost(host string) error {
	if host == "" || strings.HasPrefix(host, "-") {
		return fmt.Errorf("invalid ssh host %q", host)
	}
	for _, r := range host {
		if !sshHostRune(r) {
			return fmt.Errorf("invalid ssh host %q", host)
		}
	}
	return nil
}

func sshHostRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("._@:-", r)
}

// connectSSH forwards a local socket to the T3 server on srv's host and connects.
func connectSSH(ctx context.Context, srv server, o ConnectOptions) (*Session, error) {
	host := srv.sshHost
	st, err := remoteState(ctx, host)
	if err != nil {
		return nil, err
	}
	remoteHost := st.Host
	if remoteHost == "" || remoteHost == "0.0.0.0" || remoteHost == "::" {
		remoteHost = "127.0.0.1"
	}
	remoteAddr := net.JoinHostPort(remoteHost, strconv.Itoa(st.Port))
	tun, err := startTunnel(host, remoteAddr)
	if err != nil {
		return nil, err
	}
	if werr := tun.waitListening(ctx, 15*time.Second); werr != nil {
		tun.stop()
		return nil, fmt.Errorf("ssh tunnel to %s: %w", host, werr)
	}
	issue := func(ctx context.Context) (string, error) {
		return runIssue(sshCommand(ctx, host, remoteT3+" "+strings.Join(issueArgs, " ")))
	}
	// requests name the server's own address, as a client on its host would
	ep := endpoint{origin: "http://" + remoteAddr, client: tun.httpClient()}
	s, err := connectHTTP(ctx, srv, ep, o, issue)
	if err != nil {
		tun.stop()
		return nil, err
	}
	s.tunnel = tun
	return s, nil
}

// remoteState reads the runtime state of the T3 server on host, failing
// when there is none or its process is gone.
func remoteState(ctx context.Context, host string) (runtimeState, error) {
	out, err := sshOutput(ctx, host, remoteStateScript)
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == exitNotRunning {
		return runtimeState{}, fmt.Errorf("T3 Code is not running on %s", host)
	}
	if err != nil {
		return runtimeState{}, fmt.Errorf("read T3 state on %s: %w", host, err)
	}
	var st runtimeState
	if jerr := json.Unmarshal(out, &st); jerr != nil || st.Port == 0 {
		return runtimeState{}, fmt.Errorf("unreadable T3 runtime state on %s", host)
	}
	return st, nil
}

// sshCommand runs script on host.
func sshCommand(ctx context.Context, host, script string) *exec.Cmd {
	args := append(append([]string{}, sshOptions...), "--", host, script)
	return exec.CommandContext(ctx, "ssh", args...) //nolint:gosec // host validated by validSSHHost
}

func sshOutput(ctx context.Context, host, script string) ([]byte, error) {
	cmd := sshCommand(ctx, host, script)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, fmt.Errorf("ssh: %w", err)
	}
	return out, nil
}

// sshTunnel is a running `ssh -N -L` forward from a Unix socket in a private
// directory. Only this user can create a socket there, so whatever answers
// on it is the tunnel, never another local process that took a port first.
type sshTunnel struct {
	cmd       *exec.Cmd
	dir       string // 0700 directory holding the socket
	socket    string
	transport *http.Transport // carries HTTP through the socket
	stderr    *strings.Builder
	exited    chan struct{}
	err       error // valid once exited is closed
}

// startTunnel forwards a fresh local socket to remoteAddr through host. The
// tunnel outlives the connect timeout, so it is not tied to a context.
func startTunnel(host, remoteAddr string) (*sshTunnel, error) {
	dir, err := os.MkdirTemp("", "aidiff-t3-") // created 0700
	if err != nil {
		return nil, fmt.Errorf("create ssh tunnel directory: %w", err)
	}
	socket := filepath.Join(dir, "t3.sock")
	// ClearAllForwardings=no: a host block that clears forwardings would
	// otherwise drop -L silently and the socket would never open
	args := append(append([]string{"-N"}, sshOptions...),
		"-o", "ClearAllForwardings=no", "-o", "ExitOnForwardFailure=yes", "-o", "StreamLocalBindUnlink=yes",
		"-L", socket+":"+remoteAddr, "--", host)
	cmd := exec.Command("ssh", args...)                   //nolint:gosec,noctx // host validated by validSSHHost; lives for the session
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // keep terminal signals away from it
	t := &sshTunnel{cmd: cmd, dir: dir, socket: socket, stderr: &strings.Builder{}, exited: make(chan struct{})}
	t.transport = &http.Transport{DialContext: t.dialSocket}
	cmd.Stderr = t.stderr
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("start ssh tunnel to %s: %w", host, err)
	}
	go t.wait()
	return t, nil
}

func (t *sshTunnel) wait() {
	t.err = t.cmd.Wait()
	close(t.exited)
}

// stop ends the tunnel, waits for ssh to exit and removes its socket.
func (t *sshTunnel) stop() {
	t.transport.CloseIdleConnections()
	_ = t.cmd.Process.Kill()
	<-t.exited
	_ = os.RemoveAll(t.dir)
}

// dialSocket connects to the tunnel's socket, whatever address is asked for.
func (t *sshTunnel) dialSocket(ctx context.Context, _, _ string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", t.socket)
	if err != nil {
		return nil, fmt.Errorf("dial ssh tunnel: %w", err)
	}
	return conn, nil
}

// httpClient carries requests through the tunnel and never follows a redirect.
func (t *sshTunnel) httpClient() *http.Client {
	return &http.Client{Transport: t.transport, CheckRedirect: refuseRedirect}
}

// waitListening waits for the socket to accept connections, failing early
// with ssh's own message when the tunnel exits first.
func (t *sshTunnel) waitListening(ctx context.Context, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	d := net.Dialer{Timeout: 500 * time.Millisecond}
	for {
		select {
		case <-t.exited:
			if msg := strings.TrimSpace(t.stderr.String()); msg != "" {
				return fmt.Errorf("ssh exited: %s", msg)
			}
			return fmt.Errorf("ssh exited: %w", t.err)
		default:
		}
		if c, err := d.DialContext(ctx, "unix", t.socket); err == nil {
			_ = c.Close()
			return nil
		}
		select {
		case <-t.exited:
		case <-ctx.Done():
			return errors.New("forwarded socket never opened")
		case <-time.After(150 * time.Millisecond):
		}
	}
}
