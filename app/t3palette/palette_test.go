package t3palette

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

const (
	loginID = "abcdef123456"
	olderID = "older"
)

// testShell is a server with one project and two threads.
func testShell() t3client.Shell {
	branch := "main"
	return t3client.Shell{
		Projects: []t3client.Project{{ID: "p1", Title: "Sabra2", WorkspaceRoot: "/srv/sabra2"}},
		Threads: []t3client.Thread{
			{ID: olderID, ProjectID: "p1", Title: "Older work", UpdatedAt: "2026-11-14T12:00:00Z"},
			{ID: loginID, ProjectID: "p1", Title: "Fix login", Branch: &branch, UpdatedAt: "2026-11-15T12:00:00Z"},
		},
	}
}

// threadReview is the review of threadID over s, as a run opens it.
func threadReview(t *testing.T, s Session, threadID string) *Review {
	t.Helper()
	target, err := t3client.Resolve(testShell(), threadID)
	require.NoError(t, err)
	return &Review{Session: s, Target: target}
}

// annotatedModel is a review model holding one annotation.
func annotatedModel() fakeModel {
	store := annotation.NewStore()
	store.Add(annotation.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "check this"})
	return fakeModel{store: store}
}

// connect runs the palette's connect to spec, which the harness dials to s.
func (h *harness) connect(t *testing.T, spec string, s *fakeSession) []tea.Msg {
	t.Helper()
	h.dials[spec] = s
	return run(h.p.connect(spec))
}

// openThread runs the palette row opening threadID on s and returns the
// message that puts its review on screen.
func (h *harness) openThread(t *testing.T, s Session, threadID string) splitview.ReplaceInnerMsg {
	t.Helper()
	return find[splitview.ReplaceInnerMsg](t, run(h.p.openThread(s, threadID)(nil)))
}

func commandNames(cmds []splitview.Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Name
	}
	return out
}

func TestPalette_CommandsFollowState(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	assert.Equal(t, []string{"T3: Connect to server…"}, commandNames(h.p.Commands()))

	box := newFakeSession("ssh://boxdev", testShell())
	msgs := h.connect(t, "ssh://boxdev", box)
	picker := find[splitview.ShowPickerMsg](t, msgs)
	assert.Equal(t, "New project…", picker.Items[0].Label, "connecting leads with a new project")
	assert.Equal(t, "Sabra2", picker.Items[1].Label)
	assert.Contains(t, picker.Items[1].Detail, "2 threads")
	assert.Equal(t, []string{"T3: New thread…", "T3: Projects…", "T3: Fix login", "T3: Older work",
		"T3: Reconnect", "T3: Connect to another server…"},
		commandNames(h.p.Commands()), "connected: the server's threads, newest first; the local review stays on screen")
	assert.Equal(t, "ssh://boxdev", h.p.Commands()[0].Detail)
	assert.Nil(t, h.p.OnScreen())

	swap(h.openThread(t, box, loginID), annotatedModel())
	cmds := h.p.Commands()
	assert.Equal(t, []string{"T3: New thread…", "T3: Projects…", "T3: ▸ Fix login", "T3: Older work",
		"T3: Diff…", "T3: Send annotations to agent", "T3: Reconnect", "T3: Connect to another server…"},
		commandNames(cmds), "the thread on screen is marked, can change its diff and take the annotations")
	assert.Equal(t, "Sabra2", cmds[0].Detail, "new threads start in the reviewed project")
	assert.Equal(t, "Sabra2 · idle · main", cmds[2].Detail)
	assert.Equal(t, "Fix login", cmds[5].Detail)

	other := t3client.Project{ID: "p2", Title: "Other"}
	h.p.selectProject(box, other)
	assert.Equal(t, "Other", h.p.Commands()[0].Detail, "new threads start in the chosen project")
	assert.NotContains(t, commandNames(h.p.Commands()), "T3: Older work", "and only its threads are listed")

	threads, ok := h.p.threadList(box, testShell(), nil).(splitview.ShowPickerMsg)
	require.True(t, ok)
	assert.Equal(t, []string{"New thread…", "▸ Fix login", "Older work"},
		[]string{threads.Items[0].Label, threads.Items[1].Label, threads.Items[2].Label})
}

