package t3client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uploadServer is a T3 server served under /t3, as behind a reverse proxy:
// its socket mints upload addresses and deletes uploads, and its upload
// route takes the posts.
type uploadServer struct {
	t      *testing.T
	ws     *fakeServer
	minted chan map[string]any // createUploadUrl payloads
	posted chan postedUpload
	// receive handles a post; nil stores it whole and answers 204
	receive func(w http.ResponseWriter, r *http.Request)
	// address is the upload address minted for an attachment id
	address func(id string) string

	mu      sync.Mutex
	deleted []string
}

// postedUpload is one post the upload route took.
type postedUpload struct {
	path, auth, mimeType string
	length               int64
	body                 []byte
}

func startUploadServer(t *testing.T) (*uploadServer, *Conn) {
	t.Helper()
	u := &uploadServer{t: t, minted: make(chan map[string]any, 4), posted: make(chan postedUpload, 4),
		address: func(id string) string {
			return "/api/attachments/upload/" + id + "-token.sig"
		}}
	u.ws = &fakeServer{t: t, token: "tok", handler: u.rpc}
	mux := http.NewServeMux()
	mux.Handle("/t3/ws", u.ws)
	mux.HandleFunc("POST /t3/api/attachments/upload/", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		receive := u.receive
		u.mu.Unlock()
		if receive != nil {
			receive(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		u.posted <- postedUpload{path: r.URL.Path, auth: r.Header.Get("Authorization"), mimeType: r.Header.Get("Content-Type"),
			length: r.ContentLength, body: body}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, directEndpoint(srv.URL+"/t3"), "tok")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = c.close()
	})
	return u, c
}

func (u *uploadServer) rpc(ws *websocket.Conn, req map[string]any) {
	payload, _ := req["payload"].(map[string]any)
	switch req["tag"] {
	case "attachments.createUploadUrl":
		id := "pending-" + strings.ReplaceAll(payload["name"].(string), ".", "-")
		u.minted <- payload
		u.mu.Lock()
		address := u.address
		u.mu.Unlock()
		send(u.t, ws, success(req["id"], map[string]any{"attachmentId": id, "relativeUrl": address(id), "expiresAt": 1}))
	case "attachments.delete":
		u.mu.Lock()
		u.deleted = append(u.deleted, payload["attachmentId"].(string))
		u.mu.Unlock()
		send(u.t, ws, success(req["id"], nil))
	}
}

// set changes how posts are handled and which addresses are minted; nil
// leaves one as it is.
func (u *uploadServer) set(receive func(http.ResponseWriter, *http.Request), address func(string) string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if receive != nil {
		u.receive = receive
	}
	if address != nil {
		u.address = address
	}
}

// deletes are the uploads deleted so far.
func (u *uploadServer) deletes() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.deleted...)
}

func TestConn_UploadFile(t *testing.T) {
	u, c := startUploadServer(t)
	content := strings.Repeat("report ", 1000)
	var mu sync.Mutex
	var progress []int64
	up, err := c.UploadFile(context.Background(), FileUpload{Name: " report v2.pdf ", MimeType: "Application/PDF; charset=binary", Size: int64(len(content)),
		Content: strings.NewReader(content)}, func(sent int64) {
		mu.Lock()
		defer mu.Unlock()
		progress = append(progress, sent)
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"type": "file", "name": "report v2.pdf", "mimeType": "application/pdf", "sizeBytes": float64(len(content))},
		<-u.minted, "the address is asked for with the name and type T3 stores")
	posted := <-u.posted
	assert.Equal(t, "/t3/api/attachments/upload/pending-report v2-pdf-token.sig", posted.path, "posted below the server's base path")
	assert.Equal(t, "Bearer tok", posted.auth, "with the connection's token")
	assert.Equal(t, "application/pdf", posted.mimeType)
	assert.Equal(t, int64(len(content)), posted.length, "the length is declared, not chunked")
	assert.Equal(t, content, string(posted.body))
	mu.Lock()
	assert.Equal(t, int64(len(content)), progress[len(progress)-1], "progress reaches the size")
	mu.Unlock()

	assert.Equal(t, "pending-report v2-pdf", up.ID)
	assert.JSONEq(t, `{"type":"file","id":"pending-report v2-pdf","name":"report v2.pdf","mimeType":"application/pdf","sizeBytes":7000}`, string(up.Attachment),
		"what the message carries for the file")
	assert.Empty(t, u.deletes(), "an upload that went through is the message's")

	require.NoError(t, c.DeleteUpload(context.Background(), up.ID))
	assert.Equal(t, []string{up.ID}, u.deletes())
}

