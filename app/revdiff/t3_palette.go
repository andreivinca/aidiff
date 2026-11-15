package main

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3chat"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/ui"
)

// t3CallTimeout bounds one request to a connected T3 server.
const t3CallTimeout = 30 * time.Second

// t3Palette adds the T3 Code commands to the ctrl+p palette: connect to a
// server, switch threads, start a thread, and message the agent. It owns the
// live session and replaces the review when the thread changes.
type t3Palette struct {
	opts        options
	deps        reviewDeps
	description string // the user's --description, kept under the thread's
	autoChat    bool   // open a chat pane along with a thread, unless one is open

	mu         sync.Mutex
	program    *tea.Program      // receives the thread watch's refreshes; set before the program runs
	cur        *t3Review         // the connected server and the reviewed thread; nil before the first connection
	stop       func()            // live refresh of the current review: the file watch or the thread watch
	hist       histReq           // how the current review's annotations are filed in history
	chats      []*t3chat.Pane    // open chat panes; they follow the reviewed thread
	shell      t3client.Shell    // the server's projects and threads, from the last fetch
	shellAt    time.Time         // when shell was fetched
	refreshing bool              // a background fetch of shell is running
	project    *t3client.Project // the project new threads start in and the palette lists; nil until chosen
}

// newT3Palette starts from the review the run opened: a T3 thread when the
// --t3 flags named one, else the local source filed under its gitRoot/workDir.
func newT3Palette(opts options, deps reviewDeps, description string, src sourceSetup) *t3Palette {
	return &t3Palette{
		opts:        opts,
		deps:        deps,
		description: description,
		autoChat:    !opts.NoChat,
		cur:         src.t3,
		stop:        func() {},
		hist:        histReq{opts: opts, gitRoot: src.gitRoot, workDir: src.workDir, t3: src.t3 != nil},
	}
}

// start records the program and the opening review's live refresh.
func (t *t3Palette) start(p *tea.Program, stop func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.program = p
	if stop != nil {
		t.stop = stop
	}
}

// close stops the live refresh and ends the T3 session, if any.
func (t *t3Palette) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stop()
	t.stop = func() {}
	if t.cur != nil {
		_ = t.cur.session.Close()
		t.cur = nil
	}
}

// history is how the current review's annotations are filed.
func (t *t3Palette) history() histReq {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hist
}

// current is the connected review, nil before the first connection.
func (t *t3Palette) current() *t3Review {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cur
}

// inlineThreads is how many recent threads the palette lists directly.
const inlineThreads = 8

// shellMaxAge is how old the cached thread list may be before the palette
// refreshes it in the background.
const shellMaxAge = 5 * time.Second

// commands lists the T3 entries for the palette's current state: a new
// thread first, then the recent sessions, then what the open thread offers.
func (t *t3Palette) commands() []splitview.Command {
	cur := t.current()
	if cur == nil {
		return []splitview.Command{{Name: "T3: Connect to server…", Run: t.connectPicker}}
	}
	t.refreshShell()
	server := cur.session.Label
	t.mu.Lock()
	sh, project := t.shell, t.project
	t.mu.Unlock()
	scope := server
	if project != nil {
		scope = project.Title
	}
	cmds := []splitview.Command{
		{Name: "T3: New thread…", Detail: scope, Run: t.newThread},
		{Name: "T3: Projects…", Detail: server, Run: t.projectsCmd},
	}
	current := ""
	if cur.target.Thread != nil {
		current = cur.target.Thread.ID
	}
	threads := projectThreads(sh, project)
	for i, th := range threads {
		if i == inlineThreads {
			break
		}
		label := "T3: " + threadTitle(th)
		if th.ID == current {
			label = "T3: ▸ " + threadTitle(th)
		}
		cmds = append(cmds, splitview.Command{Name: label, Detail: threadDetail(sh, th), Run: t.openThread(th.ID)})
	}
	if len(threads) > inlineThreads {
		cmds = append(cmds, splitview.Command{Name: "T3: All threads…", Detail: scope, Run: t.threadPicker})
	}
	if cur.target.Thread != nil {
		thread := threadTitle(*cur.target.Thread)
		cmds = append(cmds,
			splitview.Command{Name: "T3: Open chat", Detail: thread, Run: t.openChat},
			splitview.Command{Name: "T3: Send message to agent…", Detail: thread, Run: t.messagePrompt},
			splitview.Command{Name: "T3: Send annotations to agent", Detail: thread, Run: t.sendAnnotations},
		)
	}
	return append(cmds, splitview.Command{Name: "T3: Connect to another server…", Run: t.connectPicker})
}

