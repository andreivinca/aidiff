package t3palette

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// Tabs from one run to the next. At exit the program saves the open tabs
// (SaveTabs): for each, only what reopens it, a local folder, or a T3
// server's project with the thread on screen there; never its terminals,
// chat panes or annotations. The next run brings them back as tabs, in the
// same order, around the workspace it starts in (StartTabs), but opens
// none of them: a folder's review is built, and a T3 server connected to,
// only once its tab is shown (Open), so many tabs start as fast as one. A
// restored tab that cannot come back (its folder gone, its project removed,
// its server no longer one aidiff can name) closes with a note once shown;
// one whose server does not answer stays, and showing it again tries again.

// TabsConfig is how the palette keeps the open tabs from one run to the
// next.
type TabsConfig struct {
	// Load reads the tabs the last run left open, in order, and the index of
	// the one it had on screen (-1 for none). It is called once, when the
	// palette is built, and only by a run that starts in a named workspace.
	Load func() (tabs []Tab, current int)
	// Save replaces them with this run's (SaveTabs).
	Save func(tabs []Tab, current int) error
}

// Tab is what reopens a tab: a folder of this machine, or a project of a
// T3 server and the thread on screen in it.
type Tab struct {
	Dir          string // a local folder; "" for a T3 project
	Server       string // the T3 server, as a saved connection dials it (Session.Label)
	Machine      string // the server's machine, named on the tab until it is connected to
	ProjectID    string
	ProjectTitle string // named on the tab until its server is connected to
	ThreadID     string // the thread on screen in the project's workspace; "" for its root
}

// StartTab is a saved T3 tab a run starts on: a run that names no target
// of its own starts on the tab the last run had on screen, and a T3
// project's review can only be built once its server is connected to. It
// is the workspace on screen from the start, said to be connecting, and
// opens (Open) as soon as the program runs.
type StartTab struct {
	Tab Tab // it comes back with its chat but no terminal, --terminal or not
	// Fallback is the folder opened when it cannot come back and no other
	// tab is left: the working directory, as a run that names nothing opens
	// it without saved tabs.
	Fallback string
}

// normalTab is tab as the palette keys it: a folder cleaned, a server named
// as a session to it names it, so the review it opens finds its tab.
func normalTab(tab Tab) Tab {
	if tab.Dir != "" {
		tab.Dir = filepath.Clean(tab.Dir)
	} else if label, err := t3client.ServerLabel(tab.Server); err == nil {
		tab.Server = label
	}
	return tab
}

// key is the workspace tab reopens, "" when it names none.
func (tab Tab) key() string {
	switch {
	case tab.Dir != "":
		return localKey(tab.Dir)
	case tab.Server != "" && tab.ProjectID != "":
		return t3Key(tab.Server, tab.ProjectID)
	}
	return ""
}

// errClosing fails what a restored tab waits on once the palette closes.
var errClosing = errors.New("aidiff is closing")

// tabDial is a connection being made for restored tabs: the restored tabs
// of its server shown meanwhile wait for it rather than dial again. Its
// fields are set before done is closed.
type tabDial struct {
	done chan struct{}
	s    Session
	sh   t3client.Shell
	err  error
}

// initTabsLocked brings back the tabs the last run left open, around the
// run's own workspace: in their order, that workspace in its place when it
// is one of them, else after them. A run that starts in no named workspace
// (stdin, a compare pair) brings none back and saves none (SaveTabs). mu
// must be held, or the palette not shared yet.
func (t *Palette) initTabsLocked() {
	t.keepTabs = t.ws != ""
	if !t.keepTabs || t.cfg.Tabs.Load == nil {
		return
	}
	saved, current := t.cfg.Tabs.Load()
	for i, tab := range saved {
		tab = normalTab(tab)
		key := tab.key()
		if key == "" || slices.Contains(t.tabs, key) {
			continue
		}
		t.tabs = append(t.tabs, key)
		if key == t.ws {
			continue // the run's own: open already, or opening (initStartTabLocked)
		}
		t.restored[key] = &tab
		if i == current {
			t.recentTab = key
		}
	}
	if len(t.tabs) > 0 && !slices.Contains(t.tabs, t.ws) {
		t.tabs = append(t.tabs, t.ws)
	}
}

