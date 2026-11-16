package t3palette

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3chat"
	"github.com/umputun/revdiff/app/t3client"
)

// chatQuitNote is what the quit dialog says about closing a chat pane.
const chatQuitNote = "Closes the chat. The agent keeps working on the server; it reopens with the next thread you open."

// chatPane is an open chat pane as the palette steers it; *chat.Pane
// implements it.
type chatPane interface {
	Exited() bool
	Follow(b chat.Backend, title string)
}

// openChat is a chat pane the palette opened, with the threads of the
// connection its thread is on. The pane keeps them while it follows
// threads on the same connection, so the server's catalog is fetched once.
type openChat struct {
	pane  chatPane
	conn  *t3client.Conn
	chats *t3chat.Chats
}

// threads are the threads of conn: the ones the pane followed before, when
// it is the same connection.
func (c *openChat) threads(conn *t3client.Conn) *t3chat.Chats {
	if c.chats == nil || conn != c.conn {
		c.conn, c.chats = conn, t3chat.NewChats(conn)
	}
	return c.chats
}

// follow points the pane at the thread th reached over conn.
func (c *openChat) follow(conn *t3client.Conn, th t3client.Thread) {
	c.pane.Follow(c.threads(conn).Thread(th.ID), threadTitle(th))
}

// followChatsLocked points the open chat panes at r's thread, when it has
// one, and forgets the closed ones. mu must be held.
func (t *Palette) followChatsLocked(r *Review) {
	th := r.thread()
	live := t.chats[:0]
	for _, c := range t.chats {
		if c.pane.Exited() {
			continue
		}
		if th != nil {
			c.follow(r.Session.Conn(), *th)
		}
		live = append(live, c)
	}
	t.chats = live
}

// chatOpenLocked reports whether a chat pane is open, forgetting closed ones.
// mu must be held.
func (t *Palette) chatOpenLocked() bool {
	live := t.chats[:0]
	for _, c := range t.chats {
		if !c.pane.Exited() {
			live = append(live, c)
		}
	}
	t.chats = live
	return len(t.chats) > 0
}

// autoChatLocked is the request for the chat pane that comes along with r,
// nil when none does: with NoChat, for a project's root, or while a chat
// pane is open, which follows r instead. mu must be held.
func (t *Palette) autoChatLocked(r *Review) tea.Msg {
	th := r.thread()
	if t.cfg.NoChat || th == nil || t.chatOpenLocked() {
		return nil
	}
	return t.chatPaneMsg(r.Session, *th)
}

// chatPaneMsg is the split's request for a chat pane on th over s.
func (t *Palette) chatPaneMsg(s Session, th t3client.Thread) tea.Msg {
	return splitview.OpenPaneMsg{Start: func(width, height int) (splitview.Terminal, error) {
		c := &openChat{}
		cfg := t.cfg.Chat
		cfg.QuitNote = chatQuitNote
		pane := chat.New(c.threads(s.Conn()).Thread(th.ID), threadTitle(th), width, height, cfg)
		c.pane = pane
		t.mu.Lock()
		t.chats = append(t.chats, c)
		t.mu.Unlock()
		return pane, nil
	}}
}

// threadReview is the review on screen when it shows a thread, else nil.
func (t *Palette) threadReview() *Review {
	r := t.OnScreen()
	if r.thread() == nil {
		return nil
	}
	return r
}

// sendAnnotations hands the review's annotations to the agent of the thread
// on screen.
func (t *Palette) sendAnnotations(inner tea.Model) tea.Cmd {
	r := t.threadReview()
	if r == nil {
		return flash("T3: open a thread first")
	}
	m, isReview := inner.(annotated)
	if !isReview || m.Store().Count() == 0 {
		return flash("T3: no annotations to send")
	}
	n := m.Store().Count()
	text := "Review annotations from aidiff:\n\n" + m.Store().FormatOutput()
	title := threadTitle(*r.thread())
	return asking("sending to "+title, func(ctx context.Context) tea.Msg {
		if err := r.Session.Server().SendMessage(ctx, r.thread().ID, text); err != nil {
			return t3Error("send", err)
		}
		return splitview.FlashMsg{Text: fmt.Sprintf("T3: %d annotation(s) sent to %s", n, title)}
	})
}