// setShellLocked caches a fetched thread list; mu must be held.
func (t *t3Palette) setShellLocked(sh t3client.Shell) {
	t.shell, t.shellAt = sh, time.Now()
}

// shellRefreshedMsg redraws the palette once a background fetch landed; the
// split passes it on and nothing acts on it.
type shellRefreshedMsg struct{}

// refreshShell fetches the thread list in the background when the cached
// one is older than shellMaxAge.
func (t *t3Palette) refreshShell() {
	t.mu.Lock()
	cur, p := t.cur, t.program
	stale := cur != nil && !t.refreshing && time.Since(t.shellAt) > shellMaxAge
	if stale {
		t.refreshing = true
	}
	t.mu.Unlock()
	if !stale {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), t3CallTimeout)
		defer cancel()
		sh, err := t3client.FetchShell(ctx, cur.session.Conn)
		t.mu.Lock()
		t.refreshing = false
		if err == nil && t.cur == cur {
			t.setShellLocked(sh)
		}
		t.mu.Unlock()
		if err == nil && p != nil {
			p.Send(shellRefreshedMsg{})
		}
	}()
}

// openThread is the palette entry for one thread: it fetches the list again,
// so a thread renamed or archived since is seen as it is, and opens it.
func (t *t3Palette) openThread(threadID string) func(tea.Model) tea.Cmd {
	return func(tea.Model) tea.Cmd {
		return tea.Batch(flash("T3: opening thread…"), func() tea.Msg {
			cur := t.current()
			if cur == nil {
				return splitview.FlashMsg{Text: "T3: not connected"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), t3CallTimeout)
			defer cancel()
			sh, err := t3client.FetchShell(ctx, cur.session.Conn)
			if err != nil {
				return t3Error("open thread", err)
			}
			t.mu.Lock()
			t.setShellLocked(sh)
			t.mu.Unlock()
			return t.openMsg(sh, threadID)
		})
	}
}

// connectPicker offers the servers aidiff knows of: the local T3 Code, servers
// with a saved login and the hosts of ~/.ssh/config. Anything else is typed.
func (t *t3Palette) connectPicker(tea.Model) tea.Cmd {
	return func() tea.Msg {
		return splitview.ShowPickerMsg{
			Title:     "T3 Code: connect to",
			Items:     serverItems(t3TokenStore()),
			OnPick:    func(it splitview.PickerItem) tea.Cmd { return t.connect(it.Label) },
			OnText:    func(s string) tea.Cmd { return t.connect(serverSpec(s)) },
			TextLabel: "Connect to %s",
		}
	}
}

// serverItems lists the connection candidates, each once.
func serverItems(store t3client.TokenStore) []splitview.PickerItem {
	var items []splitview.PickerItem
	seen := map[string]bool{}
	add := func(label, detail string) {
		if label == "" || seen[label] {
			return
		}
		seen[label] = true
		items = append(items, splitview.PickerItem{Label: label, Detail: detail})
	}
	if _, err := t3client.LocalOrigin(); err == nil {
		add("local", "T3 Code on this machine")
	}
	saved, _ := store.Servers() // an unreadable token file is reported when connecting
	for _, s := range saved {
		add(s, "saved login")
	}
	for _, h := range t3client.SSHHosts() {
		add("ssh://"+h, "~/.ssh/config")
	}
	return items
}

// serverSpec reads typed text as a server name: a bare name is an ssh host.
func serverSpec(s string) string {
	s = strings.TrimSpace(s)
	if s == "local" || strings.Contains(s, "://") {
		return s
	}
	return "ssh://" + s
}

// connect reaches the server, makes it the live session and lists its threads.
func (t *t3Palette) connect(spec string) tea.Cmd {
	return tea.Batch(flash("T3: connecting to "+spec+"…"), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		s, err := t3client.Connect(ctx, spec, t3client.ConnectOptions{Store: t3TokenStore()})
		if err != nil {
			return t3Error("connect", err)
		}
		sh, err := t3client.FetchShell(ctx, s.Conn)
		if err != nil {
			_ = s.Close()
			return t3Error("list threads", err)
		}
		t.adoptSession(s)
		t.mu.Lock()
		t.setShellLocked(sh)
		t.project = nil
		t.mu.Unlock()
		picker := t.projectList(s, sh)
		return tea.BatchMsg{
			func() tea.Msg { return ui.MachineIdentityMsg{Name: s.MachineName(), T3: true} },
			func() tea.Msg { return picker },
		}
	})
}

