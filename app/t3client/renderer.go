package t3client

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/umputun/revdiff/app/diff"
)

// Source kinds accepted by review.getDiffPreview.
const (
	SourceWorkingTree = "working-tree" // uncommitted changes
	SourceBranchRange = "branch-range" // the branch against its base, uncommitted included
)

// diffPreview is the part of review.getDiffPreview's result aidiff reads.
type diffPreview struct {
	Sources []diffSource `json:"sources"`
}

type diffSource struct {
	Kind      string         `json:"kind"`
	Diff      string         `json:"diff"`
	Truncated bool           `json:"truncated"`
	Files     []diffFileStat `json:"files"`
}

// source is the preview's source of kind. A preview without it is an error,
// never a stand-in from another kind: a working-tree review must not show a
// branch's changes under its name.
func (p diffPreview) source(kind string) (diffSource, error) {
	offered := make([]string, 0, len(p.Sources))
	for _, s := range p.Sources {
		if s.Kind == kind {
			return s, nil
		}
		offered = append(offered, s.Kind)
	}
	if len(offered) == 0 {
		return diffSource{}, fmt.Errorf("the T3 server returned no %s diff", kind)
	}
	return diffSource{}, fmt.Errorf("the T3 server returned no %s diff (only %s)", kind, strings.Join(offered, ", "))
}

// diffFileStat is one changed file of a preview's listing.
type diffFileStat struct {
	Path         string  `json:"path"`
	PreviousPath *string `json:"previousPath"`
	ChangeType   string  `json:"changeType"` // change, rename-pure, rename-changed, new, deleted
}

// entry maps the server's change type to aidiff's file status.
func (f diffFileStat) entry() diff.FileEntry {
	e := diff.FileEntry{Path: f.Path, Status: diff.FileModified}
	switch f.ChangeType {
	case "new":
		e.Status = diff.FileAdded
	case "deleted":
		e.Status = diff.FileDeleted
	case "rename-pure", "rename-changed":
		e.Status = diff.FileRenamed
		if f.PreviousPath != nil {
			e.OldPath = *f.PreviousPath
		}
	}
	return e
}

// patch is one parsed unified diff; the zero patch is an empty one.
type patch struct {
	reader *diff.MultiFileStdinReader
}

// parsePatch reads a unified diff. The patch's final newline is dropped: the
// parser would read it as an empty line.
func parsePatch(text string) (patch, error) {
	if strings.TrimSpace(text) == "" {
		return patch{}, nil
	}
	reader, err := diff.NewMultiFileStdinReader(strings.TrimRight(text, "\n"))
	if err != nil {
		return patch{}, fmt.Errorf("read unified diff: %w", err)
	}
	return patch{reader: reader}, nil
}

// empty reports whether the patch changes nothing.
func (p patch) empty() bool {
	return p.reader == nil
}

// changedFiles lists the files the patch changes.
func (p patch) changedFiles() ([]diff.FileEntry, error) {
	if p.empty() {
		return nil, nil
	}
	files, err := p.reader.ChangedFiles("", false)
	if err != nil {
		return nil, fmt.Errorf("list T3 diff files: %w", err)
	}
	return files, nil
}

// fileDiff is one file's lines of the patch, none when it does not touch it.
func (p patch) fileDiff(req diff.FileDiffRequest) ([]diff.DiffLine, error) {
	if p.empty() {
		return nil, nil
	}
	lines, err := p.reader.FileDiff(req)
	if err != nil {
		return nil, fmt.Errorf("T3 diff for %s: %w", req.Path, err)
	}
	return lines, nil
}

// patchFetch is one file's patch, fetched once however many views ask for
// it at a time: done is closed when patch and err are set.
type patchFetch struct {
	done  chan struct{}
	patch patch
	err   error
}

// Renderer serves a T3 diff preview through aidiff's renderer contract.
// ChangedFiles lists every changed file from a metadata-only preview, which
// the server never cuts short the way it caps a patch; FileDiff then fetches
// one file's patch when the file is opened, so a large change is complete.
// A server that lists no files gets the old treatment: its one patch.
type Renderer struct {
	conn   *Conn
	cwd    string
	source string

	mu        sync.Mutex
	stats     map[string]diffFileStat // the latest listing, by path
	patches   map[string]*patchFetch  // the patches of that listing, fetched or in flight, by path
	whole     patch                   // an older server's one patch
	truncated bool
}

