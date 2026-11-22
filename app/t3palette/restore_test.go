package t3palette

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// tabsFake is a tabs file in memory: what Load returns, and what was saved.
type tabsFake struct {
	mu      sync.Mutex
	tabs    []Tab
	current int
	loads   int
	saves   [][]Tab
	cur     []int
}

func (f *tabsFake) config() TabsConfig {
	return TabsConfig{
		Load: func() ([]Tab, int) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.loads++
			return f.tabs, f.current
		},
		Save: func(tabs []Tab, current int) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.saves = append(f.saves, tabs)
			f.cur = append(f.cur, current)
			return nil
		},
	}
}

// folders makes the folders names under a temporary root and returns
// their paths.
func folders(t *testing.T, names ...string) []string {
	t.Helper()
	root := t.TempDir()
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(root, n)
		require.NoError(t, os.MkdirAll(out[i], 0o750))
	}
	return out
}

// gatedDial connects to the sessions in servers, once gate is closed, and
// counts the dials.
type gatedDial struct {
	gate    chan struct{}
	servers map[string]*fakeSession
	fail    atomic.Bool // the server does not answer
	n       atomic.Int32
}

func newGatedDial(servers ...*fakeSession) *gatedDial {
	g := &gatedDial{gate: make(chan struct{}), servers: map[string]*fakeSession{}}
	for _, s := range servers {
		g.servers[s.label] = s
	}
	return g
}