// adoptSession makes s the live session. The previous one is closed, and with
// it the refresh of a thread it served; a local review keeps its file watch.
func (t *t3Palette) adoptSession(s *t3client.Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur != nil && t.cur.session != s {
		if t.hist.t3 {
			t.stop()
			t.stop = func() {}
		}
		_ = t.cur.session.Close()
	}
	t.cur = &t3Review{session: s}
}

// threadPicker lists the live server's threads, freshly fetched.
func (t *t3Palette) threadPicker(tea.Model) tea.Cmd {
	cur := t.current()
	if cur == nil {
		return t.connectPicker(nil)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), t3CallTimeout)
		defer cancel()
		sh, err := t3client.FetchShell(ctx, cur.session.Conn)
		if err != nil {
			return t3Error("list threads", err)
		}
		t.mu.Lock()
		t.setShellLocked(sh)
		project := t.project
		t.mu.Unlock()
		return t.threadList(cur.session, sh, project)
	}
}

// newThreadRow marks the picker row that starts a thread.
type newThreadRow struct{}

// threadList is the thread picker for a shell snapshot: a new thread first,
// then the sessions of project p (every session when p is nil), most recent
// first.
func (t *t3Palette) threadList(s *t3client.Session, sh t3client.Shell, p *t3client.Project) tea.Msg {
	threads := projectThreads(sh, p)
	current := ""
	if cur := t.current(); cur != nil && cur.target.Thread != nil {
		current = cur.target.Thread.ID
	}
	title := "T3 Code: " + s.Label
	if p != nil {
		title += " · " + p.Title
	}
	items := append([]splitview.PickerItem{{Label: "New thread…", Detail: "start a thread and its chat", Value: newThreadRow{}}},
		threadItems(sh, threads, current)...)
	return splitview.ShowPickerMsg{
		Title: title,
		Items: items,
		OnPick: func(it splitview.PickerItem) tea.Cmd {
			if _, start := it.Value.(newThreadRow); start {
				if p != nil {
					return t.launch(*p)
				}
				return t.projectPicker(nil)
			}
			th, _ := it.Value.(t3client.Thread)
			return t.open(sh, th.ID)
		},
	}
}

// projectThreads are the threads of p, newest first, or every thread when p
// is nil.
func projectThreads(sh t3client.Shell, p *t3client.Project) []t3client.Thread {
	all := t3client.SortedThreads(sh)
	if p == nil {
		return all
	}
	var out []t3client.Thread
	for _, th := range all {
		if th.ProjectID == p.ID {
			out = append(out, th)
		}
	}
	return out
}

// newProjectRow marks the picker row that adds a project.
type newProjectRow struct{}

// projectList is the project picker: a new project first, then the projects
// with their thread counts. Picking one leads to its threads.
func (t *t3Palette) projectList(s *t3client.Session, sh t3client.Shell) tea.Msg {
	items := make([]splitview.PickerItem, 0, len(sh.Projects)+1)
	items = append(items, splitview.PickerItem{Label: "New project…", Detail: "a folder on " + s.Label, Value: newProjectRow{}})
	for _, it := range projectItems(sh) {
		p, _ := it.Value.(t3client.Project)
		n := len(projectThreads(sh, &p))
		it.Detail = fmt.Sprintf("%s · %d thread%s", p.WorkspaceRoot, n, plural(n))
		items = append(items, it)
	}
	return splitview.ShowPickerMsg{
		Title: "T3 Code: " + s.Label + " · project",
		Items: items,
		OnPick: func(it splitview.PickerItem) tea.Cmd {
			if _, add := it.Value.(newProjectRow); add {
				return t.browse(startFolder(sh))
			}
			p, _ := it.Value.(t3client.Project)
			t.selectProject(p)
			return func() tea.Msg { return t.threadList(s, sh, &p) }
		},
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// selectProject makes p the project new threads start in.
func (t *t3Palette) selectProject(p t3client.Project) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.project = &p
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

// dirRow is a folder of the server's machine; useFolder is the one browsed.
type (
	dirRow    struct{ path string }
	useFolder struct{ path string }
)

// browse lists the folders under p on the server and lets the user walk into
// one, go up, type a path, or make the listed folder a project.
func (t *t3Palette) browse(p string) tea.Cmd {
	return tea.Batch(flash("T3: listing "+p+"…"), func() tea.Msg {
		cur := t.current()
		if cur == nil {
			return splitview.FlashMsg{Text: "T3: not connected"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), t3CallTimeout)
		defer cancel()
		parent, entries, err := t3client.Browse(ctx, cur.session.Conn, p)
		if err != nil {
			return t3Error("browse "+p, err)
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
					return t.browse(strings.TrimSuffix(v.path, "/") + "/")
				case useFolder:
					return t.createProject(v.path)
				}
				return nil
			},
			OnText:    func(typed string) tea.Cmd { return t.browse(typed) },
			TextLabel: "Go to %s",
		}
	})
}

