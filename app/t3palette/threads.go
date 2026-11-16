package t3palette

import (
	"context"
	"fmt"
	"path"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// runtimeMode is what a thread started from the palette may do on its own,
// as a thread started from T3's CLI may.
const runtimeMode = "full-access"

// Picker rows that are not a thread, a project or a folder.
type (
	newThreadRow  struct{}              // starts a thread
	newProjectRow struct{}              // adds a project
	dirRow        struct{ path string } // a folder of the server's machine
	useFolder     struct{ path string } // the folder being browsed, as a new project
)

// threadPicker lists the live server's threads, freshly fetched.
func (t *Palette) threadPicker(tea.Model) tea.Cmd {
	return t.onLive(func(s Session) tea.Cmd {
		return ask(func(ctx context.Context) tea.Msg {
			sh, err := t.fetchShell(ctx, s)
			if err != nil {
				return t3Error("list threads", err)
			}
			return t.threadList(s, sh, t.state().project)
		})
	})
}

// threadList is the thread picker for s's listing sh: a new thread first,
// then the threads of project p (every thread when p is nil), most recent
// first.
func (t *Palette) threadList(s Session, sh t3client.Shell, p *t3client.Project) tea.Msg {
	title := "T3 Code: " + s.Label()
	if p != nil {
		title += " · " + p.Title
	}
	current := t.state().currentThreadID()
	items := append([]splitview.PickerItem{{Label: "New thread…", Detail: "start a thread and its chat", Value: newThreadRow{}}},
		threadItems(sh, projectThreads(sh, p), current)...)
	return splitview.ShowPickerMsg{
		Title: title,
		Items: items,
		OnPick: func(it splitview.PickerItem) tea.Cmd {
			if _, start := it.Value.(newThreadRow); start {
				if p != nil {
					return t.launch(s, *p)
				}
				return t.projectPicker(s)
			}
			th, _ := it.Value.(t3client.Thread)
			return t.open(s, sh, th.ID)
		},
	}
}

// projectsCmd lists the live server's projects, freshly fetched.
func (t *Palette) projectsCmd(tea.Model) tea.Cmd {
	return t.onLive(func(s Session) tea.Cmd {
		return ask(func(ctx context.Context) tea.Msg {
			sh, err := t.fetchShell(ctx, s)
			if err != nil {
				return t3Error("list projects", err)
			}
			return t.projectList(s, sh)
		})
	})
}

// projectList is the project picker for s's listing sh: a new project
// first, then the projects with their thread counts. Picking one leads to
// its threads.
func (t *Palette) projectList(s Session, sh t3client.Shell) tea.Msg {
	items := make([]splitview.PickerItem, 0, len(sh.Projects)+1)
	items = append(items, splitview.PickerItem{Label: "New project…", Detail: "a folder on " + s.Label(), Value: newProjectRow{}})
	for _, it := range projectItems(sh) {
		p, _ := it.Value.(t3client.Project)
		n := len(projectThreads(sh, &p))
		it.Detail = fmt.Sprintf("%s · %d thread%s", p.WorkspaceRoot, n, plural(n))
		items = append(items, it)
	}
	return splitview.ShowPickerMsg{
		Title: "T3 Code: " + s.Label() + " · project",
		Items: items,
		OnPick: func(it splitview.PickerItem) tea.Cmd {
			if _, add := it.Value.(newProjectRow); add {
				return t.browse(s, startFolder(sh))
			}
			p, _ := it.Value.(t3client.Project)
			t.selectProject(s, p)
			return func() tea.Msg {
				return t.threadList(s, sh, &p)
			}
		},
	}
}

// selectProject makes p, a project of s, the one new threads start in,
// while s is the live session.
func (t *Palette) selectProject(s Session, p t3client.Project) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s == t.live {
		t.project = &p
	}
}

// newThread starts a thread in the selected project, or asks which project.
func (t *Palette) newThread(tea.Model) tea.Cmd {
	return t.onLive(func(s Session) tea.Cmd {
		if p := t.state().project; p != nil {
			return t.launch(s, *p)
		}
		return t.projectPicker(s)
	})
}

// projectPicker lists s's projects for a new thread; with a single project
// it starts the thread there at once.
func (t *Palette) projectPicker(s Session) tea.Cmd {
	return ask(func(ctx context.Context) tea.Msg {
		sh, err := t.fetchShell(ctx, s)
		if err != nil {
			return t3Error("list projects", err)
		}
		items := projectItems(sh)
		switch len(items) {
		case 0:
			return splitview.FlashMsg{Text: "T3: no projects on " + s.Label() + "; add one in T3 Code first"}
		case 1:
			p, _ := items[0].Value.(t3client.Project)
			t.selectProject(s, p)
			return t.launch(s, p)()
		}
		return splitview.ShowPickerMsg{
			Title: "T3 Code: new thread in which project?",
			Items: items,
			OnPick: func(it splitview.PickerItem) tea.Cmd {
				p, _ := it.Value.(t3client.Project)
				t.selectProject(s, p)
				return t.launch(s, p)
			},
		}
	})
}

// startFolder is where browsing for a new project begins: beside the
// projects the server already has, else the root.
func startFolder(sh t3client.Shell) string {
	for _, p := range sh.Projects {
		if dir := path.Dir(p.WorkspaceRoot); dir != "." && dir != "" {
			return strings.TrimSuffix(dir, "/") + "/"
		}
	}
	return "/"
}

