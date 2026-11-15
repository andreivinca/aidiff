package t3client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/umputun/revdiff/app/diff"
)

// Source kinds accepted by review.getDiffPreview.
const (
	SourceWorkingTree = "working-tree" // uncommitted changes
	SourceBranchRange = "branch-range" // the branch against its base, uncommitted included
)

// callTimeout bounds one request so a wedged server cannot hang a reload.
const callTimeout = 60 * time.Second

// Project is the part of a T3 project shell aidiff uses.
type Project struct {
	ID                    string          `json:"id"`
	Title                 string          `json:"title"`
	WorkspaceRoot         string          `json:"workspaceRoot"`
	DefaultModelSelection *ModelSelection `json:"defaultModelSelection"`
	ArchivedAt            *string         `json:"archivedAt"`
}

// Archived reports whether the project has left T3's project pickers.
func (p Project) Archived() bool { return p.ArchivedAt != nil && *p.ArchivedAt != "" }

// Thread is the part of a T3 thread shell aidiff uses.
type Thread struct {
	ModelSelection         *ModelSelection `json:"modelSelection"`
	InteractionMode        string          `json:"interactionMode"`
	RuntimeMode            string          `json:"runtimeMode"`
	ID                     string          `json:"id"`
	ProjectID              string          `json:"projectId"`
	Title                  string          `json:"title"`
	Branch                 *string         `json:"branch"`
	WorktreePath           *string         `json:"worktreePath"`
	ActiveRunID            *string         `json:"activeRunId"`
	ActiveProviderThreadID *string         `json:"activeProviderThreadId"`
	LatestRunCompletedAt   *string         `json:"latestRunCompletedAt"`
	UpdatedAt              string          `json:"updatedAt"`
}

// Running reports whether the thread's agent is mid-run.
func (t Thread) Running() bool { return t.ActiveRunID != nil }

// Shell is the server's project and thread listing.
type Shell struct {
	Projects []Project `json:"projects"`
	Threads  []Thread  `json:"threads"`
}

// Project returns the project with id, if listed.
func (s Shell) Project(id string) (Project, bool) {
	for _, p := range s.Projects {
		if p.ID == id {
			return p, true
		}
	}
	return Project{}, false
}

// shellItem is one value of the orchestration.subscribeShell stream.
type shellItem struct {
	Kind     string  `json:"kind"`
	Snapshot *Shell  `json:"snapshot"`
	Thread   *Thread `json:"thread"`
}

// FetchShell reads the current project and thread listing: the first
// snapshot of the shell stream, after which the stream is closed.
func FetchShell(ctx context.Context, c *Conn) (Shell, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	got := make(chan Shell, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Subscribe(ctx, "orchestration.subscribeShell", struct{}{}, func(v json.RawMessage) {
			var it shellItem
			if json.Unmarshal(v, &it) == nil && it.Kind == "snapshot" && it.Snapshot != nil {
				select {
				case got <- *it.Snapshot:
				default:
				}
			}
		})
	}()
	select {
	case s := <-got:
		return s, nil // the deferred cancel interrupts the stream
	case err := <-errCh:
		if err == nil {
			err = errors.New("shell stream ended without a snapshot")
		}
		return Shell{}, err
	}
}

// WatchThread calls onChange whenever the thread starts or finishes a run,
// until ctx ends or the connection drops. onChange runs on the connection's
// read goroutine and must not block.
func WatchThread(ctx context.Context, c *Conn, threadID string, onChange func(Thread)) error {
	var last *Thread
	return c.Subscribe(ctx, "orchestration.subscribeShell", struct{}{}, func(v json.RawMessage) {
		var it shellItem
		if json.Unmarshal(v, &it) != nil {
			return
		}
		var t *Thread
		switch it.Kind {
		case "snapshot":
			if it.Snapshot != nil {
				for i := range it.Snapshot.Threads {
					if it.Snapshot.Threads[i].ID == threadID {
						t = &it.Snapshot.Threads[i]
					}
				}
			}
		case "thread.updated":
			if it.Thread != nil && it.Thread.ID == threadID {
				t = it.Thread
			}
		}
		if t == nil {
			return
		}
		cur := *t
		if last != nil && runChanged(*last, cur) {
			onChange(cur)
		}
		last = &cur
	})
}

