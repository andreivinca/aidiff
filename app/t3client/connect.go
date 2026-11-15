package t3client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ConnectOptions configure how Connect authenticates.
type ConnectOptions struct {
	Store TokenStore
	Token string // explicit bearer token; skips the store
	Pair  string // pairing credential or `t3 pair` link to exchange first
}

// Session is a live connection plus whatever was started to reach it.
type Session struct {
	*Conn
	Label  string // what the user named: local, ssh://host or a URL
	tunnel *sshTunnel
}

// Close ends the connection and any SSH tunnel.
func (s *Session) Close() error {
	err := s.Conn.Close()
	if s.tunnel != nil {
		s.tunnel.stop()
	}
	return err
}

// Connect reaches a T3 server named by spec:
//   - "local": the server this user runs here, found through its runtime state file
//   - "ssh://host": the server on host, reached through an SSH port forward; the
//     token is issued on host by its t3 CLI over the same SSH login
//   - "http(s)://host[:port]": a server reached directly (LAN, Tailscale, tunnel)
//
// An empty spec means the server a pairing link names, else local.
func Connect(ctx context.Context, spec string, o ConnectOptions) (*Session, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" && o.Pair != "" {
		_, spec = PairingCredential(o.Pair)
	}
	switch {
	case spec == "" || spec == "local":
		origin, err := LocalOrigin()
		if err != nil {
			return nil, err
		}
		return connectHTTP(ctx, origin, "local", o, issueLocal)
	case strings.HasPrefix(spec, "ssh://"):
		return connectSSH(ctx, strings.TrimSuffix(strings.TrimPrefix(spec, "ssh://"), "/"), o)
	case strings.HasPrefix(spec, "http://"), strings.HasPrefix(spec, "https://"):
		var issue func(context.Context) (string, error)
		if IsLoopback(spec) {
			issue = issueLocal
		}
		origin := NormalizeURL(spec)
		return connectHTTP(ctx, origin, origin, o, issue)
	}
	return nil, fmt.Errorf("unknown T3 server %q: use local, ssh://host or http(s)://host:port", spec)
}

// connectHTTP authenticates and dials origin. Tokens are saved under key.
// issue, when set, mints a token without user help; it is retried once when
// a saved token turns out to be revoked or expired.
func connectHTTP(ctx context.Context, origin, key string, o ConnectOptions, issue func(context.Context) (string, error)) (*Session, error) {
	token, fromStore, err := resolveToken(ctx, origin, key, o, issue)
	if err != nil {
		return nil, err
	}
	conn, err := Dial(ctx, origin, token)
	if errors.Is(err, ErrUnauthorized) && fromStore && issue != nil {
		if token, err = issue(ctx); err == nil {
			if err = o.Store.Put(key, token); err == nil {
				conn, err = Dial(ctx, origin, token)
			}
		}
	}
	if errors.Is(err, ErrUnauthorized) {
		return nil, fmt.Errorf("%w; run `t3 pair` on the T3 host and pass the link with --t3-pair", err)
	}
	if err != nil {
		return nil, err
	}
	return &Session{Conn: conn, Label: key}, nil
}

// resolveToken picks the token to dial with: an explicit one, a pairing
// exchange, a saved one, or a freshly issued one, in that order.
func resolveToken(ctx context.Context, origin, key string, o ConnectOptions, issue func(context.Context) (string, error)) (token string, fromStore bool, err error) {
	if o.Token != "" {
		return o.Token, false, nil
	}
	if o.Pair != "" {
		cred, _ := PairingCredential(o.Pair)
		if token, err = Pair(ctx, origin, cred); err != nil {
			return "", false, err
		}
		return token, false, o.Store.Put(key, token)
	}
	if token, err = o.Store.Get(key); err != nil {
		return "", false, err
	}
	if token != "" {
		return token, true, nil
	}
	if issue == nil {
		return "", false, fmt.Errorf("no T3 token for %s: run `t3 pair` on the T3 host and pass the link with --t3-pair", key)
	}
	if token, err = issue(ctx); err != nil {
		return "", false, err
	}
	return token, false, o.Store.Put(key, token)
}