// NewRenderer reviews cwd on the T3 host. source is SourceWorkingTree or
// SourceBranchRange.
func NewRenderer(c *Conn, cwd, source string) *Renderer {
	return &Renderer{conn: c, cwd: cwd, source: source}
}

// ChangedFiles fetches the listing and forgets the patches of the previous
// one. A fetch still running for the previous listing finishes into its own
// patchFetch, which the new listing no longer holds.
func (r *Renderer) ChangedFiles(_ string, _ bool) ([]diff.FileEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var res diffPreview
	payload := map[string]any{"cwd": r.cwd, "sourceKind": r.source, "metadataOnly": true}
	if err := r.conn.call(ctx, "review.getDiffPreview", payload, &res); err != nil {
		return nil, err
	}
	src, err := res.source(r.source)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats = map[string]diffFileStat{}
	r.patches = map[string]*patchFetch{}
	r.whole, r.truncated = patch{}, false
	if len(src.Files) == 0 {
		return r.fromPatchLocked(src)
	}
	entries := make([]diff.FileEntry, 0, len(src.Files))
	for _, f := range src.Files {
		r.stats[f.Path] = f
		entries = append(entries, f.entry())
	}
	return entries, nil
}

// fromPatchLocked lists the files of a server's one patch; mu must be held.
func (r *Renderer) fromPatchLocked(src diffSource) ([]diff.FileEntry, error) {
	whole, err := parsePatch(src.Diff)
	if err != nil {
		return nil, fmt.Errorf("parse T3 diff: %w", err)
	}
	r.whole, r.truncated = whole, src.Truncated && !whole.empty()
	return whole.changedFiles()
}

// FileDiff returns one file's lines, fetching its patch the first time.
func (r *Renderer) FileDiff(req diff.FileDiffRequest) ([]diff.DiffLine, error) {
	p, err := r.patchOf(req.Path)
	if err != nil {
		return nil, err
	}
	return p.fileDiff(req)
}

// patchOf is the patch holding path's changes: the server's one patch, or
// the file's own, fetched now or joined while another view fetches it. A
// file that is not listed has an empty one.
func (r *Renderer) patchOf(path string) (patch, error) {
	r.mu.Lock()
	if !r.whole.empty() {
		whole := r.whole
		r.mu.Unlock()
		return whole, nil
	}
	if pf, ok := r.patches[path]; ok {
		r.mu.Unlock()
		<-pf.done
		return pf.patch, pf.err
	}
	stat, listed := r.stats[path]
	if !listed {
		r.mu.Unlock()
		return patch{}, nil
	}
	pf := &patchFetch{done: make(chan struct{})}
	r.patches[path] = pf
	r.mu.Unlock()

	var truncated bool
	pf.patch, truncated, pf.err = r.fetchPatch(stat)
	r.mu.Lock()
	switch {
	case r.patches[path] != pf: // the listing was replaced meanwhile; the result is not its
	case pf.err != nil:
		delete(r.patches, path) // the next view asks again
	case truncated:
		r.truncated = true
	}
	r.mu.Unlock()
	close(pf.done)
	return pf.patch, pf.err
}

// fetchPatch asks the server for one file's patch.
func (r *Renderer) fetchPatch(stat diffFileStat) (p patch, truncated bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var res diffPreview
	payload := map[string]any{
		"cwd":        r.cwd,
		"sourceKind": r.source,
		"file":       map[string]any{"path": stat.Path, "previousPath": stat.PreviousPath, "sourceKind": r.source},
	}
	if err = r.conn.call(ctx, "review.getDiffPreview", payload, &res); err != nil {
		return patch{}, false, fmt.Errorf("T3 diff for %s: %w", stat.Path, err)
	}
	src, err := res.source(r.source)
	if err != nil {
		return patch{}, false, fmt.Errorf("T3 diff for %s: %w", stat.Path, err)
	}
	if p, err = parsePatch(src.Diff); err != nil {
		return patch{}, false, fmt.Errorf("parse T3 diff for %s: %w", stat.Path, err)
	}
	return p, src.Truncated, nil
}

// cutShort reports whether the server cut a fetched patch short.
func (r *Renderer) cutShort() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.truncated
}