func TestPalette_ConnectWhileOpeningKeepsTheOpeningSession(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, olderID))

	opening := h.openThread(t, a, loginID) // built, not yet on screen
	b := newFakeSession("ssh://b", testShell())
	h.connect(t, "ssh://b", b) // lands before the split swaps the models
	h.settle()
	assert.Zero(t, a.closes(), "a is on screen and under the opening review")

	swap(opening, fakeModel{store: annotation.NewStore()})
	h.settle()
	assert.Same(t, a, h.p.OnScreen().Session, "the review opened shows")
	assert.Zero(t, a.closes(), "and its session stays open while it is on screen")
	assert.Zero(t, b.closes(), "b is live")

	c := newFakeSession("ssh://c", testShell())
	h.connect(t, "ssh://c", c)
	h.settle()
	assert.Equal(t, 1, b.closes(), "b is neither live nor on screen any more")
	assert.Zero(t, a.closes())

	swap(h.openThread(t, c, loginID), fakeModel{store: annotation.NewStore()})
	h.settle()
	assert.Equal(t, 1, a.closes(), "a left the screen")
	assert.Zero(t, c.closes())

	h.p.Close()
	assert.Equal(t, 1, c.closes())
	assert.Equal(t, 1, a.closes())
	assert.Equal(t, 1, b.closes())
}

func TestPalette_CloseEndsLateSessions(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, olderID))
	opening := h.openThread(t, a, loginID)
	b := newFakeSession("ssh://b", testShell())
	h.connect(t, "ssh://b", b)

	h.p.Close()
	assert.Equal(t, 1, a.closes(), "the session of a review that never reached the screen is closed too")
	assert.Equal(t, 1, b.closes())

	swap(opening, fakeModel{store: annotation.NewStore()}) // a Retire after the end changes nothing
	late := newFakeSession("ssh://late", testShell())
	msgs := h.connect(t, "ssh://late", late)
	assert.Nil(t, msgs[len(msgs)-1], "a connection landing after Close reports nothing")
	h.settle()
	assert.Equal(t, 1, a.closes())
	assert.Equal(t, 1, late.closes(), "and is closed, since nobody else will")
	assert.Nil(t, h.p.liveSession())
}

func TestPalette_ReconnectRebuildsTheReviewOverTheNewSession(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	a2 := newFakeSession("ssh://a", testShell())
	a.redial = func() (Session, error) {
		return a2, nil
	}
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	inner := annotatedModel()

	msgs := run(h.p.reconnect(inner))
	rebuilt := find[splitview.ReplaceInnerMsg](t, msgs)
	model, ok := rebuilt.Model.(fakeModel)
	require.True(t, ok)
	assert.Same(t, inner.store, model.store, "annotations are kept")
	assert.Same(t, a2, model.review.Session)
	assert.Equal(t, loginID, model.review.Target.Thread.ID)
	h.settle()
	assert.Zero(t, a.closes(), "the old session serves the review on screen until it is replaced")

	swap(rebuilt, inner)
	h.settle()
	assert.Equal(t, 1, a.closes())
	assert.Same(t, a2, h.p.liveSession())
	assert.Same(t, a2, h.p.OnScreen().Session)
	assert.Empty(t, h.history(), "the annotations moved to the new review, not to history")
}

func TestPalette_ReconnectFailureKeepsTheSession(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	start := h.p.OnScreen()

	msgs := run(h.p.reconnect(annotatedModel()))
	assert.Contains(t, flashes(msgs), "T3: reconnect: server unreachable")
	h.settle()
	assert.Same(t, start, h.p.OnScreen())
	assert.Same(t, a, h.p.liveSession())
	assert.Zero(t, a.closes())
}

