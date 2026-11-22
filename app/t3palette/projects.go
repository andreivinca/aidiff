package t3palette

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// Switching projects. Each project of a server has a split workspace of its
// own: its review, its chat and terminals, their sizes. Going back to a
// project brings its workspace back as it was left, typed chat input
// included, so the user can work on several projects at once. Each review
// also keeps its annotation store for the run, so a thread opened again
// within its project brings its annotations back.

// projectKey names a project of a server across reconnects, which make new
// sessions to the same server.
func projectKey(s Session, projectID string) string {
	return t3Key(s.Label(), projectID)
}

// t3Key is the workspace of project projectID of server label, as a
// session to it names it; a restored tab names it before connecting.
func t3Key(label, projectID string) string {
	return label + "\x00" + projectID
}

// wsKey is the split workspace of r: one per project of a server.
func wsKey(r *Review) string {
	return projectKey(r.Session, r.Target.Project.ID)
}

// Workspace is the split workspace the run starts in: the project of the
// review it opens with, "" (unnamed, closed once left) for a local review.
func (t *Palette) Workspace() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ws
}

// reviewKey names what a review shows: a thread, or a project's root.
func reviewKey(r *Review) string {
	key := r.Session.Label() + "\x00" + r.Target.Cwd
	if th := r.thread(); th != nil {
		key += "\x00" + th.ID
	}
	return key
}

// rememberLocked records store as the one holding r's annotations. mu must
// be held.
func (t *Palette) rememberLocked(r *Review, store *annotation.Store) {
	if store != nil {
		t.stores[reviewKey(r)] = store
	}
}

// storeFor is the store of r's annotations: the one it had when last on
// screen, else a new one.
func (t *Palette) storeFor(r *Review) *annotation.Store {
	t.mu.Lock()
	defer t.mu.Unlock()
	if store, ok := t.stores[reviewKey(r)]; ok {
		return store
	}
	return annotation.NewStore()
}

// projects are the live server's projects still in use, by title: the
// rows of the projects sidebar, numbered from 1 in this order.
func projects(sh t3client.Shell) []t3client.Project {
	items := projectItems(sh)
	out := make([]t3client.Project, 0, len(items))
	for _, it := range items {
		p, _ := it.Value.(t3client.Project)
		out = append(out, p)
	}
	return out
}

// currentProjectID is the project of the review on screen when it is one of
// the live server's, else the selected project's, else "".
func (v state) currentProjectID() string {
	if v.onScreen != nil && v.live != nil && v.onScreen.Session.Label() == v.live.Label() {
		return v.onScreen.Target.Project.ID
	}
	if v.project != nil {
		return v.project.ID
	}
	return ""
}

// openProject puts project p of the live server on screen: its workspace as
// it was left, when it has one, its review rebuilt when a reconnect replaced
// the session it read over; else a new one with its most recently updated
// thread, else its root. A project already on screen stays as it is.
func (t *Palette) openProject(p t3client.Project) tea.Cmd {
	v := t.state()
	s := v.live
	if s == nil {
		return nil
	}
	t.selectProject(s, p)
	key := projectKey(s, p.ID)
	t.mu.Lock()
	onScreen := t.ws == key
	left, parked := t.parked[key]
	stale := parked && t.stale[left]
	t.mu.Unlock()
	if onScreen {
		return nil
	}
	if stale {
		return t.rebuildParked(left)
	}
	if parked {
		return func() tea.Msg {
			return splitview.SwitchWorkspaceMsg{Key: key, Done: func(sw splitview.WorkspaceSwitch) {
				t.switched(key, nil, nil, sw)
			}}
		}
	}
	return asking("opening "+p.Title, func(ctx context.Context) tea.Msg {
		sh, err := t.fetchShell(ctx, s)
		if err != nil {
			return t3Error("open "+p.Title, err)
		}
		if listed, ok := sh.Project(p.ID); ok {
			p = listed
		}
		threads := projectThreads(sh, &p)
		if len(threads) == 0 {
			r := t.newReview(s, t3client.Target{Cwd: p.WorkspaceRoot, Project: p})
			return t.workspaceMsg(r, t.storeFor(r), "open "+p.Title, false)
		}
		return t.openMsg(s, sh, threads[0].ID) // a workspace that appeared meanwhile takes the thread
	})
}
