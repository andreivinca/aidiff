package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// localGit answers what the Diff View… command asks of the repository a local
// review shows: its branches, the current one's upstream, the default
// branch, and merge bases.
type localGit struct {
	dir string
}

// localRef is a branch of the repository.
type localRef struct {
	name    string // "main", or "origin/main" for a remote one
	remote  bool
	current bool
}

// run runs git in the repository and returns its trimmed output.
func (g localGit) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed git subcommands; refs come after the options
	cmd.Dir = g.dir
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

// refs lists the local and remote branches, leaving out a remote's HEAD.
func (g localGit) refs(ctx context.Context) ([]localRef, error) {
	out, err := g.run(ctx, "for-each-ref", "--format=%(HEAD)\t%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	var refs []localRef
	for line := range strings.SplitSeq(out, "\n") {
		head, full, ok := strings.Cut(line, "\t")
		if !ok || strings.HasSuffix(full, "/HEAD") {
			continue
		}
		r := localRef{current: head == "*"}
		switch {
		case strings.HasPrefix(full, "refs/heads/"):
			r.name = strings.TrimPrefix(full, "refs/heads/")
		case strings.HasPrefix(full, "refs/remotes/"):
			r.name, r.remote = strings.TrimPrefix(full, "refs/remotes/"), true
		default:
			continue
		}
		refs = append(refs, r)
	}
	return refs, nil
}

// upstream is the current branch's upstream ("origin/main"), "" when it has
// none.
func (g localGit) upstream(ctx context.Context) string {
	out, err := g.run(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return ""
	}
	return out
}

// defaultBranch is the branch the repository's remote calls its default,
// else the first of main and master that exists, "" when none does.
func (g localGit) defaultBranch(ctx context.Context) string {
	if out, err := g.run(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && out != "" {
		return out
	}
	for _, name := range []string{"main", "master", "origin/main", "origin/master"} {
		if _, err := g.run(ctx, "rev-parse", "--verify", "--quiet", name+"^{commit}"); err == nil {
			return name
		}
	}
	return ""
}

// mergeBase is the commit HEAD and ref last shared, which a diff against ref
// starts from, so it shows only what this branch changed.
func (g localGit) mergeBase(ctx context.Context, ref string) (string, error) {
	out, err := g.run(ctx, "merge-base", "--", ref, "HEAD")
	if err != nil {
		return "", fmt.Errorf("no common commit with %s: %w", ref, err)
	}
	return out, nil
}