func TestPalette_ReconnectYieldsToAConnectionMadeMeanwhile(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	a := newFakeSession("ssh://a", testShell())
	h.connect(t, "ssh://a", a)
	a2 := newFakeSession("ssh://a", testShell())
	b := newFakeSession("ssh://b", testShell())
	a.redial = func() (Session, error) {
		h.connect(t, "ssh://b", b) // the user connected elsewhere while the redial ran
		return a2, nil
	}

	msgs := run(h.p.reconnect(nil))
	assert.Contains(t, flashes(msgs), "T3: the connection changed meanwhile; reconnect canceled")
	h.settle()
	assert.Same(t, b, h.p.liveSession())
	assert.Equal(t, 1, a2.closes(), "the reconnect closes the session it made")
	assert.Equal(t, 1, a.closes(), "a was replaced by b")
	assert.Zero(t, b.closes())
}

func TestPalette_NoticesFollowTheConnection(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	s := newFakeSession("http://box:3000", testShell())
	s.notices = []string{"the T3 token travels unencrypted"}
	msgs := h.connect(t, "http://box:3000", s)
	find[splitview.ShowPickerMsg](t, msgs)
	assert.Equal(t, "T3: the T3 token travels unencrypted", flashes(msgs)[len(flashes(msgs))-1])
}

func TestPalette_NoWatchHoldsForEveryThread(t *testing.T) {
	for _, noWatch := range []bool{false, true} {
		a := newFakeSession("ssh://a", testShell())
		h := newHarness(t, Config{NoWatch: noWatch, NoChat: true}, threadReview(t, a, loginID))
		swap(h.openThread(t, a, olderID), fakeModel{store: annotation.NewStore()})
		h.p.Close() // waits for the watches to end
		if noWatch {
			assert.Empty(t, a.server.watched(), "--no-watch holds after a thread switch")
			continue
		}
		assert.ElementsMatch(t, []string{loginID, olderID}, a.server.watched(), "each thread on screen is watched")
	}
}

func TestPalette_ShellCacheIsTheLiveServers(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	a := newFakeSession("ssh://a", testShell())
	h.connect(t, "ssh://a", a)
	bShell := t3client.Shell{Projects: []t3client.Project{{ID: "pb", Title: "Bee"}}}
	b := newFakeSession("ssh://b", bShell)
	h.connect(t, "ssh://b", b)
	assert.Equal(t, bShell, h.p.state().shell)

	_, err := h.p.fetchShell(context.Background(), a)
	require.NoError(t, err)
	assert.Equal(t, bShell, h.p.state().shell, "a listing from another server never replaces the live one's")

	bShell.Projects = append(bShell.Projects, t3client.Project{ID: "pb2", Title: "Bee two"})
	b.server.shell = bShell
	_, err = h.p.fetchShell(context.Background(), b)
	require.NoError(t, err)
	assert.Equal(t, bShell, h.p.state().shell)

	h.p.selectProject(a, t3client.Project{ID: "p1"})
	assert.Nil(t, h.p.state().project, "a project of another server is not selected")
	h.p.selectProject(b, bShell.Projects[0])
	h.connect(t, "ssh://b", newFakeSession("ssh://b", bShell))
	assert.NotNil(t, h.p.state().project, "connecting again to the same server keeps its project")
	h.connect(t, "ssh://c", newFakeSession("ssh://c", testShell()))
	assert.Nil(t, h.p.state().project, "another server starts with none")
}

func TestPalette_RetiredAnnotationsGoToHistory(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	a := newFakeSession("ssh://a", testShell())
	h.connect(t, "ssh://a", a)

	first := swap(h.openThread(t, a, loginID), annotatedModel())
	h.settle()
	require.Len(t, h.history(), 1)
	assert.Nil(t, h.history()[0].review, "the run's local review is filed as itself")
	assert.Contains(t, h.history()[0].annotations, "check this")

	firstReview := h.p.OnScreen()
	m, ok := first.(fakeModel)
	require.True(t, ok)
	m.store.Add(annotation.Annotation{File: "b.go", Line: 2, Type: "+", Comment: "and this"})
	swap(h.openThread(t, a, olderID), m)
	h.settle()
	require.Len(t, h.history(), 2)
	assert.Same(t, firstReview, h.history()[1].review, "a thread's annotations are filed under its review")

	swap(h.openThread(t, a, loginID), fakeModel{store: annotation.NewStore()})
	h.settle()
	assert.Len(t, h.history(), 2, "a review without annotations leaves no history")
}

