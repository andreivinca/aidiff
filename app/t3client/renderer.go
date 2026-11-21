package t3client

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/umputun/revdiff/app/diff"
)

// Source kinds accepted by review.getDiffPreview.
const (
	SourceWorkingTree = "working-tree" // uncommitted changes
	SourceBranchRange = "branch-range" // the branch against its base, uncommitted included
)

// DiffSource is what a review compares: a source kind and, for a branch
// range, the ref it is compared against ("" lets the server pick the
// default branch).
type DiffSource struct {
	Kind    string
	BaseRef string
}

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
	cutIn  string // the file the server cut the patch short in (or after); "" when it is whole
}

// cutShortNotice ends the lines of the file a patch was cut short in. As a
// divider it cannot be taken for a line of the file: the cursor skips it, it
// takes no annotation, and no change block, which a revert works on, spans
// it. IsPlaceholder keeps a reviewed mark from outliving a reload, since
// what was cut off went unseen.
var cutShortNotice = diff.DiffLine{
	ChangeType: diff.ChangeDivider, Content: "⋯ the T3 server cut this diff short here ⋯", IsPlaceholder: true,
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
// A server that lists no files gets the old treatment: its one patch. A
// patch the server still cuts short ends with cutShortNotice.
type Renderer struct {
	conn    *Conn
	cwd     string
	source  string
	baseRef string // the branch range's base; "" for the server's default

	mu       sync.Mutex
	stats    map[string]diffFileStat   // the latest listing, by path
	patches  map[string]*patchFetch    // the patches of that listing, fetched or in flight, by path
	contents map[string]*contentsFetch // the new contents of that listing's files, by path (expand.go)
	whole    patch                     // an older server's one patch
}

// NewRenderer reviews cwd on the T3 host. source is SourceWorkingTree or
// SourceBranchRange.
func NewRenderer(c *Conn, cwd, source string) *Renderer {
	return NewSourceRenderer(c, cwd, DiffSource{Kind: source})
}

// NewSourceRenderer reviews cwd on the T3 host as src says.
func NewSourceRenderer(c *Conn, cwd string, src DiffSource) *Renderer {
	return &Renderer{conn: c, cwd: cwd, source: src.Kind, baseRef: src.BaseRef}
}

// withBase adds the branch range's base to a getDiffPreview payload.
func (r *Renderer) withBase(payload map[string]any) map[string]any {
	if r.baseRef != "" {
		payload["baseRef"] = r.baseRef
	}
	return payload
}

// ChangedFiles fetches the listing and forgets the patches of the previous
// one. A fetch still running for the previous listing finishes into its own
// patchFetch, which the new listing no longer holds.
func (r *Renderer) ChangedFiles(_ string, _ bool) ([]diff.FileEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var res diffPreview
	payload := r.withBase(map[string]any{"cwd": r.cwd, "sourceKind": r.source, "metadataOnly": true})
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
	r.contents = map[string]*contentsFetch{}
	r.whole = patch{}
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
// A patch cut short is cut in its last file, the files after it missing.
func (r *Renderer) fromPatchLocked(src diffSource) ([]diff.FileEntry, error) {
	whole, err := parsePatch(src.Diff)
	if err != nil {
		return nil, fmt.Errorf("parse T3 diff: %w", err)
	}
	files, err := whole.changedFiles()
	if err != nil {
		return nil, err
	}
	if src.Truncated && len(files) > 0 {
		whole.cutIn = files[len(files)-1].Path
	}
	r.whole = whole
	return files, nil
}

// FileDiff returns one file's lines, fetching its patch the first time. The
// file a patch was cut short in ends with cutShortNotice, and gets no more
// context: laid over the file's contents, the cut patch would pass the
// changes it is missing off as unchanged lines.
func (r *Renderer) FileDiff(req diff.FileDiffRequest) ([]diff.DiffLine, error) {
	p, err := r.patchOf(req.Path)
	if err != nil {
		return nil, err
	}
	lines, err := p.fileDiff(req)
	if err != nil {
		return nil, err
	}
	if p.cutIn != "" && p.cutIn == req.Path {
		return append(slices.Clip(lines), cutShortNotice), nil // the patch's own lines stay as they are
	}
	return r.withContext(req, lines), nil
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

	pf.patch, pf.err = r.fetchPatch(stat)
	r.mu.Lock()
	if pf.err != nil && r.patches[path] == pf { // not replaced by a new listing meanwhile
		delete(r.patches, path) // the next view asks again
	}
	r.mu.Unlock()
	close(pf.done)
	return pf.patch, pf.err
}

// fetchPatch asks the server for one file's patch.
func (r *Renderer) fetchPatch(stat diffFileStat) (p patch, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var res diffPreview
	payload := map[string]any{
		"cwd":        r.cwd,
		"sourceKind": r.source,
		"file":       map[string]any{"path": stat.Path, "previousPath": stat.PreviousPath, "sourceKind": r.source},
	}
	if err = r.conn.call(ctx, "review.getDiffPreview", r.withBase(payload), &res); err != nil {
		return patch{}, fmt.Errorf("T3 diff for %s: %w", stat.Path, err)
	}
	src, err := res.source(r.source)
	if err != nil {
		return patch{}, fmt.Errorf("T3 diff for %s: %w", stat.Path, err)
	}
	if p, err = parsePatch(src.Diff); err != nil {
		return patch{}, fmt.Errorf("parse T3 diff for %s: %w", stat.Path, err)
	}
	if src.Truncated {
		p.cutIn = stat.Path
	}
	return p, nil
}
