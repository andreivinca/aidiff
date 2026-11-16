// Package t3chat is the T3 Code side of the chat pane: a chat.Backend over
// one thread of a T3 server. It follows the thread's events, turns them
// into the conversations the pane shows, and carries out what the user
// asks: messages with their images, answers to the agent's prompts, stops,
// and T3's own slash commands (/model, /effort, /plan, /build, /mode,
// /clear, /side), with the provider's commands from the server's catalog.
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
}

// Chats opens chat backends on the threads of one T3 connection. Its
// backends share the server's catalog, so it is fetched once however often
// the user switches threads.
type Chats struct {
	srv     server // nil when there is no connection
	catalog *catalog
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
	return newBackend(c.srv, c.catalog, threadID)
}

// Backend is one thread of a T3 server as a chat.Backend. One mutex guards
// what it knows of the thread; it shows the pane a new conversation after
// every change.
type Backend struct {
	srv      server
	catalog  *catalog
	threadID string
	commands []chat.Command // T3's own slash commands

	mu      sync.Mutex
	sub     uint64      // the current subscription; events of older ones are dropped
	viewer  chat.Viewer // the current subscription's; nil before the first
	thread  thread
	loading bool // a catalog fetch of this backend's is in flight
	fresh   bool // /clear: the next message starts a fresh provider session
	sent    bool // a message went out from here
}

// newBackend is the backend of threadID on srv, sharing catalog.
func newBackend(srv server, cat *catalog, threadID string) *Backend {
	b := &Backend{srv: srv, catalog: cat, threadID: threadID, thread: newThread(threadID)}
	b.commands = b.ownCommands()
	return b
}

// Subscribe follows the thread for v until ctx ends or the connection
// drops, and fetches the catalog the first time the server is asked.
func (b *Backend) Subscribe(ctx context.Context, v chat.Viewer) error {
	b.mu.Lock()
	b.sub++
	sub := b.sub
	b.viewer = v
	b.thread = newThread(b.threadID)
	if b.catalog.unknown() && !b.loading {
		b.fetchCatalogLocked()
	}
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
				return t.update(th)
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
	b.showLocked()
}

// showLocked shows the pane the conversation as it stands; mu must be held.
func (b *Backend) showLocked() {
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
