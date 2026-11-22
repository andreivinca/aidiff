package t3palette

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/ui"
)

// sameNameShell is a server with two projects called api, in different
// folders, the first one's agent running.
func sameNameShell() t3client.Shell {
	return t3client.Shell{
		Projects: []t3client.Project{
			{ID: "one", Title: "api", WorkspaceRoot: "/srv/one/api"},
			{ID: "two", Title: "api", WorkspaceRoot: "/srv/two/api"},
		},
		Threads: []t3client.Thread{{ID: "t1", ProjectID: "one", Title: "work", Status: "running", UpdatedAt: "2026-11-15T12:00:00Z"}},
	}
}

// openRoot puts project p of the live session s on screen, its root
// review with no thread, and returns the model on screen.
func (h *harness) openRoot(t *testing.T, s Session, p t3client.Project, inner tea.Model) tea.Model {
	t.Helper()
	r := h.p.newReview(s, t3client.Target{Cwd: p.WorkspaceRoot, Project: p})
	msg, ok := h.p.workspaceMsg(r, h.p.storeFor(r), "open", false).(splitview.SwitchWorkspaceMsg)
	require.True(t, ok)
	return h.swap(msg, inner)
}

func TestSessions_DescribeFoldersAndProjects(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	dir := filepath.Join(home, "Projects", "aidiff")
	h := newHarness(t, Config{NoWatch: true, NoChat: true, Local: (&localFake{}).config(dir)}, nil)
	ss := h.p.Sessions()

	assert.Equal(t, splitview.SessionInfo{Name: "aidiff", Machine: "local", Path: "~/Projects"}, ss.Describe(localKey(dir)),
		"a folder by its name, this machine, and the folder it is in")

	s := newFakeSession("ssh://devbox", sameNameShell())
	h.connect(t, "ssh://devbox", s)
	_, err = h.p.fetchShell(t.Context(), s)
	require.NoError(t, err)
	var inner tea.Model = annotatedModel()
	inner = h.openRoot(t, s, sameNameShell().Projects[0], inner)
	h.openRoot(t, s, sameNameShell().Projects[1], inner)

	one, two := ss.Describe(projectKey(s, "one")), ss.Describe(projectKey(s, "two"))
	assert.Equal(t, splitview.SessionInfo{Name: "api", Machine: "ssh://devbox T3", Path: "/srv/one", Busy: true}, one,
		"a T3 project by its title, its server's machine and its folder; its agent runs")
	assert.Equal(t, splitview.SessionInfo{Name: "api", Machine: "ssh://devbox T3", Path: "/srv/two"}, two,
		"two projects of a name tell apart by their folders")
	assert.Equal(t, "aidiff", ss.Describe(localKey(dir)).Name, "a parked folder is described too")
	assert.Equal(t, splitview.SessionInfo{}, ss.Describe("nowhere"), "a workspace the palette does not know is left to the split")

	h.p.useLocal() // the listing of a server that is not the live one is not at hand
	assert.False(t, ss.Describe(projectKey(s, "one")).Busy)
}

func TestSessions_ProjectTitleUnlikeItsFolder(t *testing.T) {
	sh := t3client.Shell{Projects: []t3client.Project{{ID: "w", Title: "Website", WorkspaceRoot: "/srv/site"}}}
	s := newFakeSession("local", sh)
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	h.connect(t, "local", s)
	h.openRoot(t, s, sh.Projects[0], annotatedModel())
	assert.Equal(t, "/srv/site", h.p.Sessions().Describe(projectKey(s, "w")).Path, "the folder's own name, when the title is another")
}

func TestSessions_NamesAreSanitized(t *testing.T) {
	title := "evil\x1b]52;c;aGk=\x07‮name\x00"
	sh := t3client.Shell{Projects: []t3client.Project{{ID: "e", Title: title, WorkspaceRoot: "/srv/\x1b[31mred"}}}
	s := newFakeSession("ssh://h\x1b[2Jost", sh)
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	h.connect(t, "x", s)
	h.openRoot(t, s, sh.Projects[0], annotatedModel())
	info := h.p.Sessions().Describe(projectKey(s, "e"))
	for _, text := range []string{info.Name, info.Machine, info.Path} {
		assert.False(t, strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) || r == '‮' }),
			"%q holds no control or bidi characters", text)
	}
	assert.True(t, strings.HasPrefix(info.Name, "evil"))
}

