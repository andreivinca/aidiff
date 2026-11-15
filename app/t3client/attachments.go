package t3client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// MaxImageBytes is T3's limit for one image attachment.
const MaxImageBytes = 10 << 20

// ImageUpload is an image to send with a message.
type ImageUpload struct {
	Name     string
	MimeType string
	Data     []byte
}

// PersistImages stores images for the message about to be sent under
// messageID and returns the attachments that message then carries.
func PersistImages(ctx context.Context, c *Conn, threadID, messageID string, images []ImageUpload) ([]json.RawMessage, error) {
	uploads := make([]map[string]any, 0, len(images))
	for _, img := range images {
		if len(img.Data) > MaxImageBytes {
			return nil, fmt.Errorf("%s is %d MB; T3 takes images up to %d MB", img.Name, len(img.Data)>>20, MaxImageBytes>>20)
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
	if err := c.Call(ctx, "assets.persistChatAttachments", payload, &res); err != nil {
		return nil, err
	}
	return res.Attachments, nil
}

// NewMessageID mints the id of a message the client is about to send, so
// its attachments can be stored under it first.
func NewMessageID() string { return newID() }

// MessageCommandWith is MessageCommand with a chosen message id and the
// attachments PersistImages returned for it.
func MessageCommandWith(threadID, messageID, text string, attachments []json.RawMessage) Command {
	atts := make([]any, 0, len(attachments))
	for _, a := range attachments {
		atts = append(atts, a)
	}
	return Command{
		"type":           "message.dispatch",
		"threadId":       threadID,
		"messageId":      messageID,
		"text":           text,
		"attachments":    atts,
		"createdBy":      "user",
		"creationSource": "web",
		"dispatchMode":   map[string]any{"type": "queue_after_active"},
	}
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
