package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/umputun/revdiff/app/remotegit"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/t3palette"
)

// remoteFolderSource reviews a git folder on an SSH machine (a palette
// folder spelled t3client.RemoteDir): git runs there, over the machine's
// login when it has one. Like a T3 review it has no local files: no
// revert, no live refresh, no Diff View… choices.
func remoteFolderSource(d t3client.RemoteDir) sourceSetup {
	return sourceSetup{vcsSetup: vcsSetup{renderer: remotegit.New(d.Host, d.Path)}, machine: &d}
}

// remotePoll is how often a machine's folder on screen is checked for
// changes: its files cannot be watched from here, and each check is a
// command run there.
const remotePoll = 5 * time.Second

// remoteWatch is the live refresh of a machine's folder: polled while on
// screen, unless --no-watch.
func remoteWatch(opts options, src sourceSetup) func(p t3palette.Program) func() {
	r, ok := src.renderer.(*remotegit.Renderer)
	if opts.NoWatch || !ok {
		return nil
	}
	return func(p t3palette.Program) func() {
		return r.Watch(remotePoll, func() { p.Send(splitview.FilesChangedMsg{}) })
	}
}

// remoteBranch is the current branch of the machine's repository: "" on a
// detached HEAD.
func remoteBranch(d t3client.RemoteDir) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := t3client.RunOn(ctx, d.Host, d.Path, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	if _, ok := errors.AsType[*t3client.RunError](err); ok {
		return "", nil // detached
	}
	return "", fmt.Errorf("read the branch on %s: %w", d.Host, err)
}
