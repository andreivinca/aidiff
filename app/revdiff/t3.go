package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"

	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/t3palette"
)

// t3Mode reports whether this run reviews a T3 Code server.
func (o options) t3Mode() bool {
	return o.T3 != "" || o.T3Pair != "" || o.T3Thread != "" || o.T3Threads
}

// t3SourceKind maps --t3-source to the server's diff source kind.
func (o options) t3SourceKind() string {
	if o.T3Source == "branch" {
		return t3client.SourceBranchRange
	}
	return t3client.SourceWorkingTree
}

// t3ConnectOptions is how the T3 flags authenticate.
func (o options) t3ConnectOptions() t3client.ConnectOptions {
	return t3client.ConnectOptions{Store: t3TokenStore(), Token: o.T3Token, Pair: o.T3Pair}
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

// t3TokenStore keeps T3 tokens beside the config file.
func t3TokenStore() t3client.TokenStore {
	dir := filepath.Dir(defaultConfigPath())
	return t3client.TokenStore{Path: filepath.Join(dir, "t3-tokens.json")}
}

// setupT3Review connects to the server the T3 flags name and resolves the
// thread to review.
func setupT3Review(opts options) (*t3palette.Review, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t3palette.ConnectTimeout)
	defer cancel()
	r, err := t3palette.Open(ctx, opts.T3, opts.t3ConnectOptions(), opts.T3Thread)
	if err != nil {
		return nil, fmt.Errorf("t3: %w", err)
	}
	logT3Notices(r.Session)
	return r, nil
}

// t3Source is where a T3 review's diff comes from: the server, which reads
// it in the target's folder.
func t3Source(opts options, r *t3palette.Review) sourceSetup {
	src := r.Source
	if src.Kind == t3client.SourceFiles {
		folder := t3client.NewFolderReader(r.Session.Conn(), r.Target.Cwd)
		return sourceSetup{vcsSetup: vcsSetup{renderer: folder}, remote: folder, t3: r}
	}
	if src.Kind == "" {
		src = t3client.DiffSource{Kind: opts.t3SourceKind()}
	}
	renderer := t3client.NewSourceRenderer(r.Session.Conn(), r.Target.Cwd, src)
	return sourceSetup{vcsSetup: vcsSetup{renderer: renderer}, t3: r}
}

// t3HistoryPath files a T3 review's annotations by its server and folder:
// history keeps them under the folder's name.
func t3HistoryPath(r *t3palette.Review) string {
	return r.Session.Label() + ":" + r.Target.Cwd
}

// listT3Threads prints the server's threads, most recently updated first.
func listT3Threads(opts options, w io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), t3palette.ConnectTimeout)
	defer cancel()
	s, err := t3palette.Connect(ctx, opts.T3, opts.t3ConnectOptions())
	if err != nil {
		return fmt.Errorf("t3: %w", err)
	}
	defer s.Close()
	logT3Notices(s)
	sh, err := s.Server().FetchShell(ctx)
	if err != nil {
		return fmt.Errorf("t3: list threads: %w", err)
	}
	if err := t3palette.WriteThreads(w, sh); err != nil {
		return fmt.Errorf("t3: %w", err)
	}
	return nil
}

// logT3Notices reports, before the full-screen UI starts, what the user
// should know about how s was reached.
func logT3Notices(s t3palette.Session) {
	for _, n := range s.Notices() {
		log.Printf("[WARN] T3: %s", n)
	}
}