// runChanged reports a run starting or finishing between two thread states.
func runChanged(a, b Thread) bool {
	return a.Running() != b.Running() || deref(a.LatestRunCompletedAt) != deref(b.LatestRunCompletedAt)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Target is what a review looks at: a directory on the T3 host, and the
// thread it belongs to when there is one.
type Target struct {
	Cwd     string
	Thread  *Thread
	Project Project
}

// Label describes the target for the status line and errors.
func (t Target) Label() string {
	if t.Thread != nil {
		return fmt.Sprintf("%s / %s", t.Project.Title, t.Thread.Title)
	}
	return t.Project.Title
}

// Resolve picks the review target. With a thread id (or a unique prefix of
// one) it reviews that thread's worktree, or its project root when the thread
// has none. Without one it takes the most recently updated thread, and with
// no threads the only project.
func Resolve(sh Shell, threadID string) (Target, error) {
	if threadID != "" {
		t, err := findThread(sh, threadID)
		if err != nil {
			return Target{}, err
		}
		return targetFor(sh, t)
	}
	if len(sh.Threads) > 0 {
		threads := SortedThreads(sh)
		return targetFor(sh, &threads[0])
	}
	switch len(sh.Projects) {
	case 0:
		return Target{}, errors.New("T3 server has no projects")
	case 1:
		return Target{Cwd: sh.Projects[0].WorkspaceRoot, Project: sh.Projects[0]}, nil
	}
	return Target{}, errors.New("T3 server has several projects and no threads; pick a thread with --t3-thread")
}

// findThread matches a thread by id, or by a prefix that names one thread.
func findThread(sh Shell, id string) (*Thread, error) {
	var match *Thread
	for i := range sh.Threads {
		t := &sh.Threads[i]
		if t.ID == id {
			return t, nil
		}
		if strings.HasPrefix(t.ID, id) {
			if match != nil {
				return nil, fmt.Errorf("T3 thread prefix %q is ambiguous", id)
			}
			match = t
		}
	}
	if match == nil {
		return nil, fmt.Errorf("T3 thread %q not found (archived threads are not listed)", id)
	}
	return match, nil
}

func targetFor(sh Shell, t *Thread) (Target, error) {
	p, ok := sh.Project(t.ProjectID)
	if !ok {
		return Target{}, fmt.Errorf("T3 thread %s belongs to an unknown project", t.ID)
	}
	cwd := p.WorkspaceRoot
	if t.WorktreePath != nil && *t.WorktreePath != "" {
		cwd = *t.WorktreePath
	}
	th := *t
	return Target{Cwd: cwd, Thread: &th, Project: p}, nil
}

// SortedThreads returns the threads, most recently updated first.
// updatedAt is ISO-8601 UTC, so string order is time order.
func SortedThreads(sh Shell) []Thread {
	out := append([]Thread(nil), sh.Threads...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
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
	stats     map[string]diffFileStat               // the latest listing, by path
	patches   map[string]*diff.MultiFileStdinReader // the patches fetched since, by path; nil for an empty one
	whole     *diff.MultiFileStdinReader            // an older server's one patch
	truncated bool
}

// NewRenderer reviews cwd on the T3 host. source is SourceWorkingTree or
// SourceBranchRange.
func NewRenderer(c *Conn, cwd, source string) *Renderer {
	return &Renderer{conn: c, cwd: cwd, source: source}
}

// ChangedFiles fetches the listing and forgets the patches of the previous one.
func (r *Renderer) ChangedFiles(_ string, _ bool) ([]diff.FileEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var res diffPreview
	payload := map[string]any{"cwd": r.cwd, "sourceKind": r.source, "metadataOnly": true}
	if err := r.conn.Call(ctx, "review.getDiffPreview", payload, &res); err != nil {
		return nil, err
	}
	src := pickSource(res, r.source)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats = map[string]diffFileStat{}
	r.patches = map[string]*diff.MultiFileStdinReader{}
	r.whole, r.truncated = nil, false
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
	if strings.TrimSpace(src.Diff) == "" {
		return nil, nil
	}
	reader, err := diff.NewMultiFileStdinReader(strings.TrimRight(src.Diff, "\n"))
	if err != nil {
		return nil, fmt.Errorf("parse T3 diff: %w", err)
	}
	r.whole, r.truncated = reader, src.Truncated
	files, err := reader.ChangedFiles("", false)
	if err != nil {
		return nil, fmt.Errorf("list T3 diff files: %w", err)
	}
	return files, nil
}

// FileDiff returns one file's lines, fetching its patch the first time.
func (r *Renderer) FileDiff(req diff.FileDiffRequest) ([]diff.DiffLine, error) {
	r.mu.Lock()
	whole := r.whole
	reader, fetched := r.patches[req.Path]
	stat, listed := r.stats[req.Path]
	r.mu.Unlock()
	switch {
	case whole != nil:
		reader = whole
	case fetched:
	case !listed:
		return nil, nil
	default:
		var err error
		if reader, err = r.fetchPatch(stat); err != nil {
			return nil, err
		}
	}
	if reader == nil {
		return nil, nil
	}
	lines, err := reader.FileDiff(req)
	if err != nil {
		return nil, fmt.Errorf("T3 diff for %s: %w", req.Path, err)
	}
	return lines, nil
}

// fetchPatch asks the server for one file's patch and keeps it. The patch's
// final newline is dropped: the parser would read it as an empty line.
func (r *Renderer) fetchPatch(stat diffFileStat) (*diff.MultiFileStdinReader, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var res diffPreview
	payload := map[string]any{
		"cwd":        r.cwd,
		"sourceKind": r.source,
		"file":       map[string]any{"path": stat.Path, "previousPath": stat.PreviousPath, "sourceKind": r.source},
	}
	if err := r.conn.Call(ctx, "review.getDiffPreview", payload, &res); err != nil {
		return nil, fmt.Errorf("T3 diff for %s: %w", stat.Path, err)
	}
	src := pickSource(res, r.source)
	var reader *diff.MultiFileStdinReader
	if strings.TrimSpace(src.Diff) != "" {
		var err error
		if reader, err = diff.NewMultiFileStdinReader(strings.TrimRight(src.Diff, "\n")); err != nil {
			return nil, fmt.Errorf("parse T3 diff for %s: %w", stat.Path, err)
		}
	}
	r.mu.Lock()
	r.patches[stat.Path] = reader
	if src.Truncated {
		r.truncated = true
	}
	r.mu.Unlock()
	return reader, nil
}

// Truncated reports whether the server cut a fetched patch short.
func (r *Renderer) Truncated() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.truncated
}

// pickSource takes the requested source, falling back to the first one when
// the server did not return that kind.
func pickSource(res diffPreview, kind string) diffSource {
	for _, s := range res.Sources {
		if s.Kind == kind {
			return s
		}
	}
	if len(res.Sources) > 0 {
		return res.Sources[0]
	}
	return diffSource{}
}
