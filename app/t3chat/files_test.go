package t3chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// textFile is a file attached to a message whose content is read when it
// is sent; opened counts the opens.
func textFile(name, mime, content string, opened *int) chat.File {
	return chat.File{Name: name, MIME: mime, Size: int64(len(content)), Open: func() (io.ReadCloser, int64, error) {
		if opened != nil {
			*opened++
		}
		return io.NopCloser(strings.NewReader(content)), int64(len(content)), nil
	}}
}

func TestBackend_SendUploadsFilesThenSends(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	opened := 0
	var mu sync.Mutex
	var progress []string
	m := chat.Outgoing{ID: "msg-1", Text: "see [File #1: report.pdf] [Image #1] [File #2: notes.txt]",
		Images: []chat.Image{{Name: "shot.png", MIME: "image/png", Data: []byte("png")}},
		Files:  []chat.File{textFile("report.pdf", "application/pdf", "%PDF", &opened), textFile("notes.txt", "text/plain", "notes", &opened)},
		Progress: func(name string, sent, size int64) {
			mu.Lock()
			defer mu.Unlock()
			progress = append(progress, strings.Join([]string{name, itoa(sent), itoa(size)}, " "))
		}}
	require.NoError(t, s.b.Send(context.Background(), m))

	cmd := nextCommand(t, f)
	assert.Equal(t, "see [File #1: report.pdf] [Image #1] [File #2: notes.txt]", cmd["text"], "the tokens stay, naming the files for the agent")
	atts, ok := cmd["attachments"].([]any)
	require.True(t, ok)
	require.Len(t, atts, 3)
	assert.JSONEq(t, `{"type":"image","id":"att-1","name":"shot.png"}`, string(rawOf(t, atts[0])))
	assert.JSONEq(t, `{"type":"file","id":"pending-report.pdf","name":"report.pdf"}`, string(rawOf(t, atts[1])), "the files in their order")
	assert.JSONEq(t, `{"type":"file","id":"pending-notes.txt","name":"notes.txt"}`, string(rawOf(t, atts[2])))

	f.mu.Lock()
	assert.Equal(t, []string{"upload report.pdf", "upload notes.txt", "persist", "dispatch"}, f.order,
		"the files go up first, one at a time, then the images, then the message")
	assert.Equal(t, []uploadCall{{name: "report.pdf", mimeType: "application/pdf", size: 4, content: "%PDF"},
		{name: "notes.txt", mimeType: "text/plain", size: 5, content: "notes"}}, f.uploaded)
	f.mu.Unlock()
	assert.Equal(t, 2, opened, "each file is read once, as it is sent")
	mu.Lock()
	assert.Equal(t, []string{"report.pdf 0 4", "report.pdf 4 4", "notes.txt 0 5", "notes.txt 5 5"}, progress, "progress, file by file")
	mu.Unlock()
	assert.ElementsMatch(t, []string{"pending-report.pdf", "pending-notes.txt"}, f.deletedUploads(t, 2),
		"what the server copied into the thread is not left waiting a day")
}

func TestBackend_SendFailureLeavesNoUploads(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	gone := chat.File{Name: "gone.txt", MIME: "text/plain", Size: 3, Open: func() (io.ReadCloser, int64, error) {
		return nil, 0, errors.New("attach: open /tmp/gone.txt: no such file or directory")
	}}
	m := chat.Outgoing{ID: "msg-1", Text: "two", Files: []chat.File{textFile("a.txt", "text/plain", "aaa", nil), gone}}
	err := s.b.Send(context.Background(), m)
	require.EqualError(t, err, "attach: open /tmp/gone.txt: no such file or directory", "a file deleted before the send fails it")
	assert.Equal(t, []string{"pending-a.txt"}, f.deletedUploads(t, 1), "the file that went up for it is deleted")
	assert.Empty(t, f.cmds, "and no message goes out")

	f.set(errors.New("the server refused the message"), nil)
	err = s.b.Send(context.Background(), chat.Outgoing{ID: "msg-2", Text: "one", Files: []chat.File{textFile("b.txt", "text/plain", "bbb", nil)}})
	require.EqualError(t, err, "the server refused the message")
	nextCommand(t, f)
	assert.Equal(t, []string{"pending-b.txt"}, f.deletedUploads(t, 1), "a message refused leaves no upload behind")

	f.set(nil, nil)
	f.mu.Lock()
	f.uploadErr = errors.New("upload c.txt: HTTP 500: disk full")
	f.mu.Unlock()
	err = s.b.Send(context.Background(), chat.Outgoing{ID: "msg-3", Text: "one", Files: []chat.File{textFile("c.txt", "text/plain", "ccc", nil)}})
	require.EqualError(t, err, "upload c.txt: HTTP 500: disk full")
	assert.Empty(t, f.cmds)
}