// initStartTabLocked makes the saved T3 tab the run starts on (StartTab),
// when it names no review of its own, the workspace on screen: restored,
// not opened yet, so that Open opens it. mu must be held, or the palette
// not shared yet.
func (t *Palette) initStartTabLocked() {
	st := t.cfg.StartTab
	if st == nil || t.ws != "" {
		return
	}
	tab := normalTab(st.Tab)
	key := tab.key()
	if key == "" || tab.Dir != "" {
		return
	}
	t.ws, t.startTab = key, key
	t.restored[key] = &tab
}

// Opening reports whether the workspace on screen at the start is a saved
// tab that opens once the program runs (StartTab): the split shows it as
// not opened yet (splitview.Config.Opening).
func (t *Palette) Opening() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.startTab != "" && t.restored[t.startTab] != nil
}

// StartTabs are the tabs the split starts with (splitview.Config.Tabs), in
// order, the run's own workspace among them, and the one the last run had
// on screen, which the switcher lists right after the one on screen
// (Config.Recent). Both are empty when the run brings no tabs back.
func (t *Palette) StartTabs() (tabs, recent []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.recentTab != "" {
		recent = []string{t.recentTab}
	}
	return slices.Clone(t.tabs), recent
}

// SaveTabs keeps the tabs the program ends with for the next run: keys are
// the split's tabs in order, current the workspace on screen. Each is saved
// as what reopens it, a restored tab not opened in this run as it was
// loaded. A run that started in no named workspace saves nothing: it
// brought no tabs back, and its own would replace them. Call it before
// Close, which forgets the workspaces.
func (t *Palette) SaveTabs(keys []string, current string) error {
	t.mu.Lock()
	save := t.cfg.Tabs.Save
	if !t.keepTabs || save == nil {
		t.mu.Unlock()
		return nil
	}
	tabs, cur := []Tab{}, -1
	for _, k := range keys {
		tab, ok := t.tabLocked(k)
		if !ok {
			continue
		}
		if k == current {
			cur = len(tabs)
		}
		tabs = append(tabs, tab)
	}
	t.mu.Unlock()
	return save(tabs, cur)
}

// tabLocked is what reopens workspace key, false for one the palette does
// not know (the unnamed one). mu must be held.
func (t *Palette) tabLocked(key string) (Tab, bool) {
	if tab, ok := t.restored[key]; ok {
		return *tab, true
	}
	switch r, local := t.workspaceLocked(key); {
	case r != nil:
		tab := Tab{Server: r.Session.Label(), Machine: r.Session.MachineName(),
			ProjectID: r.Target.Project.ID, ProjectTitle: r.Target.Project.Title}
		if th := r.thread(); th != nil {
			tab.ThreadID = th.ID
		}
		return tab, true
	case local != nil:
		return Tab{Dir: local.Dir}, true
	}
	return Tab{}, false
}

// describeRestoredLocked is a restored tab's row: a folder as an opened
// one; a project by the title and machine it had, and whether its server
// is connected to. mu must be held.
func (t *Palette) describeRestoredLocked(tab *Tab) splitview.SessionInfo {
	if tab.Dir != "" {
		return splitview.SessionInfo{
			Name:    display(filepath.Base(tab.Dir)),
			Machine: machineLabel(tab.Dir),
			Path:    display(homeRelative(filepath.Dir(tab.Dir))),
		}
	}
	return splitview.SessionInfo{
		Name:    display(cmp.Or(tab.ProjectTitle, tab.ProjectID)),
		Machine: display(cmp.Or(tab.Machine, tab.Server)) + t3Mark,
		Note:    t.restoredNoteLocked(tab),
	}
}

// restoredNoteLocked says how a restored project's server stands: being
// connected to, connected (nothing to say), or not. mu must be held.
func (t *Palette) restoredNoteLocked(tab *Tab) string {
	switch {
	case tab.Dir != "":
		return ""
	case t.tabDials[tab.Server] != nil, t.dialed.spec == tab.Server && t.dialed.err == nil:
		return "connecting…"
	case t.serverConnLocked(tab.Server) != nil:
		return ""
	}
	return "not connected"
}

// serverConnLocked is the open connection to server label, nil when there
// is none: the one the user made last, the live one among them. mu must be
// held.
func (t *Palette) serverConnLocked(label string) Session {
	for s := range t.connected {
		if s.Label() == label && !ended(s) {
			return s
		}
	}
	return nil
}

