// Package t3chat is the T3 Code side of the chat pane: a chat.Backend over
// one thread of a T3 server. It follows the thread's events, turns them
// into the conversations the pane shows, and carries out what the user
// asks: messages with their images and files, answers to the agent's
// prompts, stops, and T3's own slash commands (/model, /effort, /plan,
// /build, /mode, /clear, /side), with the provider's commands from the
// server's catalog.
package t3chat

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// fetchTimeout bounds one catalog fetch.
const fetchTimeout = 30 * time.Second

// server is what the backend asks of a T3 server over one connection;
// *t3client.Conn is one.
type server interface {
	SubscribeThread(ctx context.Context, threadID string, h t3client.ThreadEvents) error
	Dispatch(ctx context.Context, cmd t3client.Command) error
	FetchCatalog(ctx context.Context) (t3client.Catalog, error)
	PersistImages(ctx context.Context, threadID, messageID string, images []t3client.ImageUpload) ([]json.RawMessage, error)
	UploadFile(ctx context.Context, f t3client.FileUpload, progress func(sent int64)) (t3client.Upload, error)
	DeleteUpload(ctx context.Context, id string) error
}

// Chats opens chat backends on the threads of one T3 connection. Its
// backends share the server's catalog, so it is fetched once however often
// the user switches threads.
type Chats struct {
	srv       server // nil when there is no connection
	catalog   *catalog
	newThread func(NewThread) // /clear's; nil keeps T3's fresh session
	sideChat  func(SideChat)  // /side's; nil leaves the side chat to the thread list
}

// NewThread is the thread /clear asks for in place of the one it was typed
// in, From, with that thread's settings as the chat last knew them.
type NewThread struct {
	From            string
	Model           *t3client.ModelSelection // nil when not known yet
	RuntimeMode     string
	InteractionMode string
}

// OnNewThread makes /clear start a new thread through start, which must not
// block, instead of arming a fresh session in the same thread. It applies to
// the backends opened afterwards.
func (c *Chats) OnNewThread(start func(NewThread)) {
	c.newThread = start
}

// SideChat is the side chat /side forked from thread From: the thread ID,
// which the client named.
type SideChat struct {
	From, ID string
}

// OnSideChat makes /side hand the side chat it forked to open, which must
// not block, once the fork went through, so the host can show it. It
// applies to the backends opened afterwards.
func (c *Chats) OnSideChat(open func(SideChat)) {
	c.sideChat = open
}

// NewChats serves the threads reached over conn, which may be nil.
func NewChats(conn *t3client.Conn) *Chats {
	if conn == nil {
		return newChats(nil)
	}
	return newChats(conn)
}

// newChats serves the threads of srv.
func newChats(srv server) *Chats {
	return &Chats{srv: srv, catalog: &catalog{}}
}

// Thread is the backend of the thread threadID, nil when there is no
// connection.
func (c *Chats) Thread(threadID string) chat.Backend {
	if c.srv == nil {
		return nil
	}
	b := newBackend(c.srv, c.catalog, threadID)
	b.sideChat = c.sideChat
	if c.newThread != nil {
		b.newThread = c.newThread
		b.commands = b.ownCommands() // /clear says what it does now
	}
	return b
}

// Backend is one thread of a T3 server as a chat.Backend. One mutex guards
// what it knows of the thread; it shows the pane a new conversation after
// every change.
type Backend struct {
	srv      server
	catalog  *catalog
	threadID string
	commands []chat.Command // T3's own slash commands
	// newThread starts the thread /clear asks for; nil arms a fresh session
	newThread func(NewThread)
	// sideChat opens the side chat /side forked; nil when the host shows
	// none
	sideChat func(SideChat)

	mu     sync.Mutex
	sub    uint64      // the current subscription; events of older ones are dropped
	viewer chat.Viewer // the current subscription's; nil before the first
	thread thread
	fresh  bool // /clear: the next message starts a fresh provider session
	// chosen is the model selection /model or /effort set from here, which
	// every message sent from here carries, as T3's own client does: a
	// message queued earlier keeps the selection it was queued with, and
	// when it starts T3 sets that one back on the thread. nil once the
	// thread holds it with nothing queued left to change it back.
	chosen *t3client.ModelSelection
	// pending is a /model or /effort that waits its turn behind the agent's
	// work, nil when none does (selection.go)
	pending *pendingSelection
	sent    bool // a message went out from here

	instanceMu sync.Mutex
	instance   string // the provider instance of selectionLocked as showLocked saw it last, which Commands reads without mu
}

