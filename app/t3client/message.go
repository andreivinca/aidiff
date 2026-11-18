package t3client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// MaxImageBytes is T3's limit for one image attachment.
const MaxImageBytes = 10 << 20

// maxTitleRunes bounds a thread title taken from its first message.
const maxTitleRunes = 80

// OutgoingMessage is a user message for a thread's agent. It is delivered as
// T3's own client delivers it: a thread at rest starts a turn with it, and a
// busy one gets it in the running turn, between the agent's steps, when its
// provider can take it there (steering), and as its next turn otherwise.
// Nothing running is interrupted. A message that starts a fresh session
// always waits for the next turn.
type OutgoingMessage struct {
	ThreadID     string
	MessageID    string // minted with NewID; PersistImages stores attachments under it
	Text         string
	Attachments  []json.RawMessage // what PersistImages returned for MessageID
	TitleSeed    string            // names a thread still called "New thread"; see TitleSeed
	FreshContext bool              // start the agent on a fresh session, as after /clear
	Model        *ModelSelection   // run the turn with this model instead of the thread's
}

// Command is the message.dispatch command delivering m.
func (m OutgoingMessage) Command() Command {
	atts := make([]any, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		atts = append(atts, a)
	}
	cmd := Command{
		"type":           "message.dispatch",
		"threadId":       m.ThreadID,
		"messageId":      m.MessageID,
		"text":           m.Text,
		"attachments":    atts,
		"createdBy":      "user",
		"creationSource": "web",
		"dispatchMode":   map[string]any{"type": "start_immediately"},
		"deliveryIntent": "auto", // the server picks: start, steer the active turn, or queue after it
	}
	if m.TitleSeed != "" {
		cmd["titleSeed"] = m.TitleSeed
	}
	if m.FreshContext {
		cmd["freshContext"] = true
		cmd["dispatchMode"] = map[string]any{"type": "queue_after_active"}
		delete(cmd, "deliveryIntent")
	}
	if m.Model != nil {
		cmd["modelSelection"] = *m.Model
	}
	return cmd
}

// SendMessage hands text to the thread's agent; see OutgoingMessage.
func (c *Conn) SendMessage(ctx context.Context, threadID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("nothing to send")
	}
	return c.Dispatch(ctx, OutgoingMessage{ThreadID: threadID, MessageID: NewID(), Text: text}.Command())
}

// ImageUpload is an image to send with a message.
type ImageUpload struct {
	Name     string
	MimeType string
	Data     []byte
}

// Validate reports an image T3 would refuse for its size.
func (u ImageUpload) Validate() error {
	return ValidateImageSize(u.Name, int64(len(u.Data)))
}

// ValidateImageSize reports an image of size bytes that T3 would refuse, so
// a file can be checked before it is read.
func ValidateImageSize(name string, size int64) error {
	if size > MaxImageBytes {
		return fmt.Errorf("%s is %.1f MB; T3 takes images up to %d MB", name, float64(size)/(1<<20), MaxImageBytes>>20)
	}
	return nil
}

// PersistImages stores images for the message about to be sent under
// messageID and returns the attachments that message then carries.
func (c *Conn) PersistImages(ctx context.Context, threadID, messageID string, images []ImageUpload) ([]json.RawMessage, error) {
	uploads := make([]map[string]any, 0, len(images))
	for _, img := range images {
		if err := img.Validate(); err != nil {
			return nil, err
		}
		uploads = append(uploads, map[string]any{
			"type":      "image",
			"name":      img.Name,
			"mimeType":  img.MimeType,
			"sizeBytes": len(img.Data),
			"dataUrl":   "data:" + img.MimeType + ";base64," + base64.StdEncoding.EncodeToString(img.Data),
		})
	}
	var res struct {
		Attachments []json.RawMessage `json:"attachments"`
	}
	payload := map[string]any{"threadId": threadID, "messageId": messageID, "attachments": uploads}
	if err := c.call(ctx, "assets.persistChatAttachments", payload, &res); err != nil {
		return nil, err
	}
	return res.Attachments, nil
}

// TitleSeed is what a thread's first message suggests as its name, the way
// T3's composer seeds it: the message's first line, shortened; for a message
// of images only, a note of that. The server starts from the seed and then
// generates a proper title.
func TitleSeed(text string, images int) string {
	if seed := titleFrom(text); seed != "" {
		return seed
	}
	switch {
	case images == 1:
		return "Attached image"
	case images > 1:
		return "Attached images"
	}
	return ""
}

// titleFrom is the first line of msg, shortened to a title.
func titleFrom(msg string) string {
	line, _, _ := strings.Cut(msg, "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > maxTitleRunes {
		return strings.TrimSpace(string(r[:maxTitleRunes-1])) + "…"
	}
	return line
}
