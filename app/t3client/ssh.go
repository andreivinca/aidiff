package t3client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
// process it records is gone, so one SSH login answers both. Another user's
// process counts as running, as in processRunning: kill -0 fails for it
// (EPERM) just as for a process that is gone, so ps settles which it is.
var remoteStateScript = `f="${T3CODE_HOME:-$HOME/.t3}/userdata/server-runtime.json"; ` +
	`[ -r "$f" ] || { echo "no T3 server state at $f" >&2; exit ` + strconv.Itoa(exitNoState) + `; }; ` +
	`pid=$(sed -n 's/.*"pid"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$f" | head -n 1); ` +
	`if [ -n "$pid" ] && ! { kill -0 "$pid" 2>/dev/null || ps -p "$pid" >/dev/null 2>&1; }; then exit ` + strconv.Itoa(exitNotRunning) + `; fi; ` +
	`cat "$f"`

// sshOptions apply to every ssh command: no prompts, and a dead link is
// noticed within 45s instead of hanging until TCP gives up.
var sshOptions = []string{"-o", "BatchMode=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}

// validSSHHost accepts a host for the ssh command line: [user@]host[:port]
// spelled with letters, digits and . _ @ : -, not starting with a dash, so
// it can be neither an option nor shell syntax, and with a port of 1-65535
// when it has one (see splitSSHHost).
func validSSHHost(host string) error {
	if host == "" || strings.HasPrefix(host, "-") {
		return fmt.Errorf("invalid ssh host %q", host)
	}
	for _, r := range host {
		if !sshHostRune(r) {
			return fmt.Errorf("invalid ssh host %q", host)
		}
	}
	dest, port := splitSSHHost(host)
	if dest == "" || strings.HasSuffix(dest, "@") {
		return fmt.Errorf("invalid ssh host %q: no host name", host)
	}
	if dest != host { // a port was split off
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid ssh host %q: bad port", host)
		}
	}
	return nil
}

// splitSSHHost splits [user@]host[:port] into ssh's destination
// ([user@]host) and port ("" for ssh's default). ssh reads no port from its
// destination itself: given box:2222 it looks for a host named "box:2222"
// on port 22. A host with more than one colon is an IPv6 literal, with no
// port.
func splitSSHHost(host string) (dest, port string) {
	at := strings.LastIndex(host, "@") + 1 // 0 without a user
	name, port, found := strings.Cut(host[at:], ":")
	if !found || strings.Contains(port, ":") {
		return host, ""
	}
	return host[:at] + name, port
}

// SSHArgs ends an ssh command line that reaches host, a [user@]host[:port]
// such as Session.SSHHost returns: its port as -p, then "--" and the
// destination, so nothing after the options can be read as one. The script
// to run, if any, follows.
func SSHArgs(host string) []string {
	dest, port := splitSSHHost(host)
	if port == "" {
		return []string{"--", dest}
	}
	return []string{"-p", port, "--", dest}
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
	ep := endpoint{base: "http://" + remoteAddr, client: tun.httpClient()}
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
	return exec.CommandContext(ctx, "ssh", sshArgv(host, script)...) //nolint:gosec // host validated by validSSHHost
}

