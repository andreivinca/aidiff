package t3palette

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// twoProjectShell is testShell with a second project, Alpha, listed first
// by title, with one thread, and a third project with none.
func twoProjectShell() t3client.Shell {
	sh := testShell()
	sh.Projects = append(sh.Projects,
		t3client.Project{ID: "p2", Title: "Alpha", WorkspaceRoot: "/srv/alpha"},
		t3client.Project{ID: "p3", Title: "Zed", WorkspaceRoot: "/srv/zed"})
	sh.Threads = append(sh.Threads, t3client.Thread{ID: "alpha1", ProjectID: "p2", Title: "Alpha work", UpdatedAt: "2026-11-16T12:00:00Z"})
	return sh
}

// pick runs the sidebar's Select(n) and returns the message that puts the
// project on screen.
func pick(t *testing.T, sb splitview.Sidebar, n int) splitview.SwitchWorkspaceMsg {
	t.Helper()
	cmd, hide := sb.Select(n)
	require.True(t, hide, "picking a project hides the sidebar")
	return find[splitview.SwitchWorkspaceMsg](t, run(cmd))
}

// shown is the review a ReplaceInnerMsg puts on screen.
func shown(msg splitview.SwitchWorkspaceMsg) fakeModel {
	m, _ := msg.Model.(fakeModel)
	return m
}

func TestProjects_SwitchingBringsBackTheWorkspace(t *testing.T) {
	a := newFakeSession("ssh://a", twoProjectShell())
	start := threadReview(t, a, loginID)
	startStore := annotatedModel().store
	h := newHarness(t, Config{NoWatch: true, NoChat: true, StartStore: startStore}, start)
	_, err := h.p.fetchShell(t.Context(), a)
	require.NoError(t, err)
	sb := h.p.Sidebar()
	require.True(t, sb.Available())
	require.Equal(t, projectKey(a, "p1"), h.p.Workspace(), "a T3 run starts in its project's workspace")

	view := ansi.Strip(sb.View(30, 16, true))
	assert.Contains(t, view, "3   Zed                      \n\n enter        open project", "the key help sits under the projects")
	assert.NotContains(t, ansi.Strip(sb.View(30, 8, true)), "open project", "a short panel keeps its rows for the projects")
	assert.Contains(t, view, "1   Alpha")
	assert.Contains(t, view, "2 ▸ Sabra2", "projects are numbered by title; the one on screen is marked")
	assert.Contains(t, view, "3   Zed")

	var inner tea.Model = fakeModel{store: startStore, review: start}
	older := h.openThread(t, a, olderID)
	inner = h.swap(older, inner)
	olderReview := h.p.OnScreen()

	alpha := pick(t, sb, 1)
	assert.Equal(t, "alpha1", shown(alpha).review.thread().ID, "a project not seen yet opens its newest thread")
	inner = h.swap(alpha, inner)
	h.settle()
	assert.Zero(t, a.closes())

	back := pick(t, sb, 2)
	assert.Nil(t, back.Model, "a project seen before comes back as its workspace, nothing rebuilt")
	inner = h.swap(back, inner)
	assert.Same(t, olderReview, h.p.OnScreen(), "with the thread it was left on")
	assert.Equal(t, olderID, inner.(fakeModel).review.thread().ID)
	assert.Equal(t, projectKey(a, "p1"), h.p.Workspace())

	cmd, hide := sb.Select(2)
	assert.Nil(t, cmd, "the project on screen stays as it is")
	assert.True(t, hide)

	zed := pick(t, sb, 3)
	assert.Nil(t, shown(zed).review.thread(), "a project with no threads opens its root")
	assert.Equal(t, "/srv/zed", shown(zed).review.Target.Cwd)

	cmd, hide = sb.Select(4)
	assert.False(t, hide)
	assert.Equal(t, []string{"T3: there is no project 4"}, flashes(run(cmd)))
}

