package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// connectTimeout bounds reaching a T3 server, SSH login and tunnel included.
const connectTimeout = 45 * time.Second

// t3Mode reports whether this run reviews a T3 Code server.
func (o options) t3Mode() bool {
	return o.T3 != "" || o.T3Pair != "" || o.T3Thread != "" || o.T3Threads
}

// t3SourceKind maps --t3-source to the server's diff source kind.
func (o options) t3SourceKind() string {
	if o.T3Source == "uncommitted" {
		return t3client.SourceWorkingTree
	}
	return t3client.SourceBranchRange
}

// validateT3Flags rejects flags that pick a local diff alongside T3 mode.
func validateT3Flags(opts options) error {
	if !opts.t3Mode() {
		return nil
	}
	conflicts := []struct {
		bad  bool
		flag string
	}{
		{opts.Refs.Base != "" || opts.Refs.Against != "", "refs"},
		{opts.Staged, "--staged"},
		{len(opts.Only) > 0, "--only"},
		{opts.AllFiles, "--all-files"},
		{opts.Stdin, "--stdin"},
		{opts.CompareOld != "" || opts.CompareNew != "", "--compare-old/--compare-new"},
		{len(opts.Include) > 0, "--include"},
		{len(opts.Exclude) > 0, "--exclude"},
		{opts.Annotations != "", "--annotations"},
	}
	for _, c := range conflicts {
		if c.bad {
			return fmt.Errorf("--t3 cannot be used with %s", c.flag)
		}
	}
	return nil
}

// t3Review is a connected T3 session and the target it reviews.
type t3Review struct {
	session  *t3client.Session
	target   t3client.Target
	renderer *t3client.Renderer
}

// t3TokenStore keeps T3 tokens beside the config file.
func t3TokenStore() t3client.TokenStore {
	dir := filepath.Dir(defaultConfigPath())
	return t3client.TokenStore{Path: filepath.Join(dir, "t3-tokens.json")}
}

// connectT3 reaches the server named by the T3 flags.
func connectT3(opts options) (*t3client.Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	s, err := t3client.Connect(ctx, opts.T3, t3client.ConnectOptions{
		Store: t3TokenStore(),
		Token: opts.T3Token,
		Pair:  opts.T3Pair,
	})
	if err != nil {
		return nil, fmt.Errorf("t3: %w", err)
	}
	return s, nil
}

// fetchT3Shell reads the connected server's projects and threads.
func fetchT3Shell(s *t3client.Session) (t3client.Shell, error) {
	sh, err := t3client.FetchShell(context.Background(), s.Conn)
	if err != nil {
		return t3client.Shell{}, fmt.Errorf("t3: list threads: %w", err)
	}
	return sh, nil
}

// setupT3Review connects and resolves which thread to review.
func setupT3Review(opts options) (*t3Review, error) {
	s, err := connectT3(opts)
	if err != nil {
		return nil, err
	}
	sh, err := fetchT3Shell(s)
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	target, err := t3client.Resolve(sh, opts.T3Thread)
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("t3: %w", err)
	}
	return &t3Review{
		session:  s,
		target:   target,
		renderer: t3client.NewRenderer(s.Conn, target.Cwd, opts.t3SourceKind()),
	}, nil
}

// description names the T3 thread in the info popup, ahead of any user text.
func (r *t3Review) description(user string) string {
	d := fmt.Sprintf("**T3 Code**: %s\n\n%#q on %s", r.target.Label(), r.target.Cwd, r.session.Label)
	if r.target.Thread != nil {
		d += fmt.Sprintf("\n\nthread %#q", r.target.Thread.ID)
	}
	if user != "" {
		d += "\n\n" + user
	}
	return d
}

// watch refreshes the review whenever the thread's agent starts or finishes
// a run. It returns a stop function.
func (r *t3Review) watch(p *tea.Program) (stop func()) {
	if r.target.Thread == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		err := t3client.WatchThread(ctx, r.session.Conn, r.target.Thread.ID, func(t3client.Thread) {
			go p.Send(splitview.FilesChangedMsg{}) // never block the connection's reader
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("[WARN] T3 live refresh stopped: %v", err)
		}
	}()
	return cancel
}

// listT3Threads prints the server's threads, most recently updated first.
func listT3Threads(opts options, w io.Writer) error {
	s, err := connectT3(opts)
	if err != nil {
		return err
	}
	defer s.Close()
	sh, err := fetchT3Shell(s)
	if err != nil {
		return err
	}
	threads := t3client.SortedThreads(sh)
	if len(threads) == 0 {
		if _, err := fmt.Fprintln(w, "no threads"); err != nil {
			return fmt.Errorf("write threads: %w", err)
		}
		return nil
	}
	for _, t := range threads {
		project := t.ProjectID
		if p, ok := sh.Project(t.ProjectID); ok {
			project = p.Title
		}
		state := "idle"
		if t.Running() {
			state = "running"
		}
		branch := "-"
		if t.Branch != nil {
			branch = *t.Branch
		}
		if _, err := fmt.Fprintf(w, "%s  %-7s  %-20s  %-24s  %s\n", t.ID, state, trunc(project, 20), trunc(branch, 24), t.Title); err != nil {
			return fmt.Errorf("write threads: %w", err)
		}
	}
	return nil
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