// sshArgv is the arguments of an ssh command running script on host.
func sshArgv(host, script string) []string {
	args := append(append([]string{}, sshOptions...), SSHArgs(host)...)
	return append(args, script)
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
// Its ssh is also a ControlMaster, so other ssh commands to the host can share
// its connection instead of logging in again.
type sshTunnel struct {
	cmd       *exec.Cmd
	dir       string // 0700 directory holding the sockets
	socket    string
	control   string          // the ControlMaster socket
	lifeline  io.Closer       // the watchdog's stdin; the tunnel ends when it closes
	transport *http.Transport // carries HTTP through the socket
	stderr    *strings.Builder
	exited    chan struct{}
	err       error // valid once exited is closed
	stopOnce  sync.Once
}

// killGroup kills the process group pgid; a variable so tests can watch it.
var killGroup = func(pgid int) error {
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

// tunnelWatchdog runs the tunnel's ssh ("$@") and kills it once the shell's
// stdin, a pipe only this process writes to, reaches EOF. The kernel closes
// that pipe when this process ends for any reason, a crash or SIGKILL
// included, so the tunnel and every terminal sharing its connection end with
// it: ssh sits in its own process group, out of reach of the terminal's
// signals, and would otherwise stay connected forever. A background job's
// stdin is /dev/null, so the pipe reaches the watcher as fd 3, and ssh holds
// no copy of it. The shell exits with ssh's status, and stop kills the group.
const tunnelWatchdog = `exec 3<&0 </dev/null
"$@" 3<&- &
ssh=$!
{ read -r _ <&3; kill "$ssh" 2>/dev/null; } &
watch=$!
exec 3<&-
wait "$ssh"
status=$?
kill "$watch" 2>/dev/null
exit "$status"`

// startTunnel forwards a fresh local socket to remoteAddr through host. The
// tunnel outlives the connect timeout, so it is not tied to a context.
func startTunnel(host, remoteAddr string) (*sshTunnel, error) {
	dir, err := os.MkdirTemp("", "aidiff-t3-") // created 0700
	if err != nil {
		return nil, fmt.Errorf("create ssh tunnel directory: %w", err)
	}
	t := newTunnel(dir)
	if err := t.start(tunnelArgv(host, remoteAddr, t.socket, t.control)); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("start ssh tunnel to %s: %w", host, err)
	}
	return t, nil
}

// newTunnel is a tunnel, not yet started, with its sockets in dir.
func newTunnel(dir string) *sshTunnel {
	t := &sshTunnel{dir: dir, socket: filepath.Join(dir, "t3.sock"), control: filepath.Join(dir, "ssh.ctl"),
		stderr: &strings.Builder{}, exited: make(chan struct{})}
	t.transport = &http.Transport{DialContext: t.dialSocket}
	return t
}

// tunnelArgv is the tunnel's ssh command line, forwarding socket to
// remoteAddr through host and serving other ssh commands at control.
//
// ClearAllForwardings=no: a host block that clears forwardings would
// otherwise drop -L silently and the socket would never open.
// ControlMaster=yes with our own ControlPath, never the user's: the
// connection must be this tunnel's, ending with it.
func tunnelArgv(host, remoteAddr, socket, control string) []string {
	args := append(append([]string{"ssh", "-N"}, sshOptions...),
		"-o", "ClearAllForwardings=no", "-o", "ExitOnForwardFailure=yes", "-o", "StreamLocalBindUnlink=yes",
		"-o", "ControlMaster=yes", "-o", "ControlPersist=no", "-o", "ControlPath="+control,
		"-L", socket+":"+remoteAddr)
	return append(args, SSHArgs(host)...)
}

// start runs argv under tunnelWatchdog, in a process group of its own.
func (t *sshTunnel) start(argv []string) error {
	cmd := exec.Command("/bin/sh", append([]string{"-c", tunnelWatchdog, "sh"}, argv...)...) //nolint:gosec,noctx // host validated by validSSHHost; lives for the session
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}                                    // keep terminal signals away from it
	cmd.Stderr = t.stderr
	lifeline, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open lifeline: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("run ssh: %w", err)
	}
	t.cmd, t.lifeline = cmd, lifeline
	go t.wait()
	return nil
}

func (t *sshTunnel) wait() {
	t.err = t.cmd.Wait()
	close(t.exited)
}

// controlPath is the socket ssh commands to the tunnel's host can share its
// connection through (ssh -S), empty once the tunnel has exited.
func (t *sshTunnel) controlPath() string {
	select {
	case <-t.exited:
		return ""
	default:
		return t.control
	}
}

// stop ends the tunnel, waits for ssh to exit and removes its socket. The
// watchdog shell leads ssh's process group, so killing the group ends both.
// A tunnel that has exited is not killed: its shell is reaped, so its pid,
// the group's id, may already belong to another process. Only the first call
// does anything.
func (t *sshTunnel) stop() {
	t.stopOnce.Do(func() {
		t.transport.CloseIdleConnections()
		select {
		case <-t.exited:
		default:
			_ = killGroup(t.cmd.Process.Pid)
		}
		_ = t.lifeline.Close() // ends the watchdog, should ssh outlive its shell
		<-t.exited
		_ = os.RemoveAll(t.dir)
	})
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
