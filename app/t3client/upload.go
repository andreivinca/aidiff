package t3client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
)

// T3's limits on what one message carries (its PROVIDER_SEND_TURN_* and
// getProviderAttachmentLimitError).
const (
	// MaxFileBytes is the most one file attachment may hold.
	MaxFileBytes = 50 << 20
	// MaxAttachments is the most attachments one message may carry.
	MaxAttachments = 100
	// maxTotalImageBytes bounds a message's images together, files of an
	// image type included.
	maxTotalImageBytes = 80 << 20
)

// Attachment names and media types as T3 stores them: trimmed, not blank,
// within these lengths (in UTF-16 code units, as JavaScript counts).
const (
	maxNameUnits = 255
	maxMimeChars = 100
)

// uploadStallTimeout ends an upload that sent nothing for this long: a
// large file on a slow link may take minutes, so only a transfer that stops
// moving is taken for a dead one. A variable so tests can shorten it.
var uploadStallTimeout = time.Minute

// cleanupTimeout bounds deleting an upload no message took.
const cleanupTimeout = 10 * time.Second

// errStalled ends an upload that stopped moving.
var errStalled = errors.New("the upload stalled")

// providerImageTypes are the image types T3 hands an agent as images; a
// message's images and its files of these types count together against
// maxTotalImageBytes.
var providerImageTypes = map[string]bool{"image/gif": true, "image/jpeg": true, "image/png": true, "image/webp": true}

// ValidateFileSize reports a file of size bytes that T3 would refuse, so it
// can be checked before it is read.
func ValidateFileSize(name string, size int64) error {
	switch {
	case size < 1:
		return fmt.Errorf("%s is empty; T3 takes no empty files", name)
	case size > MaxFileBytes:
		return fmt.Errorf("%s is %.1f MB; T3 takes files up to %d MB", name, float64(size)/(1<<20), MaxFileBytes>>20)
	}
	return nil
}

// AttachmentSize is one attachment of a message, as T3's limits on a whole
// message count it.
type AttachmentSize struct {
	Image    bool   // sent as an image (PersistImages), not as a file
	MimeType string // a file's media type: one of an image type counts with the images
	Size     int64
}

// ValidateAttachments reports a set of attachments T3 would refuse on one
// message: too many, or images (files of an image type included) over
// T3's total.
func ValidateAttachments(atts []AttachmentSize) error {
	if len(atts) > MaxAttachments {
		return fmt.Errorf("T3 takes up to %d attachments per message", MaxAttachments)
	}
	var images int64
	for _, a := range atts {
		if a.Image || providerImageTypes[mediaType(a.MimeType)] {
			images += a.Size
		}
	}
	if images > maxTotalImageBytes {
		return fmt.Errorf("images take up to %d MB per message together; T3 refuses more", maxTotalImageBytes>>20)
	}
	return nil
}

// FileUpload is a file to send with a message. Its content is read as it
// is sent, so a large file is never held in memory.
type FileUpload struct {
	Name     string
	MimeType string    // "" for a file of no known type
	Size     int64     // what the server is told to expect
	Content  io.Reader // the file's Size bytes; no more is read. A Closer is closed when the upload is stopped, so a read waiting on it ends
}

// Upload is a file stored on the server for the message about to be sent.
// A message that then is not sent leaves it behind: DeleteUpload removes
// it, else T3 sweeps it after a day.
type Upload struct {
	ID         string          // the server's attachment id
	Attachment json.RawMessage // what the message carries for the file
}

// UploadFile stores f on the server for the message about to be sent and
// returns what that message carries for it. The server hands out a signed
// address (attachments.createUploadUrl) and the content is posted there,
// over the connection's own transport, with its bearer token, below the
// path the server is served under. progress, when set, is told how many
// bytes went so far. An upload that sends nothing for a minute is ended.
// When the transfer fails, what the server kept of it is deleted.
func (c *Conn) UploadFile(ctx context.Context, f FileUpload, progress func(sent int64)) (Upload, error) {
	name, mimeType := attachmentName(f.Name), attachmentType(f.MimeType)
	if err := ValidateFileSize(name, f.Size); err != nil {
		return Upload{}, err
	}
	var minted struct {
		AttachmentID string `json:"attachmentId"`
		RelativeURL  string `json:"relativeUrl"`
	}
	payload := map[string]any{"type": "file", "name": name, "mimeType": mimeType, "sizeBytes": f.Size}
	if err := c.call(ctx, "attachments.createUploadUrl", payload, &minted); err != nil {
		return Upload{}, fmt.Errorf("upload %s: %w", name, err)
	}
	if minted.AttachmentID == "" {
		return Upload{}, fmt.Errorf("upload %s: the server named no attachment", name)
	}
	if err := c.post(ctx, minted.RelativeURL, mimeType, f, progress); err != nil {
		c.discardUpload(minted.AttachmentID)
		return Upload{}, fmt.Errorf("upload %s: %w", name, err)
	}
	att, err := json.Marshal(map[string]any{"type": "file", "id": minted.AttachmentID, "name": name, "mimeType": mimeType, "sizeBytes": f.Size})
	if err != nil {
		return Upload{}, fmt.Errorf("upload %s: %w", name, err)
	}
	return Upload{ID: minted.AttachmentID, Attachment: att}, nil
}