func TestConn_UploadFileStreams(t *testing.T) {
	u, c := startUploadServer(t)
	firstHalf := make(chan struct{})
	u.set(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4)
		if _, err := io.ReadFull(r.Body, buf); err != nil || string(buf) != "head" {
			http.Error(w, "no head", http.StatusBadRequest)
			return
		}
		close(firstHalf) // the start arrived while the rest is not even written
		rest, _ := io.ReadAll(r.Body)
		u.posted <- postedUpload{body: append(buf, rest...)}
		w.WriteHeader(http.StatusNoContent)
	}, nil)
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("head"))
		select {
		case <-firstHalf:
			_, _ = pw.Write([]byte("tail"))
			_ = pw.Close()
		case <-time.After(5 * time.Second):
			_ = pw.CloseWithError(errors.New("the body was held back until it was whole"))
		}
	}()
	_, err := c.UploadFile(context.Background(), FileUpload{Name: "big.bin", Size: 8, Content: pr}, nil)
	require.NoError(t, err)
	assert.Equal(t, "headtail", string((<-u.posted).body), "the content goes as it is read, not held whole first")
	assert.Equal(t, "application/octet-stream", (<-u.minted)["mimeType"], "a file of no known type")
}

func TestConn_UploadFileRefusedBeforeAnyCall(t *testing.T) {
	u, c := startUploadServer(t)
	_, err := c.UploadFile(context.Background(), FileUpload{Name: "empty.txt", Size: 0, Content: strings.NewReader("")}, nil)
	require.EqualError(t, err, "empty.txt is empty; T3 takes no empty files")
	_, err = c.UploadFile(context.Background(), FileUpload{Name: "huge.iso", Size: MaxFileBytes + 1, Content: strings.NewReader("")}, nil)
	require.EqualError(t, err, "huge.iso is 50.0 MB; T3 takes files up to 50 MB")
	assert.Empty(t, u.minted, "nothing is asked of the server for a file it would refuse")
}

func TestConn_UploadFileFailureDeletesTheUpload(t *testing.T) {
	u, c := startUploadServer(t)
	u.set(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Body was 3 bytes, expected 5.", http.StatusBadRequest)
	}, nil)
	_, err := c.UploadFile(context.Background(), FileUpload{Name: "a.txt", Size: 5, Content: strings.NewReader("hello")}, nil)
	require.ErrorContains(t, err, "upload a.txt: HTTP 400: Body was 3 bytes, expected 5.")
	require.Eventually(t, func() bool {
		return assert.ObjectsAreEqual([]string{"pending-a-txt"}, u.deletes())
	}, waitLimit, 5*time.Millisecond, "what the server kept of a failed upload is deleted")
}

func TestConn_UploadFileChangedWhileSent(t *testing.T) {
	u, c := startUploadServer(t)
	_, err := c.UploadFile(context.Background(), FileUpload{Name: "shrunk.log", Size: 10, Content: strings.NewReader("12345")}, nil)
	require.EqualError(t, err, "upload shrunk.log: the file changed while it was sent: it held 5 of the 10 bytes expected")
	require.Eventually(t, func() bool {
		return len(u.deletes()) == 1
	}, waitLimit, 5*time.Millisecond)

	// a file grown since it was measured sends the size it was asked for
	_, err = c.UploadFile(context.Background(), FileUpload{Name: "grown.log", Size: 3, Content: strings.NewReader("123456")}, nil)
	require.NoError(t, err)
	var got postedUpload
	for got.body == nil {
		got = <-u.posted
	}
	assert.Equal(t, "123", string(got.body))
}