func TestProjects_LocalWorkspaceClosesOnFirstSwitch(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	assert.Empty(t, h.p.Workspace(), "the run's local review is the unnamed workspace")
	a := newFakeSession("ssh://a", twoProjectShell())
	h.connect(t, "ssh://a", a)
	sb := h.p.Sidebar()
	local := annotatedModel()
	h.swap(pick(t, sb, 1), local)
	h.settle()
	require.Len(t, h.history(), 1, "the local review's annotations go to history as it closes")
	assert.Nil(t, h.history()[0].review)
	assert.NotContains(t, h.parked, "", "and it is not kept")
}

func TestProjects_ChatsStayInTheirWorkspace(t *testing.T) {
	a := newFakeSession("ssh://a", twoProjectShell())
	h := newHarness(t, Config{NoWatch: true}, threadReview(t, a, loginID))
	_, err := h.p.fetchShell(t.Context(), a)
	require.NoError(t, err)
	sabra := &fakeChatPane{}
	h.p.mu.Lock()
	h.p.chats = []*openChat{{pane: sabra, ws: h.p.ws}}
	h.p.mu.Unlock()

	cmd, _ := h.p.Sidebar().Select(1)
	msgs := run(cmd)
	find[splitview.OpenPaneMsg](t, msgs) // Alpha has no chat yet: one opens along
	h.swap(find[splitview.SwitchWorkspaceMsg](t, msgs), fakeModel{store: annotation.NewStore()})
	assert.Empty(t, sabra.follows, "Sabra2's chat stays on its thread while Alpha is on screen")
}

func TestProjects_StartReviewKeepsItsStore(t *testing.T) {
	a := newFakeSession("ssh://a", twoProjectShell())
	start := threadReview(t, a, loginID)
	startStore := annotatedModel().store
	h := newHarness(t, Config{NoWatch: true, NoChat: true, StartStore: startStore}, start)
	again := h.openThread(t, a, loginID)
	assert.Same(t, startStore, shown(again).store, "reopening the run's thread keeps its annotations")
}

func TestProjects_SidebarNeedsAConnection(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	sb := h.p.Sidebar()
	assert.False(t, sb.Available())
	h.connect(t, "ssh://a", newFakeSession("ssh://a", twoProjectShell()))
	assert.True(t, sb.Available())
	sb.Shown()
	cmd, hide := sb.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, hide)
	assert.Equal(t, "alpha1", shown(find[splitview.SwitchWorkspaceMsg](t, run(cmd))).review.thread().ID,
		"with nothing on screen enter opens the first project")
}

func TestProjects_FailedListingIsShownAndBacksOff(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	a := newFakeSession("ssh://a", twoProjectShell())
	h.connect(t, "ssh://a", a)
	a.server.failFetches(errors.New("server gone"))
	h.p.useLocal()
	require.True(t, h.p.useLive(a), "back to a: its listing is fetched afresh")
	sb := h.p.Sidebar()
	view := func() string { return ansi.Strip(sb.View(40, 14, false)) }
	require.Eventually(t, func() bool { return strings.Contains(view(), "list threads: server gone") }, 2*time.Second, time.Millisecond,
		"the sidebar says why there are no projects")
	assert.NotContains(t, view(), "loading…")

	h.settle()
	n := a.server.fetched()
	for range 5 {
		view()
	}
	h.settle()
	assert.Equal(t, n, a.server.fetched(), "a redraw right after a failure fetches nothing")

	a.server.failFetches(nil)
	h.p.mu.Lock()
	assert.Equal(t, 1, h.p.shellFails)
	assert.True(t, h.p.shellRetry.After(time.Now()), "the next fetch waits")
	h.p.shellRetry = time.Time{} // the wait is over
	h.p.mu.Unlock()
	view()
	h.settle()
	assert.Contains(t, view(), "Alpha", "the server answers again: its projects")
	assert.NotContains(t, view(), "server gone")
}