// createProject registers root as a project named after its folder and asks
// for the first thread's message.
func (t *t3Palette) createProject(root string) tea.Cmd {
	return tea.Batch(flash("T3: adding project "+root+"…"), func() tea.Msg {
		cur := t.current()
		if cur == nil {
			return splitview.FlashMsg{Text: "T3: not connected"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), t3CallTimeout)
		defer cancel()
		p, err := t3client.CreateProject(ctx, cur.session.Conn, path.Base(root), root)
		if err != nil {
			return t3Error("add project", err)
		}
		if sh, ferr := t3client.FetchShell(ctx, cur.session.Conn); ferr == nil {
			t.mu.Lock()
			t.setShellLocked(sh)
			t.mu.Unlock()
		}
		t.selectProject(p)
		return t.launch(p)()
	})
}

// projectsCmd lists the server's projects, freshly fetched.
func (t *t3Palette) projectsCmd(tea.Model) tea.Cmd {
	cur := t.current()
	if cur == nil {
		return t.connectPicker(nil)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), t3CallTimeout)
		defer cancel()
		sh, err := t3client.FetchShell(ctx, cur.session.Conn)
		if err != nil {
			return t3Error("list projects", err)
		}
		t.mu.Lock()
		t.setShellLocked(sh)
		t.mu.Unlock()
		return t.projectList(cur.session, sh)
	}
}

// newThread starts a thread in the chosen project, or asks which project.
func (t *t3Palette) newThread(tea.Model) tea.Cmd {
	t.mu.Lock()
	p := t.project
	t.mu.Unlock()
	if p == nil {
		return t.projectPicker(nil)
	}
	return t.launch(*p)
}

// threadItems renders threads as picker rows; the reviewed one is marked.
func threadItems(sh t3client.Shell, threads []t3client.Thread, current string) []splitview.PickerItem {
	items := make([]splitview.PickerItem, 0, len(threads))
	for _, th := range threads {
		label := threadTitle(th)
		if th.ID == current {
			label = "▸ " + label
		}
		items = append(items, splitview.PickerItem{Label: label, Detail: threadDetail(sh, th), Value: th})
	}
	return items
}

// threadTitle names a thread for the palette.
func threadTitle(th t3client.Thread) string {
	if title := strings.TrimSpace(th.Title); title != "" {
		return title
	}
	return trunc(th.ID, 9)
}

// threadDetail is the thread's project, state and branch.
func threadDetail(sh t3client.Shell, th t3client.Thread) string {
	var parts []string
	if p, ok := sh.Project(th.ProjectID); ok {
		parts = append(parts, p.Title)
	}
	state := "idle"
	if th.Running() {
		state = "running"
	}
	parts = append(parts, state)
	if th.Branch != nil && *th.Branch != "" {
		parts = append(parts, *th.Branch)
	}
	return strings.Join(parts, " · ")
}

// open makes threadID the reviewed thread: a fresh review replaces the
// current one, whose annotations go to history.
func (t *t3Palette) open(sh t3client.Shell, threadID string) tea.Cmd {
	return tea.Batch(flash("T3: opening thread…"), func() tea.Msg { return t.openMsg(sh, threadID) })
}

// openMsg builds the review of threadID and the message that installs it.
func (t *t3Palette) openMsg(sh t3client.Shell, threadID string) tea.Msg {
	cur := t.current()
	if cur == nil {
		return splitview.FlashMsg{Text: "T3: not connected"}
	}
	target, err := t3client.Resolve(sh, threadID)
	if err != nil {
		return t3Error("open thread", err)
	}
	review := &t3Review{
		session:  cur.session,
		target:   target,
		renderer: t3client.NewRenderer(cur.session.Conn, target.Cwd, t.opts.t3SourceKind()),
	}
	model, err := t.deps.newModel(vcsSetup{renderer: review.renderer, machineName: review.session.MachineName()}, annotation.NewStore(), review.description(t.description))
	if err != nil {
		return t3Error("open thread", err)
	}
	replace := splitview.ReplaceInnerMsg{Model: model, Retire: func(old tea.Model) { t.retire(old, review) }}
	if !t.autoChat || target.Thread == nil || t.chatOpen() {
		return replace
	}
	// the thread's chat comes along, in place of a shell running an agent
	chat := t.chatPaneMsg(cur.session.Conn, *target.Thread)
	return tea.BatchMsg{func() tea.Msg { return replace }, func() tea.Msg { return chat }}
}

