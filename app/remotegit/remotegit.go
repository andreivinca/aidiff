// Package remotegit reviews a git folder on an SSH machine: the files that
// differ from HEAD, staged, unstaged and untracked alike, as a local
// review's default shows them, with git running on the machine
// (t3client.RunOn). It serves aidiff's renderer contract, so the review is
// the one a local folder gets.
package remotegit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/t3client"
)

// timeout bounds one git command on the machine.
const timeout = 60 * time.Second

// fullContext is the context that shows a whole file, as a local review's
// full-file view asks git for.
const fullContext = 1000000

// emptyTree is git's empty tree, what a repository without a commit yet is
// compared against.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// gitOptions start every git command: paths taken literally and printed
// unquoted, no colors, no pager.
var gitOptions = []string{"--literal-pathspecs", "--no-pager", "-c", "core.quotepath=false", "-c", "color.ui=false"}

// run runs git on a machine; a variable for the tests.
var run = func(ctx context.Context, host, dir string, args ...string) ([]byte, error) {
	return t3client.RunOn(ctx, host, dir, "git", append(append([]string{}, gitOptions...), args...)...)
}

// Renderer reviews the git folder Root on Host.
type Renderer struct {
	Host string
	Root string // the repository's top folder there, as git spells it

	mu        sync.Mutex
	untracked map[string]bool // the untracked files of the latest listing
	base      string          // HEAD, or the empty tree before the first commit
}

// New reviews the repository at root on host.
func New(host, root string) *Renderer {
	return &Renderer{Host: host, Root: root, untracked: map[string]bool{}}
}

// Resolve finds the repository dir is in on host and returns its top
// folder, as git spells it there (C:/… on Windows).
func Resolve(ctx context.Context, host, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := run(ctx, host, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if re, ok := errors.AsType[*t3client.RunError](err); ok {
			return "", fmt.Errorf("%s on %s is not a git repository: %w", dir, host, re)
		}
		return "", err
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", fmt.Errorf("%s on %s: git names no repository", dir, host)
	}
	return root, nil
}

// ChangedFiles lists the files that differ from HEAD: git status, every
// untracked file included.
func (r *Renderer) ChangedFiles(_ string, _ bool) ([]diff.FileEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := run(ctx, r.Host, r.Root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("git status on %s: %w", r.Host, err)
	}
	base := "HEAD"
	if _, herr := run(ctx, r.Host, r.Root, "rev-parse", "--verify", "-q", "HEAD"); herr != nil {
		if _, ok := errors.AsType[*t3client.RunError](herr); !ok {
			return nil, fmt.Errorf("git on %s: %w", r.Host, herr)
		}
		base = emptyTree
	}
	files, untracked := parseStatus(out)
	r.mu.Lock()
	r.untracked, r.base = untracked, base
	r.mu.Unlock()
	return files, nil
}

// parseStatus reads `git status --porcelain=v1 -z`: an entry is "XY path",
// a rename's or copy's followed by its origin.
func parseStatus(out []byte) (files []diff.FileEntry, untracked map[string]bool) {
	untracked = map[string]bool{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		x, y, path := f[0], f[1], f[3:]
		e := diff.FileEntry{Path: path, Status: diff.FileModified}
		switch {
		case x == '?' && y == '?':
			e.Status = diff.FileAdded
			untracked[path] = true
		case x == '!':
			continue // ignored
		case x == 'R' || x == 'C':
			if i+1 < len(fields) {
				i++
				e.OldPath = fields[i]
			}
			e.Status = diff.FileRenamed
			if x == 'C' {
				e.Status, e.OldPath = diff.FileAdded, ""
			}
		case x == 'A' && y == 'D':
			continue // added, then deleted again: nothing against HEAD
		case x == 'A':
			e.Status = diff.FileAdded
		case x == 'D' || y == 'D':
			e.Status = diff.FileDeleted
		}
		files = append(files, e)
	}
	return files, untracked
}