// isRestored reports whether workspace key is a restored tab not opened
// yet: not opened another way, nor closed, since.
func (t *Palette) isRestored(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.restored[key]
	return ok
}

// doneRestored is the Done of the switch that puts restored tab key's
// stand-in on screen, which the split makes only when the tab on screen
// closes and no opened tab is left to take the screen: the workspace left
// is filed, and nothing the palette follows is on screen until the tab
// opens (Open, which the split asks at once).
func (t *Palette) doneRestored(key string) func(splitview.WorkspaceSwitch) {
	return func(sw splitview.WorkspaceSwitch) {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.closed || sw.Left == nil {
			return
		}
		prevStop := t.stop
		notes := t.leaveLocked(sw)
		t.ws, t.onScreen, t.local, t.stop = key, nil, nil, noop
		unused := t.takeUnusedLocked()
		t.laterLocked(func() {
			prevStop()
			closeAll(unused)
			t.saveNotes(notes)
		})
	}
}

// openRestored opens restored tab key, now that the user shows it: a
// folder's review is built, a project opened over its server's
// connection, made now when there is none.
// One opening at a time: shown again while it opens (enter on its stand-in,
// its tab key pressed twice during a slow dial), it says so rather than
// build a second review with a second chat.
func (t *Palette) openRestored(key string, tab Tab) tea.Cmd {
	t.mu.Lock()
	busy := t.opening[key]
	t.opening[key] = true
	t.mu.Unlock()
	if busy {
		name := cmp.Or(tab.ProjectTitle, tab.ProjectID)
		if tab.Dir != "" {
			name = filepath.Base(tab.Dir)
		}
		return flash(name + " is opening…")
	}
	if tab.Dir != "" {
		return t.openRestoredFolder(key, tab.Dir)
	}
	return t.openRestoredProject(key, tab)
}

// settled is work, the opening of restored tab key, letting the tab be
// opened again once work failed: it said why, or found nothing to open. A
// review on its way to the screen settles it for good (the tab is no
// longer restored once it lands).
func (t *Palette) settled(key string, work func() tea.Msg) func() tea.Msg {
	return func() tea.Msg {
		msg := work()
		if _, failed := msg.(splitview.FlashMsg); failed || msg == nil {
			t.mu.Lock()
			delete(t.opening, key)
			t.mu.Unlock()
		}
		return msg
	}
}

// reopenable reports whether local folder dir can be reopened: a folder
// this user can list. A variable for the tests.
var reopenable = func(dir string) bool {
	f, err := os.Open(dir) //nolint:gosec // a folder the user had open in a tab
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if fi, serr := f.Stat(); serr != nil || !fi.IsDir() {
		return false
	}
	_, err = f.Readdirnames(1)
	return err == nil || errors.Is(err, io.EOF)
}

// openRestoredFolder opens restored folder dir, workspace key, as the
// history panel opens a folder not seen yet in this run; a folder gone or
// unreadable closes its tab. The check runs in the background: a stat of
// a stale network mount can hang.
func (t *Palette) openRestoredFolder(key, dir string) tea.Cmd {
	name := filepath.Base(dir)
	return tea.Batch(flash("opening "+name+"…"), t.settled(key, func() tea.Msg {
		if !t.isRestored(key) {
			return nil // closed, or opened another way, meanwhile
		}
		if !t3client.IsRemoteDir(dir) && !reopenable(dir) { // a machine's folder fails on opening instead
			return t.dropRestored(key, name+": the folder is gone or cannot be read; its tab is closed")
		}
		t.mu.Lock()
		t.rememberDirLocked(dir)
		t.mu.Unlock()
		return t.folderMsg(key, dir, false) // a saved tab comes back without terminals
	}))
}