func TestSessions_ShowCarriesTheSidebarsDone(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "alpha")
	f := &localFake{}
	h := newHarness(t, Config{NoWatch: true, NoChat: true, Local: f.config(dir)}, nil)
	ss := h.p.Sessions()
	s := newFakeSession("ssh://devbox", sameNameShell())
	h.connect(t, "ssh://devbox", s)
	var inner tea.Model = annotatedModel()
	inner = h.openRoot(t, s, sameNameShell().Projects[0], inner)
	review := h.p.OnScreen()
	require.NotNil(t, review)
	inner = h.openRoot(t, s, sameNameShell().Projects[1], inner)

	key := projectKey(s, "one")
	done, ready := ss.Show(key)
	assert.True(t, ready, "a parked project over a live connection comes back as it was")
	inner = h.swap(splitview.SwitchWorkspaceMsg{Key: key, Done: done}, inner)
	assert.Same(t, review, h.p.OnScreen(), "the palette follows the switch")
	assert.Equal(t, key, h.p.Workspace())

	done, ready = ss.Show(localKey(dir))
	assert.True(t, ready, "a folder always does")
	h.swap(splitview.SwitchWorkspaceMsg{Key: localKey(dir), Done: done}, inner)
	assert.Equal(t, dir, h.p.LocalDir())
	assert.Nil(t, h.p.OnScreen())
	assert.Equal(t, Place{Dir: dir}, h.p.TerminalPlace())
}

func TestSessions_DroppedConnectionReconnectsOnceChosen(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	ss := h.p.Sessions()
	a := newFakeSession("ssh://a", sameNameShell())
	h.connect(t, "ssh://a", a)
	var inner tea.Model = annotatedModel()
	inner = h.openRoot(t, a, sameNameShell().Projects[0], inner)
	b := newFakeSession("ssh://b", twoProjectShell())
	h.connect(t, "ssh://b", b)
	inner = h.openRoot(t, b, twoProjectShell().Projects[0], inner)

	a.drop() // a's link ends while b is the live connection: nobody reconnects it
	key := projectKey(a, "one")
	assert.Equal(t, "disconnected", ss.Describe(key).Note, "the row says so")
	_, ready := ss.Show(key)
	assert.False(t, ready, "it is not shown in passing")

	msgs := run(ss.Open(key))
	require.Len(t, msgs, 2)
	sw, ok := msgs[0].(splitview.SwitchWorkspaceMsg)
	require.True(t, ok, "chosen, it comes on screen")
	assert.Equal(t, ui.MachineIdentityMsg{Name: "ssh://a", T3: true}, msgs[1], "then the status bar names its machine")
	assert.Same(t, a, h.p.liveSession(), "its connection is the selected one, as in the connections panel")
	assert.Eventually(t, func() bool {
		return strings.Contains(strings.Join(flashes(h.sent()), "\n"), "connection to ssh://a lost; reconnecting")
	}, 2*time.Second, time.Millisecond, "which starts the reconnect")
	assert.Equal(t, "reconnecting…", ss.Describe(key).Note)

	h.swap(sw, inner)
	assert.Equal(t, key, h.p.Workspace())
	assert.Equal(t, a, h.p.OnScreen().Session, "over the dropped connection until the reconnect lands and takeOver rebuilds it")
}

func TestSessions_ReplacedConnectionRebuildsOnceChosen(t *testing.T) {
	old := newFakeSession("ssh://a", twoProjectShell())
	fresh := newFakeSession("ssh://a", twoProjectShell())
	old.redial = func() (Session, error) { return fresh, nil }
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, old, loginID))
	ss := h.p.Sessions()
	var inner tea.Model = annotatedModel()
	inner = h.openRoot(t, old, twoProjectShell().Projects[1], inner) // Alpha; Sabra2 parked

	h.reconnect(t, old, inner)
	key := projectKey(old, "p1")
	assert.Equal(t, "reconnected", ss.Describe(key).Note)
	_, ready := ss.Show(key)
	assert.False(t, ready, "its model reads over the old connection: not shown in passing")

	sw := find[splitview.SwitchWorkspaceMsg](t, run(ss.Open(key)))
	require.NotNil(t, sw.Model, "chosen, it is rebuilt over the new connection, as openProject does")
	assert.Same(t, fresh, shown(sw).review.Session)
	assert.Equal(t, loginID, shown(sw).review.thread().ID, "on the thread it showed")
	assert.True(t, sw.Replace)
}