func (g *gatedDial) dial(ctx context.Context, spec string) (Session, error) {
	g.n.Add(1)
	select {
	case <-g.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s, ok := g.servers[spec]
	if !ok || g.fail.Load() {
		return nil, errors.New("connect: " + spec + " does not answer")
	}
	return s, nil
}

// peek is what the tab strip shows of workspace key.
func (h *harness) peek(key string) splitview.SessionInfo {
	return h.p.Sessions().(sessionList).Peek(key)
}

func TestRestore_TabsComeBackAroundTheRunsOwnWorkspace(t *testing.T) {
	dirs := folders(t, "alpha", "beta", "gamma", "delta")
	a, b, c, d := dirs[0], dirs[1], dirs[2], dirs[3]
	saved := []Tab{
		{Dir: b},
		{Server: "ssh://dev/", Machine: "dev", ProjectID: "p1", ProjectTitle: "Sabra2", ThreadID: olderID},
		{Dir: a},
		{Dir: c},
		{Dir: b}, // twice: one tab
		{},       // nothing to reopen
	}
	tabs := &tabsFake{tabs: saved, current: 1}
	f := &localFake{}
	h := newHarness(t, Config{NoChat: true, NoWatch: true, Local: f.config(a), Tabs: tabs.config()}, nil)
	order, recent := h.p.StartTabs()
	devKey := t3Key("ssh://dev", "p1")
	assert.Equal(t, []string{localKey(b), devKey, localKey(a), localKey(c)}, order,
		"in the saved order, the run's own folder in its place; the server named as a session to it names it")
	assert.Equal(t, []string{devKey}, recent, "the tab the last run had on screen, for the switcher")
	assert.Equal(t, localKey(a), h.p.Workspace(), "the run's own folder is on screen")
	assert.Equal(t, 1, tabs.loads)
	assert.Empty(t, f.opened, "nothing is opened at the start")

	h = newHarness(t, Config{NoChat: true, NoWatch: true, Local: (&localFake{}).config(d), Tabs: tabs.config()}, nil)
	order, _ = h.p.StartTabs()
	assert.Equal(t, []string{localKey(b), devKey, localKey(a), localKey(c), localKey(d)}, order,
		"a folder not among them is appended")

	dev := newFakeSession("ssh://dev", twoProjectShell())
	h = newHarness(t, Config{NoChat: true, NoWatch: true, Tabs: tabs.config()}, threadReview(t, dev, loginID))
	order, recent = h.p.StartTabs()
	assert.Equal(t, []string{localKey(b), devKey, localKey(a), localKey(c)}, order, "a --t3 project keeps its saved place")
	assert.Empty(t, recent, "the one on screen is the run's own")
	assert.Equal(t, devKey, h.p.Workspace())
	assert.Equal(t, loginID, h.p.OnScreen().Target.Thread.ID, "on the thread the run asked for")
}

func TestRestore_NothingFromARunOfNoFolder(t *testing.T) {
	tabs := &tabsFake{tabs: []Tab{{Dir: "/x"}, {Dir: "/y"}}, current: 0}
	h := newHarness(t, Config{NoChat: true, Local: (&localFake{}).config(""), Tabs: tabs.config()}, nil)
	order, recent := h.p.StartTabs()
	assert.Empty(t, order, "stdin or a compare pair brings no tabs back")
	assert.Empty(t, recent)
	assert.Zero(t, tabs.loads, "the file is not even read")
	require.NoError(t, h.p.SaveTabs([]string{localKey("/z")}, localKey("/z")))
	assert.Empty(t, tabs.saves, "and its tabs replace none")
}

func TestRestore_DescribedFromWhatWasSaved(t *testing.T) {
	dirs := folders(t, "alpha", "beta")
	a, b := dirs[0], dirs[1]
	tabs := &tabsFake{tabs: []Tab{
		{Dir: b},
		{Server: "ssh://dev", Machine: "devbox", ProjectID: "p1", ProjectTitle: "Sabra2"},
		{Server: "ssh://dev", ProjectID: "p9"},
	}, current: -1}
	h := newHarness(t, Config{NoChat: true, Local: (&localFake{}).config(a), Tabs: tabs.config()}, nil)
	assert.Equal(t, splitview.SessionInfo{Name: "beta", Machine: "local", Path: homeRelative(filepath.Dir(b))}, h.peek(localKey(b)),
		"a folder as an opened one")
	assert.Equal(t, splitview.SessionInfo{Name: "Sabra2", Machine: "devbox T3", Note: "not connected"}, h.peek(t3Key("ssh://dev", "p1")),
		"a project by its title and machine, saying its server is not connected to")
	assert.Equal(t, splitview.SessionInfo{Name: "p9", Machine: "ssh://dev T3", Note: "not connected"}, h.peek(t3Key("ssh://dev", "p9")),
		"with no title and machine saved, the id and the server")
	_, ready := h.p.Sessions().Show(localKey(b))
	assert.False(t, ready, "never shown as parked: it opens")
}

func TestRestore_FolderIsBuiltOnlyWhenShown(t *testing.T) {
	dirs := folders(t, "alpha", "beta")
	a, b := dirs[0], dirs[1]
	tabs := &tabsFake{tabs: []Tab{{Dir: a}, {Dir: b}}, current: -1}
	f := &localFake{}
	h := newHarness(t, Config{Local: f.config(a), Tabs: tabs.config()}, nil)
	h.settle()
	assert.Empty(t, f.opened, "not built at the start")

	msgs := run(h.p.Sessions().Open(localKey(b)))
	assert.Equal(t, []string{b}, f.opened, "built once shown")
	assert.Contains(t, flashes(msgs), "opening beta…")
	show := find[splitview.SwitchWorkspaceMsg](t, msgs)
	assert.Equal(t, localKey(b), show.Key, "in its own workspace, which its tab stands for")
	assert.NotNil(t, show.Model)
	for _, m := range msgs {
		_, isTerm := m.(splitview.OpenTerminalMsg)
		assert.False(t, isTerm, "no terminal: a saved tab comes back without one, and the build may finish under another tab")
	}
	assert.Eventually(t, func() bool { return f.wasRemembered(b) }, time.Second, time.Millisecond, "recorded as opened now")

	h.swap(show, annotatedModel())
	h.settle()
	assert.Equal(t, b, h.p.LocalDir())
	assert.False(t, h.p.isRestored(localKey(b)), "opened: no longer restored")
	assert.Equal(t, 1, f.watches(b), "refreshed live")
}

func TestRestore_FolderGoneIsDropped(t *testing.T) {
	dirs := folders(t, "alpha", "beta")
	a, b := dirs[0], dirs[1]
	gone := filepath.Join(filepath.Dir(a), "gone")
	tabs := &tabsFake{tabs: []Tab{{Dir: gone}, {Dir: a}, {Dir: b}}, current: -1}
	f := &localFake{}
	h := newHarness(t, Config{NoWatch: true, Local: f.config(a), Tabs: tabs.config()}, nil)
	require.Eventually(t, func() bool { return slices.Contains(h.sent(), tea.Msg(splitview.DropTabMsg{Key: localKey(gone)})) },
		time.Second, time.Millisecond, "a folder gone from disk closes its tab at the start, in the background")
	assert.Contains(t, flashes(h.sent()), "closed the tab of gone: its folder is gone")
	assert.False(t, h.p.isRestored(localKey(gone)))
	assert.True(t, h.p.isRestored(localKey(b)), "the others stay")

	old := reopenable
	reopenable = func(string) bool { return false } // gone, or unreadable, since the start
	t.Cleanup(func() { reopenable = old })
	msgs := run(h.p.Sessions().Open(localKey(b)))
	assert.Contains(t, msgs, tea.Msg(splitview.DropTabMsg{Key: localKey(b)}), "shown, it closes")
	assert.Contains(t, flashes(msgs), "beta: the folder is gone or cannot be read; its tab is closed")
	assert.Empty(t, f.opened, "nothing is built")
	assert.False(t, h.p.isRestored(localKey(b)))
}

func TestRestore_ProjectConnectsOnlyWhenShownSharingOneConnection(t *testing.T) {
	a := folders(t, "alpha")[0]
	dev := newFakeSession("ssh://dev", twoProjectShell())
	g := newGatedDial(dev)
	tabs := &tabsFake{tabs: []Tab{
		{Server: "ssh://dev", Machine: "dev", ProjectID: "p1", ProjectTitle: "Sabra2", ThreadID: olderID},
		{Dir: a},
		{Server: "ssh://dev", Machine: "dev", ProjectID: "p3", ProjectTitle: "Zed"},
		{Server: "ssh://dev", Machine: "dev", ProjectID: "p2", ProjectTitle: "Alpha", ThreadID: "deleted"},
	}, current: -1}
	h := newHarnessDial(t, Config{NoWatch: true, Local: (&localFake{}).config(a), Tabs: tabs.config()}, nil, g.dial)
	k1, k3, k2 := t3Key("ssh://dev", "p1"), t3Key("ssh://dev", "p3"), t3Key("ssh://dev", "p2")
	h.settle()
	assert.Zero(t, g.n.Load(), "no server is reached at the start")
	assert.Nil(t, h.p.liveSession())

	first := make(chan []tea.Msg)
	go func() { first <- run(h.p.Sessions().Open(k1)) }()
	require.Eventually(t, func() bool { return g.n.Load() == 1 }, time.Second, time.Millisecond)
	assert.Equal(t, "connecting…", h.peek(k1).Note, "the tab says it connects")
	assert.Equal(t, "connecting…", h.peek(k3).Note, "and so do the other tabs of that server")
	assert.Equal(t, "ssh://dev", h.p.state().dial.spec, "the panels show the dial, as a connect from the connections panel")
	second := make(chan []tea.Msg)
	go func() { second <- run(h.p.Sessions().Open(k3)) }()

	close(g.gate)
	msgs1, msgs3 := <-first, <-second
	assert.EqualValues(t, 1, g.n.Load(), "two tabs of one server share one connection")
	assert.Same(t, dev, h.p.liveSession(), "the connection made is the selected one, as a connect makes it")
	assert.Empty(t, h.p.state().dial.spec, "the dial is over")

	show1 := find[splitview.SwitchWorkspaceMsg](t, msgs1)
	assert.Equal(t, k1, show1.Key, "in its tab's workspace")
	assert.Equal(t, olderID, shown(show1).review.Target.Thread.ID, "on the thread it showed")
	find[splitview.OpenPaneMsg](t, msgs1) // with its chat, as opening a project brings it
	show3 := find[splitview.SwitchWorkspaceMsg](t, msgs3)
	assert.Equal(t, k3, show3.Key)
	assert.Nil(t, shown(show3).review.Target.Thread, "a project with no thread opens at its root")
	assert.Contains(t, flashes(msgs1), "T3: connecting to ssh://dev…")

	h.swap(show1, annotatedModel())
	h.settle()
	assert.False(t, h.p.isRestored(k1), "opened")
	assert.Equal(t, k1, h.p.Workspace())
	assert.Empty(t, h.peek(k2).Note, "its server is connected now: nothing to say")

	msgs2 := run(h.p.Sessions().Open(k2))
	assert.EqualValues(t, 1, g.n.Load(), "a tab of a connected server opens over its connection")
	assert.Equal(t, "alpha1", shown(find[splitview.SwitchWorkspaceMsg](t, msgs2)).review.Target.Thread.ID,
		"the thread it showed is gone: the project's most recent one")
}

func TestRestore_ConnectFailureKeepsTheTab(t *testing.T) {
	a := folders(t, "alpha")[0]
	dev := newFakeSession("ssh://dev", testShell())
	g := newGatedDial(dev)
	close(g.gate)
	g.fail.Store(true)
	tabs := &tabsFake{tabs: []Tab{{Dir: a}, {Server: "ssh://dev", ProjectID: "p1", ProjectTitle: "Sabra2"}}, current: -1}
	h := newHarnessDial(t, Config{NoWatch: true, NoChat: true, Local: (&localFake{}).config(a), Tabs: tabs.config()}, nil, g.dial)
	k := t3Key("ssh://dev", "p1")

	msgs := run(h.p.Sessions().Open(k))
	assert.Contains(t, flashes(msgs), "T3: open Sabra2: connect: ssh://dev does not answer", "the reason, in a flash")
	assert.NotContains(t, msgs, tea.Msg(splitview.DropTabMsg{Key: k}), "the tab stays")
	assert.True(t, h.p.isRestored(k))
	assert.Equal(t, "not connected", h.peek(k).Note)
	require.Error(t, h.p.state().dial.err, "the panels say the dial failed, as after any connect")

	g.fail.Store(false)
	msgs = run(h.p.Sessions().Open(k))
	assert.Equal(t, k, find[splitview.SwitchWorkspaceMsg](t, msgs).Key, "showing it again tries again")
	assert.EqualValues(t, 2, g.n.Load())
}

func TestRestore_ProjectGoneOrServerUnnamedIsDropped(t *testing.T) {
	a := folders(t, "alpha")[0]
	dev := newFakeSession("ssh://dev", testShell())
	g := newGatedDial(dev)
	close(g.gate)
	tabs := &tabsFake{tabs: []Tab{
		{Dir: a},
		{Server: "ssh://dev", ProjectID: "p9", ProjectTitle: "Removed"},
		{Server: "ftp://old", ProjectID: "p1", ProjectTitle: "Odd"},
	}, current: -1}
	h := newHarnessDial(t, Config{NoWatch: true, NoChat: true, Local: (&localFake{}).config(a), Tabs: tabs.config()}, nil, g.dial)

	gone := t3Key("ssh://dev", "p9")
	msgs := run(h.p.Sessions().Open(gone))
	assert.Contains(t, msgs, tea.Msg(splitview.DropTabMsg{Key: gone}))
	assert.Contains(t, flashes(msgs), "T3: Removed is no longer a project on ssh://dev; its tab is closed")
	assert.False(t, h.p.isRestored(gone))
	assert.Same(t, dev, h.p.liveSession(), "the connection stays: other tabs may need it")

	odd := t3Key("ftp://old", "p1")
	before := g.n.Load()
	msgs = run(h.p.Sessions().Open(odd))
	assert.Contains(t, msgs, tea.Msg(splitview.DropTabMsg{Key: odd}), "a server aidiff cannot name closes its tab")
	assert.Equal(t, before, g.n.Load(), "without dialing")
	require.Len(t, flashes(msgs), 1)
	assert.Contains(t, flashes(msgs)[0], "T3: Odd: unknown T3 server")
}

func TestRestore_RunsOwnServerIsUsed(t *testing.T) {
	b := folders(t, "beta")[0]
	dev := newFakeSession("ssh://dev", twoProjectShell())
	g := newGatedDial()
	tabs := &tabsFake{tabs: []Tab{
		{Dir: b},
		{Server: "ssh://dev", ProjectID: "p1", ProjectTitle: "Sabra2", ThreadID: olderID},
		{Server: "ssh://dev", ProjectID: "p2", ProjectTitle: "Alpha"},
	}, current: 2}
	h := newHarnessDial(t, Config{NoWatch: true, NoChat: true, Tabs: tabs.config()}, threadReview(t, dev, loginID), g.dial)
	k2 := t3Key("ssh://dev", "p2")
	assert.Empty(t, h.peek(k2).Note, "the run's server is connected")

	msgs := run(h.p.Sessions().Open(k2))
	assert.Zero(t, g.n.Load(), "it opens over the run's own connection")
	show := find[splitview.SwitchWorkspaceMsg](t, msgs)
	assert.Equal(t, k2, show.Key)

	// a restored project opened from the palette's thread rows, not its tab
	h = newHarnessDial(t, Config{NoWatch: true, NoChat: true, Tabs: tabs.config()}, threadReview(t, dev, loginID), g.dial)
	show = h.openThread(t, dev, "alpha1")
	assert.Equal(t, k2, show.Key, "the thread's project's tab, the restored one")
	h.swap(show, annotatedModel())
	h.settle()
	assert.False(t, h.p.isRestored(k2), "opened that way, it is no longer restored")
	msgs = run(h.p.Sessions().Open(k2))
	assert.Nil(t, find[splitview.SwitchWorkspaceMsg](t, msgs).Model, "it is a workspace like any other now: nothing is built again")
}

func TestRestore_ClosedBeforeOpened(t *testing.T) {
	dirs := folders(t, "alpha", "beta")
	a, b := dirs[0], dirs[1]
	dev := newFakeSession("ssh://dev", testShell())
	tabs := &tabsFake{tabs: []Tab{{Dir: a}, {Dir: b}, {Server: "ssh://dev", ProjectID: "p1"}}, current: -1}
	f := &localFake{}
	h := newHarness(t, Config{NoWatch: true, NoChat: true, Local: f.config(a), Tabs: tabs.config()}, nil)
	k := t3Key("ssh://dev", "p1")
	assert.Empty(t, h.p.Sessions().CloseNote(k, nil), "nothing in it to lose")
	h.p.Sessions().Closed(k, nil)
	h.p.Sessions().Closed(localKey(b), nil)
	assert.False(t, h.p.isRestored(k))
	assert.Nil(t, h.p.restoredOn(dev, testShell(), k, Tab{Server: "ssh://dev", ProjectID: "p1"}),
		"a connection landing after the close opens nothing")
	for _, msg := range run(h.p.openRestoredFolder(localKey(b), b)) {
		_, opens := msg.(splitview.SwitchWorkspaceMsg)
		assert.False(t, opens, "nor does a folder")
	}
	assert.Empty(t, f.opened)

	require.NoError(t, h.p.SaveTabs([]string{localKey(a)}, localKey(a)))
	require.Len(t, tabs.saves, 1)
	assert.Equal(t, []Tab{{Dir: a}}, tabs.saves[0], "a closed tab is not saved")
}

func TestRestore_SaveTabs(t *testing.T) {
	dirs := folders(t, "alpha", "beta", "gamma")
	a, b, c := dirs[0], dirs[1], dirs[2]
	dev := newFakeSession("ssh://dev", twoProjectShell())
	tabs := &tabsFake{tabs: []Tab{
		{Dir: c},
		{Server: "ssh://dev", Machine: "dev", ProjectID: "p3", ProjectTitle: "Zed"},
		{Dir: a},
	}, current: 0}
	f := &localFake{}
	h := newHarness(t, Config{NoWatch: true, NoChat: true, Local: f.config(a), Tabs: tabs.config()}, nil)
	h.connect(t, "ssh://dev", dev)

	// b from the history, then dev's Sabra2 thread from the palette
	inner := h.swap(find[splitview.SwitchWorkspaceMsg](t, run(h.p.openLocal(b))), annotatedModel())
	inner = h.swap(h.openThread(t, dev, olderID), inner)
	keys := []string{localKey(c), t3Key("ssh://dev", "p3"), localKey(a), localKey(b), t3Key("ssh://dev", "p1"), ""}
	require.NoError(t, h.p.SaveTabs(keys, t3Key("ssh://dev", "p1")))
	require.Len(t, tabs.saves, 1)
	assert.Equal(t, []Tab{
		{Dir: c},
		{Server: "ssh://dev", Machine: "dev", ProjectID: "p3", ProjectTitle: "Zed"},
		{Dir: a},
		{Dir: b},
		{Server: "ssh://dev", Machine: "ssh://dev", ProjectID: "p1", ProjectTitle: "Sabra2", ThreadID: olderID},
	}, tabs.saves[0], "in the tabs' order: restored ones as saved, opened ones as they are; the unnamed workspace is no tab")
	assert.Equal(t, []int{4}, tabs.cur, "the one on screen")
}

func TestRestore_StandInOnScreenOpensInPlace(t *testing.T) {
	dirs := folders(t, "alpha", "beta")
	a, b := dirs[0], dirs[1]
	tabs := &tabsFake{tabs: []Tab{{Dir: a}, {Dir: b}}, current: -1}
	f := &localFake{}
	h := newHarness(t, Config{NoTerminal: true, Local: f.config(a), Tabs: tabs.config()}, nil)
	kb := localKey(b)
	done, ready := h.p.Sessions().Show(kb)
	require.False(t, ready)

	// a's tab closed with only b's left: b's stand-in takes the screen
	done(splitview.WorkspaceSwitch{Left: annotatedModel()})
	h.settle()
	assert.Equal(t, []string{a}, f.saved, "a's annotations are filed as it closes")
	assert.Equal(t, kb, h.p.Workspace())
	assert.Empty(t, h.p.LocalDir(), "nothing the palette follows is on screen yet")

	show := find[splitview.SwitchWorkspaceMsg](t, run(h.p.Sessions().Open(kb)))
	show.Done(splitview.WorkspaceSwitch{Created: true}) // opened in place: nothing left
	h.settle()
	assert.Equal(t, b, h.p.LocalDir())
	assert.Equal(t, 1, f.watches(b))
	assert.False(t, h.p.isRestored(kb))
}

func TestRestore_ServerSpecIsNamedAsASessionNamesIt(t *testing.T) {
	label, err := t3client.ServerLabel("ssh://dev/")
	require.NoError(t, err)
	assert.Equal(t, "ssh://dev", label)
	assert.Equal(t, projectKey(newFakeSession("ssh://dev", t3client.Shell{}), "p1"), (Tab{Server: label, ProjectID: "p1"}).key(),
		"a restored tab's workspace is the one a review over a session to it lands in")
}

// startOn is a palette that starts on saved T3 tab tab (StartTab), the
// others of tabs restored around it, connecting through g.
func startOn(t *testing.T, g *gatedDial, f *localFake, start StartTab, tabs ...Tab) *harness {
	t.Helper()
	saved := &tabsFake{tabs: tabs, current: -1}
	return newHarnessDial(t, Config{NoWatch: true, Local: f.config(""), Tabs: saved.config(), StartTab: &start}, nil, g.dial)
}

func TestRestore_StartTabOpensOnceTheProgramRuns(t *testing.T) {
	a := folders(t, "alpha")[0]
	dev := newFakeSession("ssh://dev", testShell())
	g := newGatedDial(dev)
	sabra := Tab{Server: "ssh://dev/", Machine: "dev", ProjectID: "p1", ProjectTitle: "Sabra2", ThreadID: olderID}
	f := &localFake{}
	h := startOn(t, g, f, StartTab{Tab: sabra, Fallback: a}, Tab{Dir: a}, sabra)
	k := t3Key("ssh://dev", "p1")
	assert.Equal(t, k, h.p.Workspace(), "the saved tab is the workspace on screen")
	assert.True(t, h.p.Opening(), "not opened yet: the split shows it opening")
	order, _ := h.p.StartTabs()
	assert.Equal(t, []string{localKey(a), k}, order, "in its saved place, the folder the run started in not added")
	assert.Equal(t, splitview.SessionInfo{Name: "Sabra2", Machine: "dev T3", Note: "not connected"}, h.peek(k))
	assert.Zero(t, g.n.Load(), "nothing is dialed before the program runs")

	opened := make(chan []tea.Msg)
	go func() { opened <- run(h.p.Sessions().Open(k)) }() // the split's Init
	require.Eventually(t, func() bool { return g.n.Load() == 1 }, time.Second, time.Millisecond)
	assert.Equal(t, "connecting…", h.peek(k).Note, "its stand-in says it connects")
	again := run(h.p.Sessions().Open(k)) // enter on the stand-in meanwhile
	assert.Equal(t, []string{"Sabra2 is opening…"}, flashes(again), "one opening at a time")
	close(g.gate)
	msgs := <-opened
	assert.EqualValues(t, 1, g.n.Load())

	show := find[splitview.SwitchWorkspaceMsg](t, msgs)
	assert.Equal(t, k, show.Key)
	assert.Equal(t, olderID, shown(show).review.Target.Thread.ID, "on the thread it showed")
	var pane, terminal = -1, -1
	for i, m := range msgs {
		switch m.(type) {
		case splitview.OpenPaneMsg:
			pane = i
		case splitview.OpenTerminalMsg:
			terminal = i
		}
	}
	require.GreaterOrEqual(t, pane, 0, "with its chat")
	assert.Equal(t, -1, terminal, "and no terminal: a saved tab comes back without one, --terminal or not")
	show.Done(splitview.WorkspaceSwitch{Created: true}) // opened in place of its stand-in
	h.settle()
	assert.Equal(t, olderID, h.p.OnScreen().Target.Thread.ID)
	assert.False(t, h.p.Opening())
	assert.Empty(t, f.opened, "the working directory is not opened")
}

func TestRestore_StartTabThatCannotConnectStays(t *testing.T) {
	dev := newFakeSession("ssh://dev", testShell())
	g := newGatedDial(dev)
	close(g.gate)
	g.fail.Store(true)
	sabra := Tab{Server: "ssh://dev", Machine: "dev", ProjectID: "p1", ProjectTitle: "Sabra2"}
	f := &localFake{}
	h := startOn(t, g, f, StartTab{Tab: sabra, Fallback: "/work"}, sabra)
	k := t3Key("ssh://dev", "p1")
	msgs := run(h.p.Sessions().Open(k))
	assert.Contains(t, flashes(msgs), "T3: open Sabra2: connect: ssh://dev does not answer")
	assert.True(t, h.p.Opening(), "it stays, still to open")
	assert.Equal(t, "not connected", h.peek(k).Note)
	assert.Empty(t, f.opened, "a server that does not answer is no reason to leave its tab")

	g.fail.Store(false)
	msgs = run(h.p.Sessions().Open(k))
	assert.Equal(t, k, find[splitview.SwitchWorkspaceMsg](t, msgs).Key, "enter, or its tab, tries again")
}

func TestRestore_StartTabGone(t *testing.T) {
	b := folders(t, "beta")[0]
	work := folders(t, "work")[0]
	dev := newFakeSession("ssh://dev", testShell())
	g := newGatedDial(dev)
	close(g.gate)
	removed := Tab{Server: "ssh://dev", ProjectID: "p9", ProjectTitle: "Removed"}

	f := &localFake{}
	h := startOn(t, g, f, StartTab{Tab: removed, Fallback: work}, removed, Tab{Dir: b})
	k := t3Key("ssh://dev", "p9")
	msgs := run(h.p.Sessions().Open(k))
	assert.Contains(t, msgs, tea.Msg(splitview.DropTabMsg{Key: k}), "its tab closes: the split goes to the next tab")
	assert.Empty(t, f.opened, "another tab is left: the working directory is not opened")

	f = &localFake{}
	h = startOn(t, g, f, StartTab{Tab: removed, Fallback: work}, removed)
	msgs = run(h.p.Sessions().Open(k))
	require.NotEmpty(t, msgs)
	assert.Equal(t, splitview.FlashMsg{Text: "T3: connecting to ssh://dev…"}, msgs[0])
	msgs = msgs[1:]
	require.Len(t, msgs, 4, "in order: the drop, the folder, its terminal, the note")
	assert.Equal(t, splitview.DropTabMsg{Key: k}, msgs[0])
	show, ok := msgs[1].(splitview.SwitchWorkspaceMsg)
	require.True(t, ok, "nothing left: the working directory opens, as a run with no saved tabs opens it")
	assert.Equal(t, localKey(work), show.Key)
	assert.Equal(t, splitview.OpenTerminalMsg{}, msgs[2])
	assert.Equal(t, splitview.FlashMsg{Text: "T3: Removed is no longer a project on ssh://dev; its tab is closed"}, msgs[3])
	assert.Equal(t, []string{work}, f.opened)
	assert.Eventually(t, func() bool { return f.wasRemembered(work) }, time.Second, time.Millisecond)
	show.Done(splitview.WorkspaceSwitch{Left: splitview.Model{}, Created: true})
	h.settle()
	assert.Equal(t, work, h.p.LocalDir())
}