// runtimeState is T3's record of its running server (userdata/server-runtime.json).
type runtimeState struct {
	PID    int    `json:"pid"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Origin string `json:"origin"`
}

// LocalOrigin finds the T3 server this user runs on this machine.
func LocalOrigin() (string, error) {
	home := os.Getenv("T3CODE_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		home = filepath.Join(h, ".t3")
	}
	path := filepath.Join(home, "userdata", "server-runtime.json")
	b, err := os.ReadFile(path) //nolint:gosec // fixed location under the user's T3 home
	if err != nil {
		return "", fmt.Errorf("no running T3 server found (%w); start T3 Code or pass its URL to --t3", err)
	}
	var st runtimeState
	if err := json.Unmarshal(b, &st); err != nil || st.Origin == "" {
		return "", fmt.Errorf("unreadable T3 runtime state %s", path)
	}
	if st.PID > 0 {
		if err := syscall.Kill(st.PID, 0); err != nil && !errors.Is(err, syscall.EPERM) {
			return "", errors.New("T3 Code is not running; start it or pass its URL to --t3")
		}
	}
	return st.Origin, nil
}

// remoteT3 runs the t3 CLI on an SSH host, where a non-interactive shell often
// lacks ~/.local/bin on PATH.
const remoteT3 = `PATH="$HOME/.local/bin:$HOME/.t3/bin:$PATH" t3`

// remoteStateScript prints the remote runtime state, failing when the
// recorded server process is gone.
const remoteStateScript = `f="${T3CODE_HOME:-$HOME/.t3}/userdata/server-runtime.json"; ` +
	`[ -r "$f" ] || { echo "no T3 server state at $f" >&2; exit 3; }; cat "$f"`

// connectSSH forwards a local port to the T3 server on host and connects.
func connectSSH(ctx context.Context, host string, o ConnectOptions) (*Session, error) {
	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsAny(host, " \t/'\"`$;&|") {
		return nil, fmt.Errorf("invalid ssh host %q", host)
	}
	out, err := sshOutput(ctx, host, remoteStateScript)
	if err != nil {
		return nil, fmt.Errorf("read T3 state on %s: %w", host, err)
	}
	var st runtimeState
	if jerr := json.Unmarshal(out, &st); jerr != nil || st.Port == 0 {
		return nil, fmt.Errorf("unreadable T3 runtime state on %s", host)
	}
	if st.PID > 0 {
		if _, kerr := sshOutput(ctx, host, "kill -0 "+strconv.Itoa(st.PID)); kerr != nil {
			return nil, fmt.Errorf("T3 Code is not running on %s", host)
		}
	}
	remoteHost := st.Host
	if remoteHost == "" || remoteHost == "0.0.0.0" || remoteHost == "::" {
		remoteHost = "127.0.0.1"
	}

	port, err := freePort(ctx)
	if err != nil {
		return nil, err
	}
	tun, err := startTunnel(host, fmt.Sprintf("127.0.0.1:%d:%s:%d", port, remoteHost, st.Port))
	if err != nil {
		return nil, err
	}
	if werr := tun.waitListening(ctx, port, 15*time.Second); werr != nil {
		tun.stop()
		return nil, fmt.Errorf("ssh tunnel to %s: %w", host, werr)
	}
	issue := func(ctx context.Context) (string, error) {
		return runIssue(exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "--", host, //nolint:gosec // host validated above
			remoteT3+" "+strings.Join(issueArgs, " ")))
	}
	s, err := connectHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d", port), "ssh://"+host, o, issue)
	if err != nil {
		tun.stop()
		return nil, err
	}
	s.tunnel = tun
	return s, nil
}

// sshTunnel is a running `ssh -N -L` port forward.
type sshTunnel struct {
	cmd    *exec.Cmd
	stderr *strings.Builder
	exited chan struct{}
	err    error // valid once exited is closed
}

// startTunnel forwards spec (local:port:remote:port) through host. The
// tunnel outlives the connect timeout, so it is not tied to a context.
func startTunnel(host, spec string) (*sshTunnel, error) {
	// ClearAllForwardings=no: a host block that clears forwardings would
	// otherwise drop -L silently and the port would never open
	cmd := exec.Command("ssh", "-N", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=no", //nolint:gosec,noctx // host validated by caller; lives for the session
		"-o", "ExitOnForwardFailure=yes", "-L", spec, "--", host)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // keep terminal signals away from it
	t := &sshTunnel{cmd: cmd, stderr: &strings.Builder{}, exited: make(chan struct{})}
	cmd.Stderr = t.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ssh tunnel to %s: %w", host, err)
	}
	go func() {
		t.err = cmd.Wait()
		close(t.exited)
	}()
	return t, nil
}

// stop ends the tunnel and waits for ssh to exit.
func (t *sshTunnel) stop() {
	_ = t.cmd.Process.Kill()
	<-t.exited
}

// waitListening waits for the forwarded port to accept connections, failing
// early with ssh's own message when the tunnel exits first.
func (t *sshTunnel) waitListening(ctx context.Context, port int, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	d := net.Dialer{Timeout: 500 * time.Millisecond}
	for {
		if c, err := d.DialContext(ctx, "tcp", addr); err == nil {
			_ = c.Close()
			return nil
		}
		select {
		case <-t.exited:
			if msg := strings.TrimSpace(t.stderr.String()); msg != "" {
				return fmt.Errorf("ssh exited: %s", msg)
			}
			return fmt.Errorf("ssh exited: %w", t.err)
		case <-ctx.Done():
			return errors.New("forwarded port never opened")
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func sshOutput(ctx context.Context, host, script string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "--", host, script) //nolint:gosec // host validated by caller
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

func freePort(ctx context.Context) (int, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("pick a local port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// MachineName identifies the connected host, preserving SSH aliases and avoiding
// tunnel loopback addresses, credentials, and ports in the status bar.
func (s *Session) MachineName() string {
	if s.Label == "local" {
		name, _ := os.Hostname()
		return name
	}
	if u, err := url.Parse(s.Label); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "remote"
}
