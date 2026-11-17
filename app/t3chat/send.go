package t3chat

import (
	"context"
	"errors"
	"fmt"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// Send stores the message's images under its id, then sends the message
// carrying them. After /clear it opens a fresh session; the first message
// names a thread still called "New thread".
func (b *Backend) Send(ctx context.Context, m chat.Outgoing) error {
	msg := b.outgoing(m)
	if len(m.Images) > 0 {
		uploads := make([]t3client.ImageUpload, 0, len(m.Images))
		for _, img := range m.Images {
			uploads = append(uploads, t3client.ImageUpload{Name: img.Name, MimeType: img.MIME, Data: img.Data})
		}
		atts, err := b.srv.PersistImages(ctx, b.threadID, m.ID, uploads)
		if err != nil {
			return err //nolint:wrapcheck // the pane says what failed
		}
		msg.Attachments = atts
	}
	if err := b.srv.Dispatch(ctx, msg.Command()); err != nil {
		return err //nolint:wrapcheck // the pane says what failed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = true
	return nil
}

// outgoing is the message m as T3 takes it. It uses up a fresh context
// armed by /clear.
func (b *Backend) outgoing(m chat.Outgoing) t3client.OutgoingMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	msg := t3client.OutgoingMessage{ThreadID: b.threadID, MessageID: m.ID, Text: m.Text, FreshContext: b.fresh, Model: b.chosen}
	if !b.sent && !b.thread.hasUserMessage() {
		msg.TitleSeed = t3client.TitleSeed(m.Prose(), len(m.Images))
	}
	if b.fresh {
		b.fresh = false
		b.showLocked()
	}
	return msg
}

// Interrupt stops the agent: the whole thread, subagents and background
// work included, as T3's own stop does. T3 holds the queued messages back
// after it, so the queue is resumed right away: the next queued message
// starts on its own. A server that does not know thread.stop gets the
// interrupt of the run turnID instead, which leaves the queue going.
func (b *Backend) Interrupt(ctx context.Context, turnID string) error {
	err := b.srv.Dispatch(ctx, t3client.ThreadStopCommand(b.threadID))
	if err == nil {
		return b.resumeQueue(ctx)
	}
	if ctx.Err() != nil {
		return err //nolint:wrapcheck // the pane says what failed
	}
	if fallback := b.srv.Dispatch(ctx, t3client.InterruptCommand(b.threadID, turnID)); fallback != nil {
		return err //nolint:wrapcheck // the stop's own failure says more than the fallback's
	}
	return nil
}

// resumeQueue starts the next queued message a stop held back; nothing is
// sent when the queue is empty.
func (b *Backend) resumeQueue(ctx context.Context) error {
	b.mu.Lock()
	queued := b.thread.holdsQueue()
	b.mu.Unlock()
	if !queued {
		return nil
	}
	if err := b.srv.Dispatch(ctx, t3client.QueueResumeCommand(b.threadID)); err != nil {
		return fmt.Errorf("%w, but the queued messages did not start: %w", chat.ErrStopped, err)
	}
	return nil
}

// Unqueue drops the queued message messageID from the thread's queue,
// before its run starts.
func (b *Backend) Unqueue(ctx context.Context, messageID string) error {
	b.mu.Lock()
	if b.unqueuePendingLocked(messageID) {
		b.mu.Unlock()
		return nil
	}
	runID := b.thread.queuedRunOf(messageID)
	b.mu.Unlock()
	if runID == "" {
		return errors.New("it is no longer queued")
	}
	return b.srv.Dispatch(ctx, t3client.QueuedRunCancelCommand(b.threadID, runID)) //nolint:wrapcheck // the pane says what failed
}

// Decide answers the approval request promptID.
func (b *Backend) Decide(ctx context.Context, promptID string, d chat.Decision) error {
	cmd := t3client.RespondCommand(b.threadID, promptID, string(d))
	return b.srv.Dispatch(ctx, cmd) //nolint:wrapcheck // the pane says what failed
}

// Answer answers the questions of the user input request promptID: a
// question that takes several options gets a list, any other one string.
func (b *Backend) Answer(ctx context.Context, promptID string, answers map[string]chat.Answer) error {
	values := make(map[string]any, len(answers))
	for id, a := range answers {
		switch {
		case a.Multiple:
			values[id] = a.Values
		case len(a.Values) > 0:
			values[id] = a.Values[0]
		}
	}
	cmd := t3client.AnswerCommand(b.threadID, promptID, values)
	return b.srv.Dispatch(ctx, cmd) //nolint:wrapcheck // the pane says what failed
}

// Dismiss drops the user input request promptID unanswered.
func (b *Backend) Dismiss(ctx context.Context, promptID string) error {
	cmd := t3client.DismissQuestionCommand(b.threadID, promptID)
	return b.srv.Dispatch(ctx, cmd) //nolint:wrapcheck // the pane says what failed
}

// CheckImage turns down an image T3 would refuse for its size.
func (b *Backend) CheckImage(name string, size int64) error {
	return t3client.ValidateImageSize(name, size) //nolint:wrapcheck // T3's own words say why
}
