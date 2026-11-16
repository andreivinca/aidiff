package chat

import "sync"

// mailbox holds what the backend delivered until the pane takes it, and
// wakes the split. A backend may deliver from any goroutine with its own
// locks held, even from a command the pane runs with its lock held, so
// delivering never waits on the pane's lock: the pane takes the newest
// delivery whenever it takes its lock.
type mailbox struct {
	mu      sync.Mutex
	sub     uint64        // the subscription whose deliveries count
	conv    *Conversation // the newest conversation not taken yet
	note    string        // the newest note not taken yet
	closed  bool
	updates chan struct{} // closed by close
}

func newMailbox() *mailbox {
	return &mailbox{updates: make(chan struct{}, 1)}
}

// follow drops what older subscriptions delivered and counts sub's from now
// on.
func (m *mailbox) follow(sub uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sub, m.conv, m.note = sub, nil, ""
}

// show keeps c, the newest conversation of subscription sub.
func (m *mailbox) show(sub uint64, c Conversation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sub != m.sub || m.closed {
		return
	}
	m.conv = &c
	m.wakeLocked()
}

// keepNote keeps a note of subscription sub.
func (m *mailbox) keepNote(sub uint64, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sub != m.sub || m.closed || text == "" {
		return
	}
	m.note = text
	m.wakeLocked()
}

// take hands over what was delivered since the last take: the newest
// conversation, nil when none, and the newest note, "" when none.
func (m *mailbox) take() (*Conversation, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, note := m.conv, m.note
	m.conv, m.note = nil, ""
	return c, note
}

// wake asks the split for a redraw.
func (m *mailbox) wake() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wakeLocked()
}

// wakeLocked asks for a redraw; one waiting is enough. mu must be held.
func (m *mailbox) wakeLocked() {
	if m.closed {
		return
	}
	select {
	case m.updates <- struct{}{}:
	default:
	}
}

// close ends the deliveries and closes updates. Safe to call more than once.
func (m *mailbox) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	m.conv, m.note = nil, ""
	close(m.updates)
}

// delivery is one subscription's Viewer.
type delivery struct {
	box *mailbox
	sub uint64
}

// Show delivers the conversation.
func (d delivery) Show(c Conversation) {
	d.box.show(d.sub, c)
}

// Note delivers a note.
func (d delivery) Note(text string) {
	d.box.keepNote(d.sub, text)
}