func TestConn_UploadFileCancelled(t *testing.T) {
	u, c := startUploadServer(t)
	started := make(chan struct{})
	u.set(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}, nil)
	pr, pw := io.Pipe()
	defer pw.Close()
	go func() {
		_, _ = pw.Write([]byte("start"))
		close(started)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := c.UploadFile(ctx, FileUpload{Name: "slow.bin", Size: 1 << 20, Content: pr}, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Eventually(t, func() bool {
		return assert.ObjectsAreEqual([]string{"pending-slow-bin"}, u.deletes())
	}, waitLimit, 5*time.Millisecond, "a cancelled upload is deleted too")
}

func TestConn_UploadFileStalled(t *testing.T) {
	old := uploadStallTimeout
	uploadStallTimeout = 100 * time.Millisecond
	defer func() { uploadStallTimeout = old }()
	u, c := startUploadServer(t)
	release := make(chan struct{})
	defer close(release)
	u.set(func(http.ResponseWriter, *http.Request) {
		<-release // takes the request and never reads its body: the link is stuck
	}, nil)
	_, err := c.UploadFile(context.Background(), FileUpload{Name: "stuck.bin", Size: MaxFileBytes, Content: io.LimitReader(zeros{}, MaxFileBytes)}, nil)
	require.ErrorIs(t, err, errStalled, "a transfer that stops moving ends")
	require.Eventually(t, func() bool {
		return len(u.deletes()) == 1
	}, waitLimit, 5*time.Millisecond)

	// a source that stops giving data ends the same way
	pr, pw := io.Pipe()
	defer pw.Close()
	go func() {
		_, _ = pw.Write([]byte("then nothing"))
	}()
	_, c = startUploadServer(t)
	_, err = c.UploadFile(context.Background(), FileUpload{Name: "stuck2.bin", Size: 1 << 20, Content: pr}, nil)
	require.ErrorIs(t, err, errStalled)
}

// zeros is an endless run of zero bytes.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestConn_UploadFileRefusesAnAddressElsewhere(t *testing.T) {
	u, c := startUploadServer(t)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the token went to another server")
	}))
	defer elsewhere.Close()
	for _, addr := range []string{elsewhere.URL + "/api/attachments/upload/x", "//" + strings.TrimPrefix(elsewhere.URL, "http://") + "/x", "api/x"} {
		u.set(nil, func(string) string {
			return addr
		})
		_, err := c.UploadFile(context.Background(), FileUpload{Name: "a.txt", Size: 1, Content: strings.NewReader("a")}, nil)
		require.ErrorContains(t, err, "unusable upload address", addr)
	}
}

func TestValidateAttachments(t *testing.T) {
	many := make([]AttachmentSize, MaxAttachments+1)
	require.EqualError(t, ValidateAttachments(many), "T3 takes up to 100 attachments per message")
	require.NoError(t, ValidateAttachments(many[:MaxAttachments]))

	nine := make([]AttachmentSize, 9)
	for i := range nine {
		nine[i] = AttachmentSize{Image: true, Size: MaxImageBytes}
	}
	require.ErrorContains(t, ValidateAttachments(nine), "images take up to 80 MB per message together")
	require.NoError(t, ValidateAttachments(nine[:8]))
	require.ErrorContains(t, ValidateAttachments([]AttachmentSize{{MimeType: "image/png", Size: 45 << 20}, {MimeType: "IMAGE/JPEG", Size: 45 << 20}}),
		"80 MB", "files of an image type count with the images")
	require.NoError(t, ValidateAttachments([]AttachmentSize{{MimeType: "application/pdf", Size: 45 << 20}, {MimeType: "text/plain", Size: 45 << 20}}),
		"other files have no total")
}

func TestAttachmentName(t *testing.T) {
	assert.Equal(t, "report.pdf", attachmentName("  report.pdf\t"))
	assert.Equal(t, "evil_name_.txt", attachmentName("evil\nname\x1b.txt"), "no line breaks or escapes reach the agent's prompt")
	assert.Equal(t, "file", attachmentName(" \n"))
	long := strings.Repeat("é", 300) + ".tar.gz"
	got := attachmentName(long)
	assert.LessOrEqual(t, utf16Len(got), maxNameUnits)
	assert.True(t, strings.HasSuffix(got, "….gz"), got)
	emoji := strings.Repeat("😀", 200) // two UTF-16 units each
	assert.LessOrEqual(t, utf16Len(attachmentName(emoji)), maxNameUnits)

	assert.Equal(t, "text/plain", attachmentType("text/plain; charset=utf-8"))
	assert.Equal(t, "application/octet-stream", attachmentType(""))
	assert.Equal(t, "application/octet-stream", attachmentType("not a type"))
	assert.Equal(t, "application/octet-stream", attachmentType("application/"+strings.Repeat("x", 100)))
}

// waitLimit bounds a wait on the cleanup that runs in the background.
const waitLimit = 2 * time.Second
