package t3palette

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3chat"
	"github.com/umputun/revdiff/app/t3client"
)

// fakeChatPane records the backends a chat pane was pointed at.
type fakeChatPane struct {
	exited  bool
	follows []string // the titles followed
	nilBack []bool   // whether each backend was nil
}

func (p *fakeChatPane) Exited() bool {
	return p.exited
}

func (p *fakeChatPane) Follow(b chat.Backend, title string) {
	p.follows = append(p.follows, title)
	p.nilBack = append(p.nilBack, b == nil)
}

func TestOpenChat_KeepsTheConnectionsThreads(t *testing.T) {
	pane := &fakeChatPane{}
	conn := &t3client.Conn{}
	c := &openChat{pane: pane}
	c.follow(conn, t3client.Thread{ID: "a", Title: "First"})
	chats := c.chats
	require.NotNil(t, chats)
	c.follow(conn, t3client.Thread{ID: "b", Title: "Second"})
	assert.Same(t, chats, c.chats, "threads on the same connection share its catalog")
	c.follow(&t3client.Conn{}, t3client.Thread{ID: "c", Title: "Elsewhere"})
	assert.NotSame(t, chats, c.chats, "another connection has a catalog of its own")
	c.follow(nil, t3client.Thread{ID: "d", Title: "Offline"})
	assert.Equal(t, []string{"First", "Second", "Elsewhere", "Offline"}, pane.follows)
	assert.Equal(t, []bool{false, false, false, true}, pane.nilBack, "no connection leaves the pane not connected")
}

func TestPalette_FollowChatsForgetsClosedPanes(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	open, closed := &fakeChatPane{}, &fakeChatPane{exited: true}
	h.p.mu.Lock()
	other := &fakeChatPane{}
	h.p.chats = []*openChat{{pane: open, ws: h.p.ws}, {pane: closed, ws: h.p.ws}, {pane: other, ws: "elsewhere"}}
	h.p.followChatsLocked(threadReview(t, a, olderID))
	live := len(h.p.chats)
	h.p.mu.Unlock()
	assert.Equal(t, 2, live)
	assert.Empty(t, other.follows, "a pane of another workspace stays on its thread")
	assert.Equal(t, []string{"Older work"}, open.follows, "an open pane follows the thread on screen")
	assert.Empty(t, closed.follows)
}

func TestPalette_OpensChatPanes(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	open, ok := h.p.chatPaneMsg(a, t3client.Thread{ID: loginID, Title: "Fix login"}).(splitview.OpenPaneMsg)
	require.True(t, ok)
	pane, err := open.Start(60, 10)
	require.NoError(t, err)
	t.Cleanup(pane.Close)
	noter, ok := pane.(interface{ QuitNote() string })
	require.True(t, ok)
	assert.Equal(t, chatQuitNote, noter.QuitNote(), "the quit dialog says the agent keeps working")
	assert.Contains(t, pane.Render(false), "Fix login")
	h.p.mu.Lock()
	defer h.p.mu.Unlock()
	assert.True(t, h.p.chatOpenLocked(""), "the palette steers the pane it opened")
}

func TestPalette_ClearStartsANewThreadLikeTheChats(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	a.server.launchAs = "fresh"
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	model := &t3client.ModelSelection{InstanceID: "claude", Model: "claude-opus-4"}
	h.p.startClearThread(t3chat.NewThread{From: loginID, Model: model, RuntimeMode: "auto", InteractionMode: "plan"})

	h.program.mu.Lock()
	msgs := expand(h.program.msgs[0])
	h.program.mu.Unlock()
	assert.Contains(t, flashes(msgs), "T3: starting a new thread in Sabra2…")
	replace := find[splitview.SwitchWorkspaceMsg](t, msgs)
	shown, ok := replace.Model.(fakeModel)
	require.True(t, ok)
	assert.Equal(t, "fresh", shown.review.thread().ID, "the new thread takes the screen, and the chat follows it")
	assert.Equal(t, []t3client.LaunchRequest{{ProjectID: "p1", Model: *model, RuntimeMode: "auto", InteractionMode: "plan"}},
		a.server.launches, "in the same project, with the chat thread's settings")
}

func TestPalette_ClearNeedsTheChatsThreadOnScreen(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	a.server.launchAs = "fresh"
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, olderID))
	h.p.startClearThread(t3chat.NewThread{From: loginID})
	h.program.mu.Lock()
	defer h.program.mu.Unlock()
	assert.Equal(t, []string{"T3: new thread: the chat's thread is no longer on screen"}, flashes(h.program.msgs))
	assert.Empty(t, a.server.launches)
}

func TestSideChat_OpensBesideAndStaysOnItsThread(t *testing.T) {
	sh := testShell()
	sh.Threads = append(sh.Threads, t3client.Thread{ID: "side-1", ProjectID: "p1", Title: "Side chat: callers"})
	a := newFakeSession("ssh://a", sh)
	h := newHarness(t, Config{NoWatch: true}, threadReview(t, a, loginID))
	main := &fakeChatPane{}
	h.p.mu.Lock()
	h.p.chats = []*openChat{{pane: main, ws: h.p.ws, session: a, th: *h.p.onScreen.thread()}}
	ws := h.p.ws
	h.p.mu.Unlock()

	msgs := run(h.p.sideChatCmd(a, ws, t3chat.SideChat{From: loginID, ID: "side-1"}))
	open := find[splitview.OpenPaneMsg](t, msgs)
	term, err := open.Start(40, 20)
	require.NoError(t, err)
	t.Cleanup(term.Close)
	h.p.mu.Lock()
	require.Len(t, h.p.chats, 2, "the side chat opens a pane of its own, beside the chat")
	side := h.p.chats[1]
	h.p.mu.Unlock()
	assert.True(t, side.side)
	assert.Equal(t, "Side chat: callers", side.th.Title, "on its thread, as the server lists it")
	assert.Equal(t, ws, side.ws, "in the workspace it was asked in")

	older := threadReview(t, a, olderID)
	h.p.mu.Lock()
	h.p.followChatsLocked(older)
	h.p.mu.Unlock()
	assert.Equal(t, []string{"Older work"}, main.follows, "the review's chat follows the review")
	assert.Equal(t, "side-1", side.th.ID, "the side chat stays on its thread")

	main.exited = true
	h.p.mu.Lock()
	assert.False(t, h.p.chatOpenLocked(ws), "a side chat is not the workspace's chat: the next thread opens one")
	h.p.mu.Unlock()

	h.p.mu.Lock()
	h.p.ws = "elsewhere" // the user moved on before the side chat was listed
	h.p.mu.Unlock()
	msgs = run(h.p.sideChatCmd(a, ws, t3chat.SideChat{From: loginID, ID: "side-1"}))
	assert.Contains(t, flashes(msgs), "T3: side chat started; ctrl+p lists it with the other threads", "no pane where it was not asked")
}