// FileDiff is one file's lines against HEAD, every line of it with the full
// context asked for; an untracked file shows whole, added.
func (r *Renderer) FileDiff(req diff.FileDiffRequest) ([]diff.DiffLine, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	r.mu.Lock()
	untracked, base := r.untracked[req.Path], r.base
	r.mu.Unlock()
	if base == "" {
		base = "HEAD"
	}
	n := req.ContextLines
	if n <= 0 || n > fullContext {
		n = fullContext
	}
	var args []string
	switch {
	case untracked:
		args = []string{"diff", "--no-index", fmt.Sprintf("-U%d", n), "--", "/dev/null", req.Path}
	case req.OldPath != "" && req.OldPath != req.Path:
		args = []string{"diff", "-M", fmt.Sprintf("-U%d", n), base, "--", req.OldPath, req.Path}
	default:
		args = []string{"diff", fmt.Sprintf("-U%d", n), base, "--", req.Path}
	}
	out, err := run(ctx, r.Host, r.Root, args...)
	if re, ok := errors.AsType[*t3client.RunError](err); ok && untracked && re.Code == 1 {
		err = nil // --no-index exits 1 when the files differ, as they do
	}
	if err != nil {
		return nil, fmt.Errorf("git diff %s on %s: %w", req.Path, r.Host, err)
	}
	// a file of CRLF lines, as a Windows checkout holds, reads as any other
	text := strings.TrimRight(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	if !strings.Contains(text, "\n@@ ") && req.OldPath != "" && req.OldPath != req.Path && !untracked {
		return r.unchanged(ctx, base, req.OldPath) // a pure rename: git shows no lines
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	reader, err := diff.NewMultiFileStdinReader(text)
	if err != nil {
		return nil, fmt.Errorf("read the diff of %s: %w", req.Path, err)
	}
	lines, err := reader.FileDiff(diff.FileDiffRequest{Path: req.Path, OldPath: req.OldPath})
	if err != nil {
		return nil, fmt.Errorf("the diff of %s: %w", req.Path, err)
	}
	return lines, nil
}

// unchanged is the lines of path at base, every one unchanged: a file
// renamed and nothing else.
func (r *Renderer) unchanged(ctx context.Context, base, path string) ([]diff.DiffLine, error) {
	out, err := run(ctx, r.Host, r.Root, "show", base+":"+path)
	if err != nil {
		return nil, fmt.Errorf("git show %s on %s: %w", path, r.Host, err)
	}
	text := strings.TrimSuffix(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	if text == "" {
		return nil, nil
	}
	parts := strings.Split(text, "\n")
	lines := make([]diff.DiffLine, len(parts))
	for i, l := range parts {
		lines[i] = diff.DiffLine{OldNum: i + 1, NewNum: i + 1, Content: l, ChangeType: diff.ChangeContext}
	}
	return lines, nil
}

// fingerprint sums what the review shows: the files git status lists and
// the changed lines of the tracked ones. An edit to a file that is already
// untracked does not change it; the next listing change, or R, shows it.
func (r *Renderer) fingerprint(ctx context.Context) (string, error) {
	status, err := run(ctx, r.Host, r.Root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	changes, err := run(ctx, r.Host, r.Root, "diff", "HEAD", "--no-ext-diff", "-U0")
	if _, ok := errors.AsType[*t3client.RunError](err); ok {
		changes, err = nil, nil // no commit yet: the listing alone
	}
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	sum.Write(status)
	sum.Write([]byte{0})
	sum.Write(changes)
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// Watch checks the folder every interval and calls changed when what the
// review shows changed since the check before, until stop is called: the
// machine's files cannot be watched from here. A check that fails is
// skipped.
func (r *Renderer) Watch(interval time.Duration, changed func()) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		last := ""
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			cctx, ccancel := context.WithTimeout(ctx, timeout)
			fp, err := r.fingerprint(cctx)
			ccancel()
			if err == nil {
				if last != "" && fp != last {
					changed()
				}
				last = fp
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
