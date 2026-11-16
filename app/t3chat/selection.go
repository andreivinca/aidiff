package t3chat

import (
	"context"
	"fmt"
	"slices"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// pendingSelectionID is the queue card of a /model or /effort that waits
// its turn; ↑ takes it back like a queued message.
const pendingSelectionID = "aidiff:pending-selection"

// pendingSelection is a /model or /effort given while the agent works. It
// waits its turn like a queued message: the messages queued before it run
// with the selection they were queued with, the ones sent after it carry it
// (see chosen), and once nothing is left ahead of it the thread takes it.
type pendingSelection struct {
	selection t3client.ModelSelection
	what      string          // model or effort, for a failure
	command   string          // as typed, "/effort high", for the card and for taking it back
	before    map[string]bool // the messages queued ahead of it
}

// choose sets selection, which the command typed as command chose: at once
// while the agent is idle with nothing queued, else as a queued card that
// waits its turn. Either way the messages sent from here carry it from now
// on. note says what it did.
func (b *Backend) choose(what string, selection t3client.ModelSelection, command, note string) chat.Reply {
	if b.busyLocked() {
		before := map[string]bool{}
		for _, q := range b.thread.queue() {
			before[q.ID] = true
		}
		b.chosen = &selection
		b.pending = &pendingSelection{selection: selection, what: what, command: command, before: before}
		b.showLocked()
		return chat.Reply{Note: note + " after the agent's current work; messages sent from now run with it"}
	}
	send := batch{srv: b.srv, what: what, cmds: []t3client.Command{t3client.ModelSelectionCommand(b.threadID, selection)}}.send
	return chat.Reply{Note: note, Send: func(ctx context.Context) error {
		if err := send(ctx); err != nil {
			return err
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		b.chosen = &selection
		b.settleChoiceLocked()
		b.showLocked()
		return nil
	}}
}

// busyLocked reports whether the agent works or messages wait in the queue;
// mu must be held.
func (b *Backend) busyLocked() bool {
	return len(b.thread.queue()) > 0 || slices.ContainsFunc(b.thread.activeRuns(), func(id string) bool {
		return b.thread.runs[id].Active()
	})
}

// applyPendingLocked gives the thread the waiting selection once its turn
// came: nothing it waited for is queued or running. A message sent after it
// that already gave the thread the same selection settles it too. mu must
// be held.
func (b *Backend) applyPendingLocked() {
	p := b.pending
	if p == nil {
		return
	}
	if p.selection.Same(b.thread.model) && !b.waitsLocked(p) {
		b.pending = nil
		b.settleChoiceLocked()
		return
	}
	if b.busyLocked() {
		return
	}
	b.pending = nil
	cmd := t3client.ModelSelectionCommand(b.threadID, p.selection)
	go func() { // events arrive on the connection's reader, which must not wait on a call
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		if err := b.srv.Dispatch(ctx, cmd); err != nil {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.noteLocked(fmt.Sprintf("%s failed: %v", p.what, err))
		}
	}()
}

// waitsLocked reports whether a message queued ahead of p is still queued;
// mu must be held.
func (b *Backend) waitsLocked(p *pendingSelection) bool {
	return slices.ContainsFunc(b.thread.queue(), func(q chat.Queued) bool {
		return p.before[q.ID]
	})
}

// withPendingLocked is the queue with the waiting selection's card after
// the messages queued ahead of it; mu must be held.
func (b *Backend) withPendingLocked(queued []chat.Queued) []chat.Queued {
	p := b.pending
	if p == nil {
		return queued
	}
	at := 0
	for i, q := range queued {
		if p.before[q.ID] {
			at = i + 1
		}
	}
	return slices.Insert(slices.Clone(queued), at, chat.Queued{ID: pendingSelectionID, Text: p.command})
}

// unqueuePendingLocked takes the waiting selection back: nothing changes on
// the thread, and messages sent from here carry its selection again. It
// reports whether messageID was its card; mu must be held.
func (b *Backend) unqueuePendingLocked(messageID string) bool {
	if messageID != pendingSelectionID || b.pending == nil {
		return false
	}
	b.pending, b.chosen = nil, nil
	b.showLocked()
	return true
}