// browse lists the folders under dir on s's machine and lets the user walk
// into one, go up, type a path, or make the listed folder a project.
func (t *Palette) browse(s Session, dir string) tea.Cmd {
	return asking("listing "+dir, func(ctx context.Context) tea.Msg {
		parent, entries, err := s.Server().Browse(ctx, dir)
		if err != nil {
			return t3Error("browse "+dir, err)
		}
		items := []splitview.PickerItem{{Label: "Use this folder", Detail: parent, Value: useFolder{parent}}}
		if up := path.Dir(parent); up != parent {
			items = append(items, splitview.PickerItem{Label: "..", Detail: up, Value: dirRow{up}})
		}
		for _, e := range entries {
			items = append(items, splitview.PickerItem{Label: e.Name + "/", Value: dirRow{e.Path}})
		}
		return splitview.ShowPickerMsg{
			Title: "T3 Code: new project in " + parent,
			Items: items,
			OnPick: func(it splitview.PickerItem) tea.Cmd {
				switch v := it.Value.(type) {
				case dirRow:
					return t.browse(s, strings.TrimSuffix(v.path, "/")+"/")
				case useFolder:
					return t.createProject(s, v.path)
				}
				return nil
			},
			OnText: func(typed string) tea.Cmd {
				return t.browse(s, typed)
			},
			TextLabel: "Go to %s",
		}
	})
}

// createProject registers root on s's server as a project named after its
// folder and starts its first thread.
func (t *Palette) createProject(s Session, root string) tea.Cmd {
	return asking("adding project "+root, func(ctx context.Context) tea.Msg {
		p, err := s.Server().CreateProject(ctx, path.Base(root), root)
		if err != nil {
			return t3Error("add project", err)
		}
		t.selectProject(s, p)
		return t.launch(s, p)()
	})
}

// launch starts an empty thread in p and opens it with its chat, where the
// first message is typed.
func (t *Palette) launch(s Session, p t3client.Project) tea.Cmd {
	return asking("starting thread in "+p.Title, func(ctx context.Context) tea.Msg {
		model, err := s.Server().DefaultModel(ctx, p)
		if err != nil {
			return t3Error("start thread", err)
		}
		return t.launchMsg(ctx, s, t3client.LaunchRequest{ProjectID: p.ID, Model: model, RuntimeMode: runtimeMode})
	})
}

// launchMsg starts the thread req on s and opens it with its chat.
func (t *Palette) launchMsg(ctx context.Context, s Session, req t3client.LaunchRequest) tea.Msg {
	id, err := s.Server().LaunchThread(ctx, req)
	if err != nil {
		return t3Error("start thread", err)
	}
	sh, err := t.shellWithThread(ctx, s, id)
	if err != nil {
		return t3Error("open new thread", err)
	}
	return t.openMsg(s, sh, id)
}

// openThread is the palette row of one of s's threads: it fetches the list
// again, so a thread renamed or archived since is seen as it is, and opens
// the thread.
func (t *Palette) openThread(s Session, threadID string) func(tea.Model) tea.Cmd {
	return func(tea.Model) tea.Cmd {
		return asking("opening thread", func(ctx context.Context) tea.Msg {
			sh, err := t.fetchShell(ctx, s)
			if err != nil {
				return t3Error("open thread", err)
			}
			return t.openMsg(s, sh, threadID)
		})
	}
}

// open makes threadID, from s's listing sh, the thread on screen.
func (t *Palette) open(s Session, sh t3client.Shell, threadID string) tea.Cmd {
	return tea.Batch(flash("T3: opening thread…"), func() tea.Msg {
		return t.openMsg(s, sh, threadID)
	})
}

// openMsg builds the review of threadID over s, with no annotations, and
// the message that puts it on screen, with the thread's chat pane when one
// comes along.
func (t *Palette) openMsg(s Session, sh t3client.Shell, threadID string) tea.Msg {
	target, err := t3client.Resolve(sh, threadID)
	if err != nil {
		return t3Error("open thread", err)
	}
	r := &Review{Session: s, Target: target}
	show := t.showMsg(r, annotation.NewStore(), "open thread")
	if _, ok := show.(splitview.ReplaceInnerMsg); !ok {
		return show
	}
	t.mu.Lock()
	chat := t.autoChatLocked(r)
	t.mu.Unlock()
	if chat == nil {
		return show
	}
	return tea.BatchMsg{
		func() tea.Msg {
			return show
		},
		func() tea.Msg {
			return chat
		},
	}
}

// showMsg builds the model of r over store and the message that puts it on
// screen in place of the current review, which install retires.
func (t *Palette) showMsg(r *Review, store *annotation.Store, what string) tea.Msg {
	if !t.prepare(r) {
		return splitview.FlashMsg{Text: "T3: " + what + ": the connection was closed meanwhile"}
	}
	model, err := t.cfg.NewReview(r, store)
	if err != nil {
		t.abandon(r)
		return t3Error(what, err)
	}
	return splitview.ReplaceInnerMsg{
		Model: model,
		Retire: func(old tea.Model) {
			t.install(old, r, store)
		},
	}
}
