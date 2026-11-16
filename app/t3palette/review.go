package t3palette

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// Review is a T3 review: the session it reads over and the target it shows,
// a thread's worktree or a project's root.
type Review struct {
	Session Session
	Target  t3client.Target
	// Source is what the review compares; the zero source is the run's
	// --t3-source.
	Source t3client.DiffSource
	// SourceLabel names Source for the user; "" for the zero source.
	SourceLabel string
}

// Open connects with o to the server named by spec and resolves the review
// of threadID, an id or a unique prefix of one; "" takes the most recently
// updated thread. It is how a run starts with a T3 review.
func Open(ctx context.Context, spec string, o t3client.ConnectOptions, threadID string) (*Review, error) {
	dial := func(ctx context.Context) (Session, error) {
		return Connect(ctx, spec, o)
	}
	s, sh, err := reach(ctx, dial)
	if err != nil {
		return nil, err
	}
	target, err := t3client.Resolve(sh, threadID)
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("pick the thread: %w", err)
	}
	return &Review{Session: s, Target: target}, nil
}

// Description names the review in the info popup, ahead of the user's own
// text.
func (r *Review) Description(user string) string {
	d := fmt.Sprintf("**T3 Code**: %s\n\n%#q on %s", r.Target.Label(), r.Target.Cwd, r.Session.Label())
	if r.Target.Thread != nil {
		d += fmt.Sprintf("\n\nthread %#q", r.Target.Thread.ID)
	}
	if r.SourceLabel != "" {
		d += "\n\ndiff: " + r.SourceLabel
	}
	if user != "" {
		d += "\n\n" + user
	}
	return d
}

// thread is the thread the review shows, nil for a project's root.
func (r *Review) thread() *t3client.Thread {
	if r == nil {
		return nil
	}
	return r.Target.Thread
}

// follow sends p a FilesChangedMsg whenever the agent of the review's
// thread, which must be set, starts or finishes a run, until ctx ends or the
// connection drops.
func (r *Review) follow(ctx context.Context, p Program) {
	err := r.Session.Server().WatchThread(ctx, r.Target.Thread.ID, func(t3client.Thread) {
		go p.Send(splitview.FilesChangedMsg{}) // never block the connection's reader
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("[WARN] T3 live refresh stopped: %v", err)
	}
}
