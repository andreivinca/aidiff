package t3client

import (
	"context"
	"encoding/json"
)

// ThreadEvents receives what SubscribeThread decodes. Handlers run on the
// connection's read goroutine and must not block. The server re-sends whole
// entities, so a streaming reply arrives as the same item with more text.
type ThreadEvents struct {
	Message        func(Message)
	Snapshot       func(ThreadProjection)
	TurnItem       func(TurnItem)
	Run            func(Run)
	Request        func(RuntimeRequest)
	Thread         func(Thread)
	Subagent       func(Subagent)
	ProviderThread func(ProviderThread)
}

// threadEventTypes are the domain events whose payload is the whole thread.
// Other thread.* events carry something else, such as a pull request link.
var threadEventTypes = []string{
	"thread.created",
	"thread.archived",
	"thread.unarchived",
	"thread.deleted",
	"thread.settled",
	"thread.unsettled",
	"thread.snoozed",
	"thread.unsnoozed",
	"thread.pinned",
	"thread.auto-settle-set",
	"thread.unpinned",
	"thread.pin-reordered",
	"thread.active-reordered",
	"thread.tags-updated",
	"thread.visited",
	"thread.marked-unread",
	"thread.metadata-updated",
	"thread.pull-request-synced",
	"thread.runtime-mode-updated",
	"thread.interaction-mode-updated",
	"thread.model-selection-updated",
	"thread.provider-switched",
	"thread.guest-share-updated",
}

// eventHandler takes one domain event's payload.
type eventHandler func(payload json.RawMessage)

// handlers maps each domain event type to the handler that wants it; events
// without a set handler are left out.
func (h ThreadEvents) handlers() map[string]eventHandler {
	table := map[string]eventHandler{}
	if h.Message != nil {
		table["message.updated"] = decodeTo(h.Message)
	}
	if h.TurnItem != nil {
		table["turn-item.updated"] = decodeTo(h.TurnItem)
	}
	if h.Run != nil {
		table["run.created"] = decodeTo(h.Run)
		table["run.updated"] = decodeTo(h.Run)
	}
	if h.Request != nil {
		table["runtime-request.updated"] = decodeTo(h.Request)
	}
	if h.Subagent != nil {
		table["subagent.updated"] = decodeTo(h.Subagent)
	}
	if h.ProviderThread != nil {
		table["provider-thread.updated"] = decodeTo(h.ProviderThread)
	}
	if h.Thread != nil {
		for _, typ := range threadEventTypes {
			table[typ] = decodeTo(h.Thread)
		}
	}
	return table
}

// entity is an event payload naming what it updates.
type entity interface {
	entityID() string
}

// decodeTo is a handler decoding the payload as T for handle. A payload
// that does not decode, or names no entity, is dropped.
func decodeTo[T entity](handle func(T)) eventHandler {
	return func(payload json.RawMessage) {
		var v T
		if json.Unmarshal(payload, &v) != nil || v.entityID() == "" {
			return
		}
		handle(v)
	}
}

func (m Message) entityID() string {
	return m.ID
}

func (t TurnItem) entityID() string {
	return t.ID
}

func (r Run) entityID() string {
	return r.ID
}

func (r RuntimeRequest) entityID() string {
	return r.ID
}

func (t Thread) entityID() string {
	return t.ID
}

func (a Subagent) entityID() string {
	return a.ID
}

func (t ProviderThread) entityID() string {
	return t.ID
}

type threadStreamItem struct {
	Kind       string            `json:"kind"` // snapshot, event, unknown-event, synchronized
	Projection *ThreadProjection `json:"projection"`
	Event      *struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	} `json:"event"`
}

// SubscribeThread streams a thread's transcript: a snapshot, then every
// update, until ctx ends or the connection drops. No handler runs after it
// returns.
func (c *Conn) SubscribeThread(ctx context.Context, threadID string, h ThreadEvents) error {
	return c.subscribe(ctx, "orchestration.subscribeThread", map[string]any{"threadId": threadID}, h.stream().observe)
}

// stream is the router of one subscription delivering to h.
func (h ThreadEvents) stream() threadStream {
	return threadStream{snapshot: h.Snapshot, handlers: h.handlers()}
}

// threadStream routes the values of one thread subscription.
type threadStream struct {
	snapshot func(ThreadProjection)
	handlers map[string]eventHandler
}

// observe takes one stream value.
func (s threadStream) observe(v json.RawMessage) {
	var it threadStreamItem
	if json.Unmarshal(v, &it) != nil {
		return
	}
	switch {
	case it.Kind == "snapshot" && it.Projection != nil:
		if s.snapshot != nil {
			s.snapshot(*it.Projection)
		}
	case it.Kind == "event" && it.Event != nil:
		s.dispatch(it.Event.Type, it.Event.Payload)
	}
}

// dispatch hands one domain event to the handler that wants it.
func (s threadStream) dispatch(typ string, payload json.RawMessage) {
	if handle, ok := s.handlers[typ]; ok {
		handle(payload)
	}
}
