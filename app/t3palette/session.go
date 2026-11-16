package t3palette

import (
	"context"
	"fmt"
	"time"

	"github.com/umputun/revdiff/app/t3client"
)

// ConnectTimeout bounds reaching a T3 server, SSH login and tunnel included,
// and listing its threads.
const ConnectTimeout = 45 * time.Second

// Session is a live connection to a T3 server as the palette uses it.
// Connect adapts a *t3client.Session to it. The palette tells sessions apart
// by identity, so an implementation is a pointer.
type Session interface {
	// Label is the server as the user named it: local, ssh://host or a URL.
	Label() string
	// SSHHost is the host the session tunnels through, empty when none.
	SSHHost() string
	// SSHControlPath is the socket that shares the SSH tunnel's connection,
	// empty when there is none.
	SSHControlPath() string
	// Local reports whether the server runs on this machine, so that its
	// folders are this machine's.
	Local() bool
	// MachineName names the server's host for the status bar.
	MachineName() string
	// Notices are what the user should know about how the session was
	// reached, though it did not stop it.
	Notices() []string
	// Server is the connection the palette's requests go over.
	Server() Server
	// Conn is the same connection for the chat panes and the review's
	// renderer, which take it as it is.
	Conn() *t3client.Conn
	// Redial connects again to the same server, the way this session was
	// made. Its error says it was a reconnect, as Connect's says connect.
	Redial(ctx context.Context) (Session, error)
	// Close ends the connection and whatever was started to reach it.
	Close() error
}

// Server is what the palette asks of a connected T3 server;
// *t3client.Conn implements it.
type Server interface {
	FetchShell(ctx context.Context) (t3client.Shell, error)
	WatchThread(ctx context.Context, threadID string, onChange func(t3client.Thread)) error
	Browse(ctx context.Context, partialPath string) (parent string, entries []t3client.DirEntry, err error)
	CreateProject(ctx context.Context, title, workspaceRoot string) (t3client.Project, error)
	DefaultModel(ctx context.Context, p t3client.Project) (t3client.ModelSelection, error)
	LaunchThread(ctx context.Context, req t3client.LaunchRequest) (string, error)
	SendMessage(ctx context.Context, threadID, text string) error
	ListRefs(ctx context.Context, cwd string) ([]t3client.Ref, error)
}

// Connect reaches the server named by spec, authenticating as o says (see
// t3client.Connect).
func Connect(ctx context.Context, spec string, o t3client.ConnectOptions) (Session, error) {
	s, err := t3client.Connect(ctx, spec, o)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return &clientSession{Session: s}, nil
}

// clientSession is a *t3client.Session as a Session.
type clientSession struct {
	*t3client.Session
}

// Server is the session's connection.
func (s *clientSession) Server() Server {
	return s.Conn()
}

// Redial connects again with the options the session was made with.
func (s *clientSession) Redial(ctx context.Context) (Session, error) {
	next, err := s.Session.Redial(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconnect: %w", err)
	}
	return &clientSession{Session: next}, nil
}

// reach dials a session with dial and lists its server's projects and
// threads. A session whose listing fails is closed.
func reach(ctx context.Context, dial func(context.Context) (Session, error)) (Session, t3client.Shell, error) {
	s, err := dial(ctx)
	if err != nil {
		return nil, t3client.Shell{}, err
	}
	sh, err := s.Server().FetchShell(ctx)
	if err != nil {
		_ = s.Close()
		return nil, t3client.Shell{}, fmt.Errorf("list threads: %w", err)
	}
	return s, sh, nil
}