// openRestoredProject opens restored project tab, workspace key, over its
// server's connection, as openProject opens a project: made now when there
// is none, which the panels show as the connections panel's connect does.
// A connection that fails leaves the tab, to be tried again; a server spec
// aidiff can no longer read closes it.
func (t *Palette) openRestoredProject(key string, tab Tab) tea.Cmd {
	title := cmp.Or(tab.ProjectTitle, tab.ProjectID)
	if _, err := t3client.ServerLabel(tab.Server); err != nil {
		return func() tea.Msg {
			return t.dropRestored(key, "T3: "+title+": "+err.Error()+"; its tab is closed")
		}
	}
	t.mu.Lock()
	open := t.serverConnLocked(tab.Server)
	t.mu.Unlock()
	if open != nil {
		return asking("opening "+title, func(ctx context.Context) tea.Msg {
			return t.settled(key, func() tea.Msg {
				sh, err := t.fetchShell(ctx, open)
				if err != nil {
					return t3Error("open "+title, err)
				}
				return t.restoredOn(open, sh, key, tab)
			})()
		})
	}
	return tea.Batch(flash("T3: connecting to "+tab.Server+"…"), t.settled(key, func() tea.Msg {
		s, sh, dialed, err := t.tabConnection(tab.Server)
		if err != nil {
			return t3Error("open "+title, err)
		}
		msg := t.restoredOn(s, sh, key, tab)
		if dialed {
			return withNotices(s, msg)
		}
		return msg
	}))
}

// tabConnection is the connection to server spec for a restored tab, with
// the server's listing: the open one, else one made now. The restored tabs
// of that server shown meanwhile wait for it rather than dial again, so
// they share it. A connection made here is kept as every connection the
// user makes is (adoptTab); dialed reports that this call made it.
func (t *Palette) tabConnection(spec string) (s Session, sh t3client.Shell, dialed bool, err error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, t3client.Shell{}, false, errClosing
	}
	if open := t.serverConnLocked(spec); open != nil {
		t.mu.Unlock()
		ctx, cancel := context.WithTimeout(t.ctx, callTimeout)
		defer cancel()
		sh, err = t.fetchShell(ctx, open)
		return open, sh, false, err
	}
	if d := t.tabDials[spec]; d != nil {
		t.mu.Unlock()
		select {
		case <-d.done:
			return d.s, d.sh, false, d.err
		case <-t.ctx.Done():
			return nil, t3client.Shell{}, false, errClosing
		}
	}
	choice := t.chooseLocked()
	t.dialed = dialing{spec: spec, choice: choice, since: time.Now()}
	t.dialTickLocked(choice)
	d := &tabDial{done: make(chan struct{})}
	t.tabDials[spec] = d
	t.mu.Unlock()

	ctx, cancel := context.WithTimeout(t.ctx, ConnectTimeout)
	defer cancel()
	s, sh, err = reach(ctx, func(ctx context.Context) (Session, error) {
		return t.dial(ctx, spec)
	})
	switch {
	case err != nil:
		t.dialFailed(choice, err)
	case !t.adoptTab(s, sh, choice):
		s, err = nil, errClosing
	}
	t.mu.Lock()
	delete(t.tabDials, spec)
	t.mu.Unlock()
	d.s, d.sh, d.err = s, sh, err
	close(d.done)
	return s, sh, true, err
}

// adoptTab keeps s, a connection made for a restored tab, as its server's,
// open for the run as every connection the user makes, and makes it the
// live one with sh as its listing, unless the user chose another
// connection meanwhile: a restored tab's dial is a choice, as a connect in
// the connections panel is. What came over a dropped connection to the
// same server moves to s (repointLocked). It reports false, closing s,
// once the palette is closed.
func (t *Palette) adoptTab(s Session, sh t3client.Shell, choice uint64) bool {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = s.Close()
		return false
	}
	defer t.mu.Unlock()
	var dropped Session
	for c := range t.connected {
		if c != s && c.Label() == s.Label() && ended(c) {
			dropped = c
		}
	}
	if choice == t.chosen {
		t.dialed = dialing{}
		t.adoptLocked(s, sh)
	} else {
		t.held[s] = true
		t.connectLocked(s)
	}
	if dropped != nil {
		t.repointLocked(dropped, s, sh)
	}
	t.closeLaterLocked(t.takeUnusedLocked())
	return true
}

