package t3client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ConnectOptions configure how Connect authenticates.
type ConnectOptions struct {
	Store TokenStore
	Token string // explicit bearer token; skips the store
	Pair  string // pairing credential or `t3 pair` link to exchange first
}

// Session is a live connection plus whatever was started to reach it.
// Close it with Session.Close, which also ends an SSH tunnel.
type Session struct {
	conn    *Conn
	server  server
	opts    ConnectOptions // what the session was made with, for Redial
	tunnel  *sshTunnel
	notices []string
}

// Conn is the session's connection.
func (s *Session) Conn() *Conn {
	return s.conn
}

// Label is the server as the user named it: local, ssh://host or a URL.
func (s *Session) Label() string {
	return s.server.label
}

// SSHHost is the SSH host the session tunnels through, empty for a local or
// HTTP session.
func (s *Session) SSHHost() string {
	return s.server.sshHost
}

// Local reports whether the server runs on this machine: "local", or a URL
// on loopback. An SSH session's tunnel ends here, but its server does not.
func (s *Session) Local() bool {
	return s.server.kind == serverLocal || (s.server.kind == serverHTTP && isLoopback(s.server.origin))
}

// MachineName identifies the connected host, preserving SSH aliases and avoiding
// tunnel loopback addresses, credentials, and ports in the status bar.
func (s *Session) MachineName() string {
	if s.server.kind == serverLocal {
		name, _ := os.Hostname()
		return name
	}
	// the label is ssh://host or the server's origin
	if u, err := url.Parse(s.server.label); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "remote"
}

// Notices are what the user should know about how the session was reached
// but that did not stop it: a token that could not be saved, credentials
// sent unencrypted.
func (s *Session) Notices() []string {
	return s.notices
}

// Redial connects again to the same server, with the options this session
// was made with. A pairing credential is spent by the first exchange, so a
// session made by pairing redials with the token that pairing produced.
func (s *Session) Redial(ctx context.Context) (*Session, error) {
	return connect(ctx, s.server, s.opts)
}

// Close ends the connection and any SSH tunnel.
func (s *Session) Close() error {
	var err error
	if s.conn != nil {
		err = s.conn.close()
	}
	if s.tunnel != nil {
		s.tunnel.stop()
	}
	return err
}

// serverKind is how a server is reached.
type serverKind int

const (
	serverLocal serverKind = iota // this user's server on this machine
	serverSSH                     // a server on an SSH host, through a tunnel
	serverHTTP                    // a server reached at its URL
)

// server is a T3 server as the user named it.
type server struct {
	kind    serverKind
	label   string // local, ssh://host or the server's origin; tokens are saved under it
	sshHost string // serverSSH only, validated for the ssh command line
	origin  string // serverHTTP only
}

// parseServer reads a server spec: "local", "ssh://host" or "http(s)://host[:port]".
func parseServer(spec string) (server, error) {
	spec = strings.TrimSpace(spec)
	switch {
	case spec == "" || spec == "local":
		return server{kind: serverLocal, label: "local"}, nil
	case strings.HasPrefix(spec, "ssh://"):
		host := strings.TrimSuffix(strings.TrimPrefix(spec, "ssh://"), "/")
		if err := validSSHHost(host); err != nil {
			return server{}, err
		}
		return server{kind: serverSSH, label: "ssh://" + host, sshHost: host}, nil
	case strings.HasPrefix(spec, "http://"), strings.HasPrefix(spec, "https://"):
		origin := normalizeURL(spec)
		return server{kind: serverHTTP, label: origin, origin: origin}, nil
	}
	return server{}, fmt.Errorf("unknown T3 server %q: use local, ssh://host or http(s)://host:port", spec)
}

// Connect reaches a T3 server named by spec:
//   - "local": the server this user runs here, found through its runtime state file
//   - "ssh://host": the server on host, reached through an SSH port forward; the
//     token is issued on host by its t3 CLI over the same SSH login
//   - "http(s)://host[:port]": a server reached directly (LAN, Tailscale, tunnel)
//
// An empty spec means the server a pairing link names, else local.
func Connect(ctx context.Context, spec string, o ConnectOptions) (*Session, error) {
	if strings.TrimSpace(spec) == "" && o.Pair != "" {
		_, spec = pairingCredential(o.Pair)
	}
	srv, err := parseServer(spec)
	if err != nil {
		return nil, err
	}
	return connect(ctx, srv, o)
}