// DeleteUpload removes an upload no message took. The server deletes only
// uploads still waiting for a message, so one a message carries stays.
func (c *Conn) DeleteUpload(ctx context.Context, id string) error {
	return c.call(ctx, "attachments.delete", map[string]any{"attachmentId": id}, nil)
}

// discardUpload deletes an upload that failed, in the background: what
// went up of it is the server's to forget, and the caller's context may be
// what ended it.
func (c *Conn) discardUpload(id string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = c.DeleteUpload(ctx, id) // best effort: T3 sweeps a forgotten upload after a day
	}()
}

// post sends f's content to the upload address rel, a path below the
// server's base. Exactly f.Size bytes are sent: the server refuses any
// other count.
func (c *Conn) post(ctx context.Context, rel, mimeType string, f FileUpload, progress func(int64)) error {
	target, err := c.uploadURL(rel)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if closer, ok := f.Content.(io.Closer); ok {
		// the HTTP client waits for a read of the body to return even once the
		// request is stopped; closing the content ends one waiting on it
		stop := context.AfterFunc(ctx, func() {
			_ = closer.Close()
		})
		defer stop() // before cancel(nil): an upload that went through leaves the content to its owner
	}
	body := &uploadBody{r: io.LimitReader(f.Content, f.Size), progress: progress, watchdog: time.AfterFunc(uploadStallTimeout, func() {
		cancel(errStalled)
	})}
	defer body.watchdog.Stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return fmt.Errorf("upload request: %w", err)
	}
	req.ContentLength = f.Size
	req.Header.Set("Content-Type", mimeType)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.ep.client.Do(req)
	if err != nil {
		if sent := body.count(); sent < f.Size && body.ended() {
			return fmt.Errorf("the file changed while it was sent: it held %d of the %d bytes expected", sent, f.Size)
		}
		if cause := context.Cause(ctx); errors.Is(cause, errStalled) {
			return cause
		}
		return fmt.Errorf("send to %s: %w", c.ep.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody*4))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, errorExcerpt(text))
	}
	return nil
}

// uploadURL is the address of the upload path rel below the server's base.
// rel must be a path: an address elsewhere would carry the token away.
func (c *Conn) uploadURL(rel string) (string, error) {
	u, err := url.Parse(rel)
	if err != nil || u.Scheme != "" || u.Host != "" || !strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "//") {
		return "", fmt.Errorf("the server gave an unusable upload address %q", rel)
	}
	return strings.TrimRight(c.ep.base, "/") + rel, nil
}

// uploadBody is a file's content as the HTTP client reads it to send it:
// it counts the bytes, tells progress, and holds the stall watchdog off
// while it moves.
type uploadBody struct {
	r        io.Reader
	progress func(int64)
	watchdog *time.Timer

	mu   sync.Mutex
	sent int64
	eof  bool // the content ran out
}

func (b *uploadBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.mu.Lock()
	b.sent += int64(n)
	sent := b.sent
	if errors.Is(err, io.EOF) {
		b.eof = true
	}
	b.mu.Unlock()
	if n > 0 {
		b.watchdog.Reset(uploadStallTimeout)
		if b.progress != nil {
			b.progress(sent)
		}
	}
	return n, err //nolint:wrapcheck // the HTTP client reads io.EOF as the end
}

// count is how many bytes were read so far.
func (b *uploadBody) count() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sent
}

// ended reports whether the content ran out.
func (b *uploadBody) ended() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.eof
}

// attachmentName is a file's name as T3 stores it: on one line, trimmed,
// not blank, at most maxNameUnits long, its extension kept when it has to
// be shortened. The name goes into the agent's prompt, so a line break in
// it would read as text of the user's.
func attachmentName(name string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(name)))
	if name == "" {
		return "file"
	}
	if utf16Len(name) <= maxNameUnits {
		return name
	}
	ext := filepath.Ext(name)
	if utf16Len(ext) > 16 {
		ext = ""
	}
	stem := []rune(strings.TrimSuffix(name, ext))
	for len(stem) > 0 && utf16Len(string(stem))+utf16Len("…"+ext) > maxNameUnits {
		stem = stem[:len(stem)-1]
	}
	return strings.TrimSpace(string(stem)) + "…" + ext
}

// utf16Len is how long s is as JavaScript counts a string.
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// attachmentType is a file's media type as T3 stores it: without
// parameters, lowercase, application/octet-stream when unknown.
func attachmentType(mimeType string) string {
	t := mediaType(mimeType)
	if t == "" || len(t) > maxMimeChars {
		return "application/octet-stream"
	}
	return t
}

// mediaType is mimeType's type without its parameters, lowercase; "" when
// it is not one.
func mediaType(mimeType string) string {
	t, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return ""
	}
	return t
}