func TestSessions_PassingByFilesAnnotationsOnce(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	ss := h.p.Sessions()
	s := newFakeSession("ssh://devbox", sameNameShell())
	h.connect(t, "ssh://devbox", s)
	var inner tea.Model = fakeModel{store: annotation.NewStore()}
	inner = h.openRoot(t, s, sameNameShell().Projects[0], inner)
	annotated := annotatedModel()
	annotated.review = h.p.OnScreen()
	inner = h.openRoot(t, s, sameNameShell().Projects[1], annotated) // one's review, annotated, leaves
	h.settle()
	require.Len(t, h.history(), 1, "leaving files the annotations as a safety net")

	one, two := projectKey(s, "one"), projectKey(s, "two")
	for range 3 { // the switcher passing back and forth
		done, _ := ss.Show(one)
		inner = h.swap(splitview.SwitchWorkspaceMsg{Key: one, Done: done}, inner)
		done, _ = ss.Show(two)
		inner = h.swap(splitview.SwitchWorkspaceMsg{Key: two, Done: done}, inner)
	}
	h.settle()
	assert.Len(t, h.history(), 1, "unchanged, they are not filed again")

	done, _ := ss.Show(one)
	back := h.swap(splitview.SwitchWorkspaceMsg{Key: one, Done: done}, inner)
	back.(fakeModel).store.Add(annotation.Annotation{File: "b.go", Line: 2, Type: "+", Comment: "and this"})
	done, _ = ss.Show(two)
	h.swap(splitview.SwitchWorkspaceMsg{Key: two, Done: done}, back)
	h.settle()
	require.Len(t, h.history(), 2, "changed, they are")
	assert.Contains(t, h.history()[1].annotations, "and this")
}

// addNote adds an annotation to model's store, as the user would, on a
// line of its own.
func addNote(model tea.Model, comment string) {
	store := model.(fakeModel).store
	store.Add(annotation.Annotation{File: "b.go", Line: store.Count() + 10, Type: "+", Comment: comment})
}

func TestSessions_ClosedTabFilesAndForgetsAProject(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	s := newFakeSession("ssh://a", twoProjectShell())
	h.connect(t, "ssh://a", s)
	_, err := h.p.fetchShell(t.Context(), s)
	require.NoError(t, err)
	ss := h.p.Sessions()
	sabra, alpha := twoProjectShell().Projects[0], twoProjectShell().Projects[1]
	key := projectKey(s, sabra.ID)

	inner := h.openRoot(t, s, sabra, fakeModel{store: annotation.NewStore()})
	addNote(inner, "first")
	sabraModel := inner
	h.openRoot(t, s, alpha, inner) // sabra parked, its note filed as it left
	h.settle()
	require.Len(t, h.history(), 1)
	assert.Empty(t, ss.CloseNote(key, sabraModel), "filed already: closing loses nothing")

	addNote(sabraModel, "second")
	assert.Equal(t, "Its 2 annotations go to history; opening it again starts without them.", ss.CloseNote(key, sabraModel))

	ss.Closed(key, sabraModel)
	delete(h.parked, key) // the split closed it
	h.settle()
	saves := h.history()
	require.Len(t, saves, 2, "the notes not filed yet go to history")
	assert.Equal(t, sabra.ID, saves[1].review.Target.Project.ID, "filed under its project")
	assert.Contains(t, saves[1].annotations, "second")
	h.p.mu.Lock()
	_, parked := h.p.parked[key]
	stored := false
	for _, st := range h.p.stores {
		stored = stored || st == sabraModel.(fakeModel).store
	}
	h.p.mu.Unlock()
	assert.False(t, parked, "the palette drops its record of the workspace")
	assert.False(t, stored, "and the annotations it kept for the run")
	assert.Equal(t, splitview.SessionInfo{}, ss.Describe(key), "the switcher knows it no more")

	again := h.openRoot(t, s, sabra, fakeModel{store: annotation.NewStore()})
	assert.Zero(t, again.(fakeModel).store.Count(), "opened again: a new workspace, with no annotations")
	assert.Equal(t, key, h.p.Workspace())
}

