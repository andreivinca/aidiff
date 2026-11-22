package t3chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// dispatchTimeout bounds sending a message once its files are up.
const dispatchTimeout = 30 * time.Second

// cleanupTimeout bounds deleting the uploads of a message.
const cleanupTimeout = 10 * time.Second

// Send uploads the message's files, one at a time and read as they go,
// stores its images under its id, then sends the message carrying them all.
// After /clear it opens a fresh session; the first message names a thread
// still called "New thread". Cancelling ctx stops an upload, never half a
// send: once the files are up the message goes out on a deadline of its
// own. The uploads of a message that was not sent are deleted, and so are
// those of one that was, which the server copied into the thread.
func (b *Backend) Send(ctx context.Context, m chat.Outgoing) error {
	msg := b.outgoing(m)
	if err := t3client.ValidateAttachments(attachmentSizes(m)); err != nil {
		return err //nolint:wrapcheck // T3's own words say why
	}
	uploads, err := b.uploadFiles(ctx, m)
	defer b.discard(uploads)
	if err != nil {
		return err
	}
	if len(m.Images) > 0 {
		images := make([]t3client.ImageUpload, 0, len(m.Images))
		for _, img := range m.Images {
			images = append(images, t3client.ImageUpload{Name: img.Name, MimeType: img.MIME, Data: img.Data})
		}
		atts, err := b.srv.PersistImages(ctx, b.threadID, m.ID, images)
		if err != nil {
			return err //nolint:wrapcheck // the pane says what failed
		}
		msg.Attachments = atts
	}
	for _, up := range uploads {
		msg.Attachments = append(msg.Attachments, up.Attachment)
	}
	if len(uploads) > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), dispatchTimeout)
		defer cancel()
	}
	if err := b.srv.Dispatch(ctx, msg.Command()); err != nil {
		return err //nolint:wrapcheck // the pane says what failed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = true
	if msg.FreshContext && b.fresh { // used up only now: a message that failed leaves it for the retry
		b.fresh = false
		b.showLocked()
	}
	return nil
}

// uploadFiles sends the message's files to the server ahead of it, in
// order, telling m.Progress how far each went. A file is opened only now,
// so it goes as it is now; one that is gone fails the send. On failure the
// uploads made so far are returned too, for the caller to delete.
func (b *Backend) uploadFiles(ctx context.Context, m chat.Outgoing) ([]t3client.Upload, error) {
	var uploads []t3client.Upload
	for _, f := range m.Files {
		up, err := b.uploadFile(ctx, f, m.Progress)
		if err != nil {
			return uploads, err
		}
		uploads = append(uploads, up)
	}
	return uploads, nil
}

// uploadFile sends one file, read from its start.
func (b *Backend) uploadFile(ctx context.Context, f chat.File, progress func(name string, sent, size int64)) (t3client.Upload, error) {
	if f.Open == nil {
		return t3client.Upload{}, fmt.Errorf("%s cannot be read", f.Name)
	}
	content, size, err := f.Open()
	if err != nil {
		return t3client.Upload{}, err
	}
	defer func() { _ = content.Close() }()
	var sent func(int64)
	if progress != nil {
		progress(f.Name, 0, size)
		sent = func(n int64) {
			progress(f.Name, n, size)
		}
	}
	return b.srv.UploadFile(ctx, t3client.FileUpload{Name: f.Name, MimeType: f.MIME, Size: size, Content: content}, sent) //nolint:wrapcheck // its words name the file
}

// discard deletes uploads in the background: what is left of a message's
// files once it went out, or failed to.
func (b *Backend) discard(uploads []t3client.Upload) {
	if len(uploads) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		for _, up := range uploads {
			_ = b.srv.DeleteUpload(ctx, up.ID) // best effort: T3 sweeps a forgotten upload after a day
		}
	}()
}

// attachmentSizes is what m carries, as T3's limits on a message count it.
func attachmentSizes(m chat.Outgoing) []t3client.AttachmentSize {
	out := make([]t3client.AttachmentSize, 0, len(m.Images)+len(m.Files))
	for _, img := range m.Images {
		out = append(out, t3client.AttachmentSize{Image: true, MimeType: img.MIME, Size: int64(len(img.Data))})
	}
	for _, f := range m.Files {
		out = append(out, t3client.AttachmentSize{MimeType: f.MIME, Size: f.Size})
	}
	return out
}

// outgoing is the message m as T3 takes it, with a fresh context armed by
// /clear, which Send uses up once the message went out.
func (b *Backend) outgoing(m chat.Outgoing) t3client.OutgoingMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	msg := t3client.OutgoingMessage{ThreadID: b.threadID, MessageID: m.ID, Text: m.Text, FreshContext: b.fresh, Model: b.chosen}
	if !b.sent && !b.thread.hasUserMessage() {
		msg.TitleSeed = t3client.TitleSeed(m.Prose(), len(m.Images), len(m.Files))
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

// CheckAttachments turns down attachments T3 would refuse on one message:
// the newest too large, or an empty file, too many, or images (files of an
// image type included) over T3's total.
func (b *Backend) CheckAttachments(atts []chat.Attachment) error {
	if len(atts) == 0 {
		return nil
	}
	last := atts[len(atts)-1]
	check := t3client.ValidateFileSize
	if last.Kind == chat.AttachedImage {
		check = t3client.ValidateImageSize
	}
	if err := check(last.Name, last.Size); err != nil {
		return err //nolint:wrapcheck // T3's own words say why
	}
	sizes := make([]t3client.AttachmentSize, 0, len(atts))
	for _, a := range atts {
		sizes = append(sizes, t3client.AttachmentSize{Image: a.Kind == chat.AttachedImage, MimeType: a.MIME, Size: a.Size})
	}
	return t3client.ValidateAttachments(sizes) //nolint:wrapcheck // T3's own words say why
}

// secretServer is a server that answers the agent's secret requests
// (t3client.Conn.AnswerSecret).
type secretServer interface {
	AnswerSecret(ctx context.Context, threadID, itemID, secret string, decline bool) error
}

// ProvideSecret hands secret to T3 for the secret request requestID.
func (b *Backend) ProvideSecret(ctx context.Context, requestID, secret string) error {
	s, ok := b.srv.(secretServer)
	if !ok {
		return errors.New("this server takes no secrets")
	}
	return s.AnswerSecret(ctx, b.threadID, requestID, secret, false) //nolint:wrapcheck // the pane says what failed
}

// DeclineSecret turns the secret request requestID down.
func (b *Backend) DeclineSecret(ctx context.Context, requestID string) error {
	s, ok := b.srv.(secretServer)
	if !ok {
		return errors.New("this server takes no secrets")
	}
	return s.AnswerSecret(ctx, b.threadID, requestID, "", true) //nolint:wrapcheck // the pane says what failed
}