func TestPalette_TerminalPlace(t *testing.T) {
	ssh := newFakeSession("ssh://box", testShell())
	ssh.sshHost, ssh.control = "box", "/tmp/aidiff-t3-1/ssh.ctl"
	local := newFakeSession("local", testShell())
	local.local = true
	remote := newFakeSession("https://box.example", testShell())

	assert.Equal(t, Place{}, newHarness(t, Config{NoWatch: true, NoChat: true}, nil).p.TerminalPlace(),
		"no session: a local terminal where terminals start")

	assert.Equal(t, Place{SSHHost: "box", SSHControlPath: "/tmp/aidiff-t3-1/ssh.ctl", Dir: "/srv/sabra2"},
		newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, ssh, loginID)).p.TerminalPlace(),
		"an ssh session opens on its host, over its tunnel's connection, in the reviewed folder")

	assert.Equal(t, Place{Dir: "/srv/sabra2"},
		newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, local, loginID)).p.TerminalPlace(),
		"a local session stays local")

	assert.Equal(t, Place{}, newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, remote, loginID)).p.TerminalPlace(),
		"a remote server's folder is not this machine's")

	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	h.connect(t, "ssh://box", ssh)
	h.p.selectProject(ssh, t3client.Project{ID: "p1", WorkspaceRoot: "/srv/repo"})
	assert.Equal(t, Place{SSHHost: "box", SSHControlPath: "/tmp/aidiff-t3-1/ssh.ctl", Dir: "/srv/repo"}, h.p.TerminalPlace(),
		"connected without a thread: the live server, in the selected project")
}

func TestPalette_SendAnnotationsNeedsAThread(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	assert.Equal(t, []string{"T3: open a thread first"}, flashes(run(h.p.sendAnnotations(annotatedModel()))))

	a := newFakeSession("ssh://a", testShell())
	h.connect(t, "ssh://a", a)
	swap(h.openThread(t, a, loginID), fakeModel{store: annotation.NewStore()})
	assert.Equal(t, []string{"T3: no annotations to send"}, flashes(run(h.p.sendAnnotations(fakeModel{store: annotation.NewStore()}))))

	msgs := run(h.p.sendAnnotations(annotatedModel()))
	assert.Contains(t, flashes(msgs), "T3: 1 annotation(s) sent to Fix login")
	require.Len(t, a.server.sent, 1)
	assert.Contains(t, a.server.sent[0], "Review annotations from aidiff:")
}

func TestPalette_OpenWithAutoChat(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	h := newHarness(t, Config{NoWatch: true}, nil)
	h.connect(t, "ssh://a", a)
	msgs := run(h.p.openThread(a, loginID)(nil))
	find[splitview.ReplaceInnerMsg](t, msgs)
	find[splitview.OpenPaneMsg](t, msgs)

	h = newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	h.connect(t, "ssh://a", a)
	for _, m := range run(h.p.openThread(a, loginID)(nil)) {
		_, isPane := m.(splitview.OpenPaneMsg)
		assert.False(t, isPane, "--no-chat opens no chat pane")
	}
}

func TestPalette_OpenOnAClosedSessionIsRefused(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	a := newFakeSession("ssh://a", testShell())
	h.connect(t, "ssh://a", a)
	h.connect(t, "ssh://b", newFakeSession("ssh://b", testShell()))
	h.settle()
	require.Equal(t, 1, a.closes())

	msgs := run(h.p.openThread(a, loginID)(nil))
	assert.Contains(t, flashes(msgs), "T3: open thread: the connection was closed meanwhile")
	assert.Nil(t, h.p.OnScreen())
}
