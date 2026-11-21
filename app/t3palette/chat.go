package t3palette

import (
	"cmp"
	"context"
	"fmt"
	"slices"

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
	pane      chatPane
	conn      *t3client.Conn
	chats     *t3chat.Chats
	newThread func(t3chat.NewThread) // what /clear in the pane does
	ws        string                 // the split workspace the pane is in
	session   Session                // the session of the thread it follows, for a reconnect to move it (repointLocked)
	th        t3client.Thread        // the thread it follows
}

// threads are the threads of conn: the ones the pane followed before, when
// it is the same connection.
func (c *openChat) threads(conn *t3client.Conn) *t3chat.Chats {
	if c.chats == nil || conn != c.conn {
		c.conn, c.chats = conn, t3chat.NewChats(conn)
		if c.newThread != nil {
			c.chats.OnNewThread(c.newThread)
		}
	}
	return c.chats
}

// follow points the pane at the thread th reached over conn.
func (c *openChat) follow(conn *t3client.Conn, th t3client.Thread) {
	c.th = th
	c.pane.Follow(c.threads(conn).Thread(th.ID), threadTitle(th))
}

// followOn points the pane at the thread th reached over s.
func (c *openChat) followOn(s Session, th t3client.Thread) {
	c.session = s
	c.follow(s.Conn(), th)
}

// followChatsLocked points the open chat panes of the workspace on screen
// at r's thread, when it has one, and forgets the closed ones. The chats of
// other workspaces stay on their threads. mu must be held.
func (t *Palette) followChatsLocked(r *Review) {
	th := r.thread()
	for _, c := range t.liveChatsLocked() {
		if th != nil && c.ws == t.ws {
			c.followOn(r.Session, *th)
		}
	}
}

// liveChatsLocked forgets the closed chat panes and returns the others.
// mu must be held.
func (t *Palette) liveChatsLocked() []*openChat {
	live := t.chats[:0]
	for _, c := range t.chats {
		if !c.pane.Exited() {
			live = append(live, c)
		}
	}
	t.chats = live
	return live
}

// dropChatsLocked forgets the chat panes of workspace ws, which the split
// closed with it. mu must be held.
func (t *Palette) dropChatsLocked(ws string) {
	t.chats = slices.DeleteFunc(t.chats, func(c *openChat) bool { return c.ws == ws })
}

// chatOpenLocked reports whether workspace ws has a chat pane open. mu must
// be held.
func (t *Palette) chatOpenLocked(ws string) bool {
	return slices.ContainsFunc(t.liveChatsLocked(), func(c *openChat) bool { return c.ws == ws })
}

// autoChatLocked is the request for the chat pane that comes along with r,
// nil when none does: with NoChat, for a project's root, or while r's
// workspace has a chat pane open, which follows r instead. mu must be held.
func (t *Palette) autoChatLocked(r *Review) tea.Msg {
	th := r.thread()
	if t.cfg.NoChat || th == nil || t.chatOpenLocked(wsKey(r)) {
		return nil
	}
	return t.chatPaneMsg(r.Session, *th)
}

// chatPaneMsg is the split's request for a chat pane on th over s.
func (t *Palette) chatPaneMsg(s Session, th t3client.Thread) tea.Msg {
	return splitview.OpenPaneMsg{Start: func(width, height int) (splitview.Terminal, error) {
		c := &openChat{newThread: t.clearThread, session: s, th: th}
		cfg := t.cfg.Chat
		cfg.QuitNote = chatQuitNote
		pane := chat.New(c.threads(s.Conn()).Thread(th.ID), threadTitle(th), width, height, cfg)
		c.pane = pane
		t.mu.Lock()
		c.ws = t.ws // opened in the workspace on screen
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

// clearThread is /clear in a chat pane: a new thread in the project of the
// thread it was typed in, with that thread's model and modes, which takes
// its place on screen and in the chat. It reports on the bottom row.
// The pane calls it under its own lock, and the palette calls into panes
// under mu, so all of it runs on a goroutine of its own; it does not hold up
// Close, since Send returns once the program ends.
func (t *Palette) clearThread(req t3chat.NewThread) {
	go t.startClearThread(req)
}

func (t *Palette) startClearThread(req t3chat.NewThread) {
	t.mu.Lock()
	p, r, closed := t.program, t.onScreen, t.closed
	t.mu.Unlock()
	if p == nil || closed {
		return
	}
	th := r.thread()
	if th == nil || th.ID != req.From {
		p.Send(splitview.FlashMsg{Text: "T3: new thread: the chat's thread is no longer on screen"})
		return
	}
	launch := t3client.LaunchRequest{ProjectID: r.Target.Project.ID, RuntimeMode: cmp.Or(req.RuntimeMode, th.RuntimeMode, runtimeMode),
		InteractionMode: cmp.Or(req.InteractionMode, th.InteractionMode)}
	model := cmp.Or(req.Model, th.ModelSelection)
	cmd := asking("starting a new thread in "+r.Target.Project.Title, func(ctx context.Context) tea.Msg {
		if model == nil {
			m, err := r.Session.Server().DefaultModel(ctx, r.Target.Project)
			if err != nil {
				return t3Error("new thread", err)
			}
			model = &m
		}
		launch.Model = *model
		return t.launchMsg(ctx, r.Session, launch)
	})
	p.Send(cmd())
}