// retire files the replaced review's annotations in history and makes review
// the current one, with its live refresh.
func (t *t3Palette) retire(old tea.Model, review *t3Review) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if m, ok := old.(ui.Model); ok && m.Store().Count() > 0 {
		h := t.hist
		h.annotations, h.files = m.Store().FormatOutput(), m.Store().Files()
		saveHistory(h)
	}
	t.stop()
	t.cur = review
	t.stop = func() {}
	if t.program != nil {
		t.stop = review.watch(t.program)
	}
	t.hist = histReq{opts: t.opts, t3: true}
	project := review.target.Project
	t.project = &project
	t.followChatsLocked(review)
}

// followChatsLocked points the open chat panes at the reviewed thread and
// forgets closed ones; mu must be held.
func (t *t3Palette) followChatsLocked(review *t3Review) {
	live := t.chats[:0]
	for _, c := range t.chats {
		if c.Exited() {
			continue
		}
		if review.target.Thread != nil {
			c.Follow(review.session.Conn, review.target.Thread.ID, threadTitle(*review.target.Thread))
		}
		live = append(live, c)
	}
	t.chats = live
}

// openChat adds a chat pane for the reviewed thread to the right column.
func (t *t3Palette) openChat(tea.Model) tea.Cmd {
	return func() tea.Msg { return t.openChatMsg() }
}

// openChatMsg is the split's request for a chat pane on the reviewed thread,
// or a flash when no thread is open.
func (t *t3Palette) openChatMsg() tea.Msg {
	cur := t.current()
	if cur == nil || cur.target.Thread == nil {
		return splitview.FlashMsg{Text: "T3: open a thread first (T3: Connect to server…)"}
	}
	return t.chatPaneMsg(cur.session.Conn, *cur.target.Thread)
}

// chatPaneMsg is the split's request for a chat pane on th over conn.
func (t *t3Palette) chatPaneMsg(conn *t3client.Conn, th t3client.Thread) tea.Msg {
	return splitview.OpenPaneMsg{Start: func(width, height int) (splitview.Terminal, error) {
		pane := t3chat.New(conn, th.ID, threadTitle(th), width, height, t3chat.Config{NoColor: t.opts.NoColors})
		t.mu.Lock()
		t.chats = append(t.chats, pane)
		t.mu.Unlock()
		return pane, nil
	}}
}

// projectPicker lists the live server's projects for a new thread.
func (t *t3Palette) projectPicker(tea.Model) tea.Cmd {
	cur := t.current()
	if cur == nil {
		return t.connectPicker(nil)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), t3CallTimeout)
		defer cancel()
		sh, err := t3client.FetchShell(ctx, cur.session.Conn)
		if err != nil {
			return t3Error("list projects", err)
		}
		items := projectItems(sh)
		if len(items) == 0 {
			return splitview.FlashMsg{Text: "T3: no projects on " + cur.session.Label + "; add one in T3 Code first"}
		}
		if len(items) == 1 {
			p, _ := items[0].Value.(t3client.Project)
			t.selectProject(p)
			return t.launch(p)() // one project: straight to its empty thread
		}
		return splitview.ShowPickerMsg{
			Title: "T3 Code: new thread in which project?",
			Items: items,
			OnPick: func(it splitview.PickerItem) tea.Cmd {
				p, _ := it.Value.(t3client.Project)
				t.selectProject(p)
				return t.launch(p)
			},
		}
	}
}

// projectItems renders the projects still in use as picker rows, by title.
func projectItems(sh t3client.Shell) []splitview.PickerItem {
	var items []splitview.PickerItem
	for _, p := range sh.Projects {
		if p.Archived() {
			continue
		}
		items = append(items, splitview.PickerItem{Label: p.Title, Detail: p.WorkspaceRoot, Value: p})
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label) })
	return items
}