// newBackend is the backend of threadID on srv, sharing catalog.
func newBackend(srv server, cat *catalog, threadID string) *Backend {
	b := &Backend{srv: srv, catalog: cat, threadID: threadID, thread: newThread(threadID)}
	b.commands = b.ownCommands()
	return b
}

// Subscribe follows the thread for v until ctx ends or the connection
// drops, and fetches the catalog the first time the server is asked, or
// waits for the fetch another backend started.
func (b *Backend) Subscribe(ctx context.Context, v chat.Viewer) error {
	b.mu.Lock()
	b.sub++
	sub := b.sub
	b.viewer = v
	b.thread = newThread(b.threadID)
	b.catalog.request(b.srv, false, b.catalogArrived)
	b.mu.Unlock()
	return b.srv.SubscribeThread(ctx, b.threadID, b.events(sub)) //nolint:wrapcheck // the pane shows the connection's own words
}

// events routes the events of subscription sub to the thread.
func (b *Backend) events(sub uint64) t3client.ThreadEvents {
	return t3client.ThreadEvents{
		Snapshot: func(pr t3client.ThreadProjection) {
			b.change(sub, func(t *thread) bool {
				t.load(pr)
				return true
			})
		},
		Message: func(m t3client.Message) {
			b.change(sub, func(t *thread) bool {
				return t.message(m)
			})
		},
		TurnItem: func(it t3client.TurnItem) {
			b.change(sub, func(t *thread) bool {
				t.upsert(it)
				return true
			})
		},
		Run: func(r t3client.Run) {
			b.change(sub, func(t *thread) bool {
				t.runs[r.ID] = r
				return true
			})
		},
		Request: func(r t3client.RuntimeRequest) {
			b.change(sub, func(t *thread) bool {
				t.requests[r.ID] = r
				return true
			})
		},
		Thread: func(th t3client.Thread) {
			b.change(sub, func(t *thread) bool {
				if !t.update(th) {
					return false
				}
				b.settleChoiceLocked()
				return true
			})
		},
		Subagent: func(a t3client.Subagent) {
			b.change(sub, func(t *thread) bool {
				t.subagent(a)
				return true
			})
		},
		ProviderThread: func(th t3client.ProviderThread) {
			b.change(sub, func(t *thread) bool {
				t.providers[th.ID] = th
				return true
			})
		},
	}
}

// change applies an event of subscription sub to the thread and shows the
// result, unless the subscription was replaced or the event changed
// nothing. It runs on the connection's reader and does not block.
func (b *Backend) change(sub uint64, apply func(t *thread) bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sub != b.sub || !apply(&b.thread) {
		return
	}
	b.applyPendingLocked()
	b.showLocked()
}

// selectionLocked is the model selection the next message runs with: the
// one chosen from here, else the thread's; mu must be held.
func (b *Backend) selectionLocked() *t3client.ModelSelection {
	if b.chosen != nil {
		return b.chosen
	}
	return b.thread.model
}

// settleChoiceLocked forgets the selection chosen from here once the thread
// holds it and no queued message is left that would set its own selection
// back; changes made from another client then show as they come. mu must be
// held.
func (b *Backend) settleChoiceLocked() {
	if b.chosen != nil && b.pending == nil && b.chosen.Same(b.thread.model) && len(b.thread.queue()) == 0 {
		b.chosen = nil
	}
}

// showLocked shows the pane the conversation as it stands; mu must be held.
// Every change of the thread or of the selection chosen from here ends in
// it.
func (b *Backend) showLocked() {
	b.instanceLocked()
	if b.viewer == nil {
		return
	}
	b.viewer.Show(b.conversationLocked())
}

// noteLocked tells the user something; mu must be held.
func (b *Backend) noteLocked(text string) {
	if b.viewer == nil {
		return
	}
	b.viewer.Note(text)
}