// connect reaches srv, authenticating as o says.
func connect(ctx context.Context, srv server, o ConnectOptions) (*Session, error) {
	switch srv.kind {
	case serverLocal:
		origin, err := LocalOrigin()
		if err != nil {
			return nil, err
		}
		return connectHTTP(ctx, srv, directEndpoint(origin), o, issueLocal)
	case serverSSH:
		return connectSSH(ctx, srv, o)
	default: // serverHTTP
		var issue issuer
		if isLoopback(srv.origin) {
			issue = issueLocal
		}
		return connectHTTP(ctx, srv, directEndpoint(srv.origin), o, issue)
	}
}

// issuer mints a token without user help.
type issuer func(context.Context) (string, error)

// endpoint is how to reach a T3 server's HTTP origin.
type endpoint struct {
	origin string       // http(s)://host[:port], what requests are addressed to
	client *http.Client // carries them; never follows a redirect
}

// directEndpoint reaches origin over the network.
func directEndpoint(origin string) endpoint {
	return endpoint{origin: origin, client: &http.Client{CheckRedirect: refuseRedirect}}
}

// refuseRedirect keeps a redirect from carrying a credential to another URL;
// the redirect response itself then reads as a failure.
func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// connectHTTP authenticates and dials ep. issue, when set, mints a token
// without user help; it is retried once when a saved token turns out to be
// revoked or expired.
func connectHTTP(ctx context.Context, srv server, ep endpoint, o ConnectOptions, issue issuer) (*Session, error) {
	s := &Session{server: srv, opts: o}
	if srv.kind == serverHTTP && !strings.HasPrefix(ep.origin, "https://") && !isLoopback(ep.origin) {
		s.notice("the T3 token travels unencrypted to %s; use https:// or ssh:// to protect it", ep.origin)
	}
	token, fromStore, err := s.resolveToken(ctx, ep, issue)
	if err != nil {
		return nil, err
	}
	conn, err := dial(ctx, ep, token)
	if errors.Is(err, errUnauthorized) && fromStore && issue != nil {
		if token, err = issue(ctx); err == nil {
			s.save(token)
			conn, err = dial(ctx, ep, token)
		}
	}
	if errors.Is(err, errUnauthorized) {
		return nil, fmt.Errorf("%w; run `t3 pair` on the T3 host and pass the link with --t3-pair", err)
	}
	if err != nil {
		return nil, err
	}
	s.conn = conn
	return s, nil
}

// resolveToken picks the token to dial with: an explicit one, a pairing
// exchange, a saved one, or a freshly issued one, in that order.
func (s *Session) resolveToken(ctx context.Context, ep endpoint, issue issuer) (token string, fromStore bool, err error) {
	o := s.opts
	if o.Token != "" {
		return o.Token, false, nil
	}
	if o.Pair != "" {
		cred, _ := pairingCredential(o.Pair)
		if token, err = pair(ctx, ep, cred); err != nil {
			return "", false, err
		}
		s.opts.Pair, s.opts.Token = "", token // the credential is spent
		s.save(token)
		return token, false, nil
	}
	if token, err = o.Store.Get(s.server.label); err != nil {
		return "", false, err
	}
	if token != "" {
		return token, true, nil
	}
	if issue == nil {
		return "", false, fmt.Errorf("no T3 token for %s: run `t3 pair` on the T3 host and pass the link with --t3-pair", s.server.label)
	}
	if token, err = issue(ctx); err != nil {
		return "", false, err
	}
	s.save(token)
	return token, false, nil
}

// save keeps a freshly obtained token for later sessions. Failing to save
// does not fail this one, which holds the token already, so it becomes a
// notice.
func (s *Session) save(token string) {
	if err := s.opts.Store.Put(s.server.label, token); err != nil {
		s.notice("the T3 token for %s was not saved, so the next connection needs a new one: %v", s.server.label, err)
	}
}

func (s *Session) notice(format string, args ...any) {
	s.notices = append(s.notices, fmt.Sprintf(format, args...))
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