// launch starts an empty thread in p and opens it with its chat, where the
// first message is typed.
func (t *t3Palette) launch(p t3client.Project) tea.Cmd {
	return tea.Batch(flash("T3: starting thread in "+p.Title+"…"), func() tea.Msg {
		cur := t.current()
		if cur == nil {
			return splitview.FlashMsg{Text: "T3: not connected"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		model, err := t3client.DefaultModel(ctx, cur.session.Conn, p)
		if err != nil {
			return t3Error("start thread", err)
		}
		req := t3client.LaunchRequest{ProjectID: p.ID, Model: model}
		id, err := t3client.LaunchThread(ctx, cur.session.Conn, req)
		if err != nil {
			return t3Error("start thread", err)
		}
		sh, err := shellWithThread(ctx, cur.session.Conn, id)
		if err != nil {
			return t3Error("open new thread", err)
		}
		t.mu.Lock()
		t.setShellLocked(sh)
		t.mu.Unlock()
		return t.openMsg(sh, id)
	})
}

// chatOpen reports whether a chat pane is open, forgetting closed ones.
func (t *t3Palette) chatOpen() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	live := t.chats[:0]
	for _, c := range t.chats {
		if !c.Exited() {
			live = append(live, c)
		}
	}
	t.chats = live
	return len(t.chats) > 0
}

// shellWithThread fetches the shell until it lists threadID, which a server
// may publish a moment after launchThread returns.
func shellWithThread(ctx context.Context, c *t3client.Conn, threadID string) (t3client.Shell, error) {
	const attempts, pause = 5, 300 * time.Millisecond
	var sh t3client.Shell
	for range attempts {
		var err error
		if sh, err = t3client.FetchShell(ctx, c); err != nil {
			return sh, fmt.Errorf("list threads: %w", err)
		}
		for _, th := range sh.Threads {
			if th.ID == threadID {
				return sh, nil
			}
		}
		select {
		case <-ctx.Done():
			return sh, fmt.Errorf("waiting for the thread: %w", ctx.Err())
		case <-time.After(pause):
		}
	}
	return sh, fmt.Errorf("thread %s is not listed yet; use \"T3: Switch thread…\"", trunc(threadID, 9))
}

// messagePrompt asks for a message to the reviewed thread's agent.
func (t *t3Palette) messagePrompt(tea.Model) tea.Cmd {
	th, ok := t.thread()
	if !ok {
		return flash("T3: open a thread first")
	}
	return func() tea.Msg {
		return splitview.ShowPromptMsg{
			Title:       "T3 Code: message to " + threadTitle(th),
			Placeholder: "tell the agent what to do next",
			OnSubmit:    func(text string) tea.Cmd { return t.send(th, text, "message sent") },
		}
	}
}

// sendAnnotations hands the review's annotations to the thread's agent.
func (t *t3Palette) sendAnnotations(inner tea.Model) tea.Cmd {
	th, ok := t.thread()
	if !ok {
		return flash("T3: open a thread first")
	}
	m, isReview := inner.(ui.Model)
	if !isReview || m.Store().Count() == 0 {
		return flash("T3: no annotations to send")
	}
	n := m.Store().Count()
	text := "Review annotations from aidiff:\n\n" + m.Store().FormatOutput()
	return t.send(th, text, fmt.Sprintf("%d annotation(s) sent", n))
}

// thread is the reviewed thread, if a T3 thread is open.
func (t *t3Palette) thread() (t3client.Thread, bool) {
	cur := t.current()
	if cur == nil || cur.target.Thread == nil {
		return t3client.Thread{}, false
	}
	return *cur.target.Thread, true
}

// send delivers text to th's agent and reports done.
func (t *t3Palette) send(th t3client.Thread, text, done string) tea.Cmd {
	return tea.Batch(flash("T3: sending to "+threadTitle(th)+"…"), func() tea.Msg {
		cur := t.current()
		if cur == nil {
			return splitview.FlashMsg{Text: "T3: not connected"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), t3CallTimeout)
		defer cancel()
		if err := t3client.SendMessage(ctx, cur.session.Conn, th.ID, text); err != nil {
			return t3Error("send", err)
		}
		return splitview.FlashMsg{Text: "T3: " + done + " to " + threadTitle(th)}
	})
}

// flash is a command that shows text on the bottom row.
func flash(text string) tea.Cmd {
	return func() tea.Msg { return splitview.FlashMsg{Text: text} }
}

// t3Error reports a failed T3 step on the bottom row.
func t3Error(what string, err error) tea.Msg {
	return splitview.FlashMsg{Text: "T3: " + what + ": " + err.Error()}
}