func TestBackend_SendCancelledMidUpload(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	hold := make(chan struct{})
	f.mu.Lock()
	f.uploadHold = hold
	f.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.b.Send(ctx, chat.Outgoing{ID: "msg-1", Text: "x", Files: []chat.File{textFile("big.iso", "application/octet-stream", "iso", nil)}})
	}()
	cancel()
	err := <-done
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, f.cmds, "a cancelled upload sends nothing")
	close(hold)
}

func TestBackend_SendRefusesWhatT3Refuses(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	files := make([]chat.File, t3client.MaxAttachments+1)
	for i := range files {
		files[i] = textFile("a.txt", "text/plain", "a", nil)
	}
	require.EqualError(t, s.b.Send(context.Background(), chat.Outgoing{ID: "m", Text: "x", Files: files}),
		"T3 takes up to 100 attachments per message")
	f.mu.Lock()
	assert.Empty(t, f.order, "checked before anything goes up")
	f.mu.Unlock()
}

func TestBackend_CheckAttachments(t *testing.T) {
	s := subscribe(t, newFakeServer(fixture()))
	require.NoError(t, s.b.CheckAttachments(nil))
	require.NoError(t, s.b.CheckAttachments([]chat.Attachment{{Kind: chat.AttachedFile, Name: "a.pdf", MIME: "application/pdf", Size: 1 << 20}}))
	require.EqualError(t, s.b.CheckAttachments([]chat.Attachment{{Kind: chat.AttachedFile, Name: "empty.txt", Size: 0}}),
		"empty.txt is empty; T3 takes no empty files")
	require.EqualError(t, s.b.CheckAttachments([]chat.Attachment{{Kind: chat.AttachedFile, Name: "disk.img", Size: t3client.MaxFileBytes + 1}}),
		"disk.img is 50.0 MB; T3 takes files up to 50 MB")
	require.EqualError(t, s.b.CheckAttachments([]chat.Attachment{{Kind: chat.AttachedImage, Name: "big.png", Size: t3client.MaxImageBytes + 1}}),
		"big.png is 10.0 MB; T3 takes images up to 10 MB")
	big := []chat.Attachment{{Kind: chat.AttachedFile, Name: "a.png", MIME: "image/png", Size: 45 << 20}, {Kind: chat.AttachedFile, Name: "b.png", MIME: "image/png", Size: 45 << 20}}
	require.ErrorContains(t, s.b.CheckAttachments(big), "images take up to 80 MB per message together")
}

func TestBackend_FirstMessageOfFilesSeedsTheTitle(t *testing.T) {
	snap := fixture()
	snap.VisibleTurnItems = nil
	f := newFakeServer(snap)
	s := subscribe(t, f)
	require.NoError(t, s.b.Send(context.Background(), chat.Outgoing{ID: "a", Text: "[File #1: a.txt] [Image #1]",
		Images: []chat.Image{{Name: "x.png"}}, Files: []chat.File{textFile("a.txt", "text/plain", "a", nil)}}))
	assert.Equal(t, "Attached image and file", nextCommand(t, f)["titleSeed"])
}

func TestEntries_UserMessageAttachments(t *testing.T) {
	snap := fixture()
	snap.VisibleTurnItems = []t3client.ProjectedTurnItem{{Position: 1, Item: t3client.TurnItem{ID: "u1", Type: "user_message", MessageID: "m1",
		Status: "completed", Text: "look", Attachments: t3client.Attachments{
			{Type: "image", ID: "i", Name: "shot.png", MimeType: "image/png", SizeBytes: 120},
			{Type: "file", ID: "f", Name: "report.pdf", MimeType: "application/pdf", SizeBytes: 2 << 20},
			{Type: "snapshot", ID: "s", SizeBytes: 7},
			{ID: "u", Name: "odd"},
		}}}}
	s := subscribe(t, newFakeServer(snap))
	var msg chat.UserMessage
	for _, e := range s.v.latest().Entries {
		if m, ok := e.(chat.UserMessage); ok {
			msg = m
		}
	}
	assert.Equal(t, chat.UserMessage{Text: "look", Attachments: []chat.Attachment{
		{Kind: "image", Name: "shot.png", MIME: "image/png", Size: 120},
		{Kind: "file", Name: "report.pdf", MIME: "application/pdf", Size: 2 << 20},
		{Kind: "snapshot", Name: "s", Size: 7},
		{Kind: "attachment", Name: "odd"},
	}}, msg, "every kind shows, one T3 has and this client does not know by its own name")
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// rawOf is an attachment of a dispatched message as JSON.
func rawOf(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
