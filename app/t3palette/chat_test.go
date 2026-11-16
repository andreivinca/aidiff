package t3palette

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/splitview"
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
	h.p.chats = []*openChat{{pane: open}, {pane: closed}}
	h.p.followChatsLocked(threadReview(t, a, olderID))
	live := len(h.p.chats)
	h.p.mu.Unlock()
	assert.Equal(t, 1, live)
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
	assert.True(t, h.p.chatOpenLocked(), "the palette steers the pane it opened")
}