// restoredOn opens restored project tab, workspace key, over s, its
// server's connection, whose listing is sh: the thread it showed, else the
// project's most recently updated thread, else its root, with the thread's
// chat as opening a project brings it. A project gone from the server
// closes the tab.
func (t *Palette) restoredOn(s Session, sh t3client.Shell, key string, tab Tab) tea.Msg {
	if !t.isRestored(key) {
		return nil // closed, or opened another way, meanwhile
	}
	title := cmp.Or(tab.ProjectTitle, tab.ProjectID)
	p, ok := sh.Project(tab.ProjectID)
	if !ok || p.Archived() {
		return t.dropRestored(key, "T3: "+title+" is no longer a project on "+tab.Server+"; its tab is closed")
	}
	var msg tea.Msg
	threads := projectThreads(sh, &p)
	switch {
	case tab.ThreadID != "" && slices.ContainsFunc(sh.Threads, func(th t3client.Thread) bool {
		return th.ID == tab.ThreadID && th.ProjectID == p.ID
	}):
		msg = t.openMsg(s, sh, tab.ThreadID)
	case len(threads) == 0:
		r := t.newReview(s, t3client.Target{Cwd: p.WorkspaceRoot, Project: p})
		msg = t.workspaceMsg(r, t.storeFor(r), "open "+p.Title, false)
	default:
		msg = t.openMsg(s, sh, threads[0].ID)
	}
	return msg // no terminal, even with --terminal: terminals are not part of a saved tab
}

// dropRestored forgets restored tab key, which cannot come back, and closes
// its tab, saying why in text; nothing when it was opened or closed
// meanwhile. When it was on screen and no tab is left, in a run that
// started on a saved tab, the working directory opens in its place
// (StartTab.Fallback), as such a run opens it without saved tabs.
func (t *Palette) dropRestored(key, text string) tea.Msg {
	t.mu.Lock()
	_, ok := t.restored[key]
	delete(t.restored, key)
	fallback := ""
	if st := t.cfg.StartTab; ok && st != nil && key == t.ws && t.aloneLocked() {
		fallback = st.Fallback
	}
	if fallback != "" {
		t.rememberDirLocked(fallback)
	}
	t.mu.Unlock()
	drop := func() tea.Msg { return splitview.DropTabMsg{Key: key} }
	switch {
	case !ok:
		return nil
	case fallback == "":
		return tea.BatchMsg{drop, flash(text)}
	}
	// in order: the drop leaves the tab's stand-in to close once left, then
	// the folder takes the screen, then the note, which a switch would clear
	return tea.Sequence(drop, func() tea.Msg {
		return t.folderMsg(localKey(fallback), fallback, true) // as a run opens the working directory
	}, flash(text))()
}

// aloneLocked reports whether the palette knows no workspace but the one
// on screen: none parked, no restored tab left. mu must be held.
func (t *Palette) aloneLocked() bool {
	return len(t.restored) == 0 && len(t.parked) == 0 && len(t.parkedLocal) == 0
}

// pruneTabsLocked closes, in the background, the restored tabs of folders
// gone from disk, with a note: a stat of a stale network mount can hang,
// so it never holds up the start. mu must be held, with the program set.
func (t *Palette) pruneTabsLocked() {
	dirs := map[string]string{}
	for key, tab := range t.restored {
		if tab.Dir != "" {
			dirs[key] = tab.Dir
		}
	}
	p := t.program
	if len(dirs) == 0 || p == nil {
		return
	}
	order := slices.Clone(t.tabs)
	t.loops.Go(func() {
		var gone []string
		for _, key := range order {
			dir, ok := dirs[key]
			if !ok {
				continue
			}
			if t.ctx.Err() != nil {
				return
			}
			if !t3client.IsRemoteDir(dir) && !isFolder(dir) { // a machine's folder fails on opening instead
				gone = append(gone, key)
			}
		}
		var drop, names []string
		t.mu.Lock()
		for _, key := range gone {
			if tab, ok := t.restored[key]; ok { // else opened or closed meanwhile
				drop, names = append(drop, key), append(names, filepath.Base(tab.Dir))
				delete(t.restored, key)
			}
		}
		t.mu.Unlock()
		if len(drop) == 0 {
			return
		}
		for _, key := range drop {
			p.Send(splitview.DropTabMsg{Key: key})
		}
		text := "closed the tab of " + names[0] + ": its folder is gone"
		if len(names) > 1 {
			text = fmt.Sprintf("closed %d tabs whose folders are gone: %s", len(names), strings.Join(names, ", "))
		}
		p.Send(splitview.FlashMsg{Text: text})
	})
}
