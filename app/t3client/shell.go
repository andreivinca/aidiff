package t3client

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"
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
func (p Project) Archived() bool {
	return p.ArchivedAt != nil && *p.ArchivedAt != ""
}

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
func (t Thread) Running() bool {
	return t.ActiveRunID != nil
}

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

// SortedThreads returns the threads, most recently updated first.
// updatedAt is ISO-8601 UTC, so string order is time order.
func SortedThreads(sh Shell) []Thread {
	out := append([]Thread(nil), sh.Threads...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

// shellItem is one value of the orchestration.subscribeShell stream.
type shellItem struct {
	Kind     string  `json:"kind"`
	Snapshot *Shell  `json:"snapshot"`
	Thread   *Thread `json:"thread"`
}

// FetchShell reads the current project and thread listing: the first
// snapshot of the shell stream, after which the stream is closed.
func (c *Conn) FetchShell(ctx context.Context) (Shell, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	got := make(chan Shell, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.subscribe(ctx, "orchestration.subscribeShell", struct{}{}, func(v json.RawMessage) {
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
func (c *Conn) WatchThread(ctx context.Context, threadID string, onChange func(Thread)) error {
	w := runWatch{threadID: threadID, onChange: onChange}
	return c.subscribe(ctx, "orchestration.subscribeShell", struct{}{}, w.observe)
}

// runWatch follows one thread through the shell stream.
type runWatch struct {
	threadID string
	onChange func(Thread)
	last     *Thread
}

// observe takes one shell stream value.
func (w *runWatch) observe(v json.RawMessage) {
	var it shellItem
	if json.Unmarshal(v, &it) != nil {
		return
	}
	t := w.find(it)
	if t == nil {
		return
	}
	cur := *t
	if w.last != nil && runChanged(*w.last, cur) {
		w.onChange(cur)
	}
	w.last = &cur
}

// find is the watched thread's state in it, nil when it holds none.
func (w *runWatch) find(it shellItem) *Thread {
	switch it.Kind {
	case "snapshot":
		if it.Snapshot == nil {
			return nil
		}
		for i := range it.Snapshot.Threads {
			if it.Snapshot.Threads[i].ID == w.threadID {
				return &it.Snapshot.Threads[i]
			}
		}
	case "thread.updated":
		if it.Thread != nil && it.Thread.ID == w.threadID {
			return it.Thread
		}
	}
	return nil
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