func TestSessions_ClosedTabOnScreenFilesOnce(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	s := newFakeSession("ssh://a", twoProjectShell())
	h.connect(t, "ssh://a", s)
	ss := h.p.Sessions()
	sabra, alpha := twoProjectShell().Projects[0], twoProjectShell().Projects[1]

	inner := h.openRoot(t, s, sabra, fakeModel{store: annotation.NewStore()})
	inner = h.openRoot(t, s, alpha, inner) // alpha on screen, sabra parked
	addNote(inner, "on alpha")
	alphaKey, sabraKey := projectKey(s, alpha.ID), projectKey(s, sabra.ID)
	assert.NotEmpty(t, ss.CloseNote(alphaKey, inner), "alpha's note is not filed yet")

	// the split closes alpha's tab on screen: sabra comes, alpha is not kept
	done, ready := ss.Show(sabraKey)
	require.True(t, ready)
	done(splitview.WorkspaceSwitch{Left: inner, LeftKept: false})
	ss.Closed(alphaKey, inner)
	h.settle()
	require.Len(t, h.history(), 1, "filed once, as it left the screen")
	assert.Equal(t, alpha.ID, h.history()[0].review.Target.Project.ID)
	assert.Equal(t, sabraKey, h.p.Workspace())
	h.p.mu.Lock()
	_, parked := h.p.parked[alphaKey]
	h.p.mu.Unlock()
	assert.False(t, parked, "nor parked")
}

func TestSessions_ClosedTabOfAFolder(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "alpha"), filepath.Join(root, "beta")
	for _, d := range []string{a, b} {
		require.NoError(t, os.MkdirAll(d, 0o750))
	}
	f := &localFake{}
	h := newHarness(t, Config{NoWatch: true, NoChat: true, NoTerminal: true, Local: f.config(a, a, b)}, nil)
	ss := h.p.Sessions()
	aModel := annotatedModel()
	h.swap(pick(t, h.p.Sidebar(), 2), aModel) // beta comes, alpha parked: its note filed
	h.settle()
	require.Equal(t, []string{a}, f.saved)

	addNote(aModel, "more")
	ss.Closed(localKey(a), aModel)
	delete(h.parked, localKey(a))
	h.settle()
	assert.Equal(t, []string{a, a}, f.saved, "what it had not filed goes to history under its folder")
	h.p.mu.Lock()
	_, parked := h.p.parkedLocal[localKey(a)]
	h.p.mu.Unlock()
	assert.False(t, parked, "the folder's workspace is forgotten")

	msg := pick(t, h.p.Sidebar(), 1)
	assert.NotNil(t, msg.Model, "opened again from the history: built anew, a new tab")
	assert.Equal(t, []string{b, a}, f.opened)
}

func TestSessions_PeekFetchesNothing(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	s := newFakeSession("ssh://devbox", sameNameShell())
	h.connect(t, "ssh://devbox", s)
	h.openRoot(t, s, sameNameShell().Projects[0], annotatedModel())
	h.settle()
	h.p.mu.Lock()
	h.p.shellAt = time.Now().Add(-time.Hour) // an old listing
	h.p.mu.Unlock()
	fetched := s.server.fetched()
	ss := h.p.Sessions()
	peek, ok := ss.(interface {
		Peek(string) splitview.SessionInfo
	})
	require.True(t, ok, "the palette lets the tab strip describe without fetching")
	assert.Equal(t, "api", peek.Peek(projectKey(s, "one")).Name)
	h.settle()
	assert.Equal(t, fetched, s.server.fetched(), "Peek fetches nothing")
	ss.Describe(projectKey(s, "one"))
	h.settle()
	assert.Greater(t, s.server.fetched(), fetched, "Describe fetches an old listing again")
}
