package t3chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/clipboard"
	"github.com/umputun/revdiff/app/t3client"
)

// fakeServer feeds a pane a snapshot, lets tests push events, and records
// what the pane dispatches.
type fakeServer struct {
	snapshot t3client.ThreadProjection
	cmds     chan t3client.Command
	fail     error

	mu        sync.Mutex
	handlers  t3client.ThreadEvents
	clip      clipboard.Content
	clipErr   error
	persisted []persistCall
}

type persistCall struct {
	threadID, messageID string
	images              []t3client.ImageUpload
}

func (f *fakeServer) subscribe(ctx context.Context, _ *t3client.Conn, _ string, h t3client.ThreadEvents) error {
	f.mu.Lock()
	f.handlers = h
	f.mu.Unlock()
	h.Snapshot(f.snapshot)
	<-ctx.Done()
	return fmt.Errorf("subscription ended: %w", ctx.Err())
}

func (f *fakeServer) dispatch(_ context.Context, _ *t3client.Conn, cmd t3client.Command) error {
	f.cmds <- cmd
	return f.fail
}

func (f *fakeServer) catalog(context.Context, *t3client.Conn) (t3client.Catalog, error) {
	return t3client.Catalog{
		Models: []t3client.ModelChoice{
			{InstanceID: "claude", Provider: "Claude Code", Slug: "claude-opus-4", Name: "Opus 4"},
			{InstanceID: "claude", Provider: "Claude Code", Slug: "claude-sonnet-4", Name: "Sonnet 4"},
			{InstanceID: "codex", Provider: "Codex", Slug: "gpt-5", Name: "GPT-5"},
		},
		Commands: []t3client.ProviderCommand{
			{InstanceID: "claude", Name: "compact", Description: "Summarize the conversation and reduce context usage"},
			{InstanceID: "claude", Name: "review", Description: "Review a pull request"},
			{InstanceID: "codex", Name: "goal", Hint: "Objective, or pause, resume, clear"},
		},
	}, nil
}

func (f *fakeServer) clipboard(context.Context) (clipboard.Content, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clip, f.clipErr
}

func (f *fakeServer) persist(_ context.Context, _ *t3client.Conn, threadID, messageID string, images []t3client.ImageUpload) ([]json.RawMessage, error) {
	f.mu.Lock()
	f.persisted = append(f.persisted, persistCall{threadID, messageID, images})
	f.mu.Unlock()
	out := make([]json.RawMessage, 0, len(images))
	for i, img := range images {
		out = append(out, json.RawMessage(fmt.Sprintf(`{"type":"image","id":"att-%d","name":%q}`, i+1, img.Name)))
	}
	return out, nil
}

func (f *fakeServer) live() t3client.ThreadEvents {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handlers
}

func fixture() t3client.ThreadProjection {
	run := "run-2"
	return t3client.ThreadProjection{
		Runs: []t3client.Run{{ID: "run-1", Ordinal: 1, Status: "completed"}, {ID: "run-2", Ordinal: 2, Status: "running"}},
		VisibleTurnItems: []t3client.ProjectedTurnItem{
			{Position: 2, Item: t3client.TurnItem{ID: "i2", Type: "reasoning", Status: "completed", Text: "secret thoughts"}},
			{Position: 1, Item: t3client.TurnItem{ID: "i1", Type: "user_message", Status: "completed", Text: "please fix the login page"}},
			{Position: 3, Item: t3client.TurnItem{ID: "i3", Type: "command_execution", Status: "completed",
				Input: json.RawMessage(`"go test ./..."`), ExitCode: new(1)}},
			{Position: 4, Item: t3client.TurnItem{ID: "i4", Type: "file_change", Status: "completed",
				FileName: "login.go", Additions: new(3), Deletions: new(1)}},
			{Position: 5, Item: t3client.TurnItem{ID: "i5", Type: "assistant_message", Status: "running", Streaming: true,
				RunID: &run, Text: "Looking at the handler"}},
		},
	}
}

func newTestPane(t *testing.T, w, h int, snap t3client.ThreadProjection) (*Pane, *fakeServer) {
	t.Helper()
	f := &fakeServer{snapshot: snap, cmds: make(chan t3client.Command, 8)}
	p := New(&t3client.Conn{}, "th-1", "Fix login", w, h,
		Config{NoColor: true, Subscribe: f.subscribe, Dispatch: f.dispatch, Catalog: f.catalog, Clipboard: f.clipboard, Persist: f.persist})
	t.Cleanup(p.Close)
	waitUpdate(t, p) // the snapshot
	return p, f
}

// waitUpdate waits for the pane to report a change and to finish whatever
// send or fetch is in flight.
func waitUpdate(t *testing.T, p *Pane) {
	t.Helper()
	select {
	case <-p.Updates():
	case <-time.After(2 * time.Second):
		t.Fatal("no update from the pane")
	}
	waitFor(t, p, func() bool { return !p.busy && !p.catalogLoading })
}

// waitFor polls the pane's state under its lock until cond holds.
func waitFor(t *testing.T, p *Pane, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		ok := cond()
		p.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane never reached the expected state")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func nextCommand(t *testing.T, f *fakeServer) t3client.Command {
	t.Helper()
	select {
	case cmd := <-f.cmds:
		return cmd
	case <-time.After(2 * time.Second):
		t.Fatal("nothing was dispatched")
		return nil
	}
}

func view(p *Pane) string { return ansi.Strip(p.Render(true)) }

func TestPane_RendersTranscript(t *testing.T) {
	p, f := newTestPane(t, 50, 19, fixture())
	v := view(p)
	lines := strings.Split(v, "\n")
	require.Len(t, lines, 19)
	for i, l := range lines {
		assert.Equal(t, 50, ansi.StringWidth(l), "row %d keeps the width", i)
	}
	assert.Contains(t, v, "Fix login · running")
	assert.Contains(t, v, "┃  please fix the login page", "the user's text sits in a rail panel")
	assert.Contains(t, v, "+ 1 command, 1 edit, 1 thought", "the agent's work is one quiet line")
	assert.NotContains(t, v, "secret thoughts", "finished reasoning is never shown")
	assert.NotContains(t, v, "$ go test")
	assert.Contains(t, v, "   Looking at the handler▍", "the agent's prose is inset and shows a cursor while streaming")
	assert.NotContains(t, v, "▌ agent", "no role labels")
	assert.Contains(t, v, "│   █message the agent")
	assert.Less(t, strings.Index(v, "please fix the login page"), strings.Index(v, "Looking at the handler"), "items follow their position")

	p.SendText("/work\r")
	v = view(p)
	assert.Contains(t, v, "$ go test ./... ✗ exit 1", "/work shows every step")
	assert.Contains(t, v, "✎ login.go +3 −1")
	assert.Contains(t, v, "· thought")
	p.SendText("/work\r")
	assert.NotContains(t, view(p), "$ go test")

	f.live().TurnItem(t3client.TurnItem{ID: "i5", Type: "assistant_message", Status: "completed",
		Text: "## Found it\n\nThe bug is in `validate()`, see **line 12** of [login.go](/srv/app/login.go).\n\n- fix the check\n- add a test\n\n```go\nreturn ok\n```"})
	f.live().TurnItem(t3client.TurnItem{ID: "i6", Type: "checkpoint", Status: "completed"})
	f.live().Run(t3client.Run{ID: "run-2", Ordinal: 2, Status: "completed"})
	waitUpdate(t, p)
	v = view(p)
	assert.Contains(t, v, "   Found it", "a heading loses its marker")
	assert.Contains(t, v, "The bug is in validate(), see line 12 of", "inline markers and links are concealed")
	assert.Contains(t, v, "   login.go.")
	assert.NotContains(t, v, "/srv/app")
	assert.NotContains(t, v, "1 step", "a checkpoint shows nothing")
	assert.Contains(t, v, "   • fix the check", "bullets become dots")
	assert.Contains(t, v, "     return ok", "fenced code is kept, indented")
	assert.NotContains(t, v, "```")
	assert.NotContains(t, v, "▍")
	assert.Contains(t, v, "Fix login · idle")
	assert.NotContains(t, ansi.Strip(p.Render(false)), "█", "no cursor without focus")
}

func TestPane_InputAndSend(t *testing.T) {
	p, f := newTestPane(t, 60, 10, fixture())
	p.SendText("helo")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendText("l")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyEnd})
	p.SendText(" world")
	assert.Contains(t, view(p), "│   hello world█")
	p.SendText("\x17") // ctrl+w
	assert.Contains(t, view(p), "│   hello █")
	p.Paste("there\nagain")
	assert.Contains(t, view(p), "\n│   again█", "a pasted line break continues on the next row")
	p.SendText("\x1b[13;2u") // shift+enter as the terminal encodes it
	p.SendText("more")
	assert.Contains(t, view(p), "\n│   more█", "shift+enter breaks the line")
	p.SendText("\x7f\x7f\x7f\x7f\x7f") // back to "there\nagain"

	p.SendText("\r")
	cmd := nextCommand(t, f)
	assert.Equal(t, "message.dispatch", cmd["type"])
	assert.Equal(t, "hello there\nagain", cmd["text"])
	assert.Equal(t, "th-1", cmd["threadId"])
	waitUpdate(t, p)
	assert.Contains(t, view(p), "message the agent", "the input is cleared once sent")

	f.fail = errors.New("boom")
	p.SendText("retry\r")
	nextCommand(t, f)
	waitUpdate(t, p)
	v := view(p)
	assert.Contains(t, v, "send failed: boom")
	assert.Contains(t, v, "│   retry█", "a failed message stays in the input")
}

func TestPane_SlashCommands(t *testing.T) {
	p, f := newTestPane(t, 80, 12, fixture())
	p.SendText("/help\r")
	assert.Contains(t, view(p), "type / to see every command")

	p.SendText("/stop\r")
	assert.Contains(t, view(p), "Stop agent?")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendText("\r")
	cmd := nextCommand(t, f)
	assert.Equal(t, "run.interrupt", cmd["type"])
	assert.Equal(t, "run-2", cmd["runId"], "the newest active run")
	waitUpdate(t, p)

	p.SendText("/accept\r")
	assert.Contains(t, view(p), "nothing is waiting for an approval")

	f.live().TurnItem(t3client.TurnItem{ID: "i6", Type: "approval_request", Status: "waiting", RequestID: "req-1",
		Prompt: "Run rm -rf build?"})
	f.live().Request(t3client.RuntimeRequest{ID: "req-1", Kind: "command", Status: "pending"})
	waitUpdate(t, p)
	v := view(p)
	assert.Contains(t, v, "⚠ approval needed: Run rm -rf build?")
	assert.Contains(t, v, "type /accept or /decline")
	assert.Contains(t, v, "waiting for you")

	p.SendText("/accept\r")
	cmd = nextCommand(t, f)
	assert.Equal(t, "runtime-request.respond", cmd["type"])
	assert.Equal(t, "req-1", cmd["requestId"])
	assert.Equal(t, "accept", cmd["decision"])
	waitUpdate(t, p)

	p.SendText("/plan\r")
	assert.Equal(t, "plan", nextCommand(t, f)["interactionMode"])
	waitUpdate(t, p)
	p.SendText("/mode Auto\r")
	assert.Equal(t, "auto", nextCommand(t, f)["runtimeMode"])
	waitUpdate(t, p)

	p.SendText("/mode bogus\r")
	assert.Contains(t, view(p), "/mode approval-required | auto-accept-edits | auto | full-access")
	p.SendText("/compact\r")
	cmd = nextCommand(t, f)
	assert.Equal(t, "message.dispatch", cmd["type"])
	assert.Equal(t, "/compact", cmd["text"], "a command the pane does not know goes to the agent, as in T3")
	waitUpdate(t, p)

	p.SendText("/clear\r")
	assert.Contains(t, view(p), "fresh session")
	p.SendText("start over\r")
	cmd = nextCommand(t, f)
	assert.Equal(t, true, cmd["freshContext"], "/clear marks the next message")
	waitUpdate(t, p)
	p.SendText("again\r")
	assert.Nil(t, nextCommand(t, f)["freshContext"], "once")
	waitUpdate(t, p)

	p.SendText("/btw is this file used anywhere?\r")
	fork := nextCommand(t, f)
	assert.Equal(t, "thread.fork", fork["type"])
	assert.Equal(t, "th-1", fork["sourceThreadId"])
	assert.Equal(t, true, fork["sideChat"])
	question := nextCommand(t, f)
	assert.Equal(t, fork["targetThreadId"], question["threadId"], "the question goes to the side chat")
	assert.Equal(t, "is this file used anywhere?", question["text"])
	waitUpdate(t, p)
	assert.Contains(t, view(p), "side chat started")
}

func TestPane_ScrollResizeFollowClose(t *testing.T) {
	snap := fixture()
	for i := range 30 {
		snap.VisibleTurnItems = append(snap.VisibleTurnItems, t3client.ProjectedTurnItem{Position: 10 + i,
			Item: t3client.TurnItem{ID: "u" + string(rune('a'+i)), Type: "user_message", Status: "completed", Text: "line"}})
	}
	p, _ := newTestPane(t, 40, 8, snap)
	assert.NotContains(t, view(p), "↑")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyPgUp})
	assert.Contains(t, view(p), "↑", "scrolled up shows how far")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyPgDown})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyPgDown})
	assert.NotContains(t, view(p), "↑")

	p.Resize(30, 6)
	lines := strings.Split(view(p), "\n")
	require.Len(t, lines, 6)
	assert.Equal(t, 30, ansi.StringWidth(lines[0]))

	old := p.subID
	p.Follow(&t3client.Conn{}, "th-2", "Other thread")
	waitUpdate(t, p)
	p.onItem(old, t3client.TurnItem{ID: "stale", Type: "user_message", Text: "from the old subscription"})
	assert.NotContains(t, view(p), "from the old subscription", "events of a replaced subscription are dropped")
	assert.Equal(t, "th-2", p.ThreadID())
	assert.Contains(t, view(p), "Other thread")

	p.Close()
	assert.True(t, p.Exited())
	assert.Contains(t, p.QuitNote(), "agent keeps working")
}

func TestPane_FirstMessageSeedsTheTitle(t *testing.T) {
	snap := fixture()
	snap.VisibleTurnItems = nil // an empty, freshly started thread
	p, f := newTestPane(t, 60, 10, snap)
	p.SendText("Fix the login page\nIt breaks on empty passwords\r")
	cmd := nextCommand(t, f)
	assert.Equal(t, "Fix the login page", cmd["titleSeed"], "the first line names the thread")
	waitUpdate(t, p)

	f.live().TurnItem(t3client.TurnItem{ID: "u1", Type: "user_message", Status: "completed", Text: "Fix the login page"})
	f.live().Thread(t3client.Thread{ID: "th-1", Title: "Login page fix"})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "Login page fix ·", "the generated title reaches the header")
	p.SendText("and the signup page\r")
	cmd = nextCommand(t, f)
	assert.Nil(t, cmd["titleSeed"], "later messages leave the name alone")
}

func TestPane_PastesImages(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	png := []byte("\x89PNG\r\n\x1a\nfake")
	f.mu.Lock()
	f.clip = clipboard.Content{Image: png, Mime: "image/png"}
	f.mu.Unlock()

	p.SendText("look at this")
	p.SendText("\x16") // ctrl+v
	waitUpdate(t, p)
	v := view(p)
	assert.Contains(t, v, "│   look at this [Image #1] █", "the image shows as a token in the input")
	assert.Contains(t, v, "[Image #1] attached: clipboard.png, 12 B")

	f.mu.Lock()
	f.clip = clipboard.Content{Text: "and this text"}
	f.mu.Unlock()
	p.SendText("\x16")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "[Image #1] and this text█", "text on the clipboard is pasted as text")

	dir := t.TempDir()
	file := filepath.Join(dir, "shot.jpg")
	require.NoError(t, os.WriteFile(file, []byte("jpegdata"), 0o600))
	p.Paste(file)
	assert.Contains(t, view(p), "[Image #2] █", "a pasted path to an image attaches it")
	p.Paste(filepath.Join(dir, "missing.png"))
	assert.Contains(t, p.input.text(), "missing.png", "a path that is no file is pasted as text")
	p.SendText("\x17") // drop that text again

	p.SendText("\r")
	cmd := nextCommand(t, f)
	waitUpdate(t, p)
	f.mu.Lock()
	calls := f.persisted
	f.mu.Unlock()
	require.Len(t, calls, 1, "the images are stored before the message goes out")
	assert.Equal(t, "th-1", calls[0].threadID)
	assert.Equal(t, cmd["messageId"], calls[0].messageID, "under the message's own id")
	require.Len(t, calls[0].images, 2)
	assert.Equal(t, "clipboard.png", calls[0].images[0].Name)
	assert.Equal(t, png, calls[0].images[0].Data)
	assert.Equal(t, "shot.jpg", calls[0].images[1].Name)
	assert.Equal(t, "image/jpeg", calls[0].images[1].MimeType)
	assert.Len(t, cmd["attachments"], 2, "the message carries what the server stored")
	assert.Contains(t, cmd["text"], "[Image #1] and this text [Image #2]")

	p.SendText("/attach " + file + "\r")
	assert.Contains(t, view(p), "[Image #3] █")
	p.SendText("\x15") // ctrl+u clears the input, token included
	p.SendText("no image after all\r")
	cmd = nextCommand(t, f)
	waitUpdate(t, p)
	assert.Empty(t, cmd["attachments"], "a token deleted from the input drops its image")
	f.mu.Lock()
	assert.Len(t, f.persisted, 1, "nothing was stored for it")
	f.mu.Unlock()

	p.SendText("/attach " + filepath.Join(dir, "notes.txt") + "\r")
	assert.Contains(t, view(p), "not an image file")
	f.mu.Lock()
	f.clip, f.clipErr = clipboard.Content{}, clipboard.ErrNoTool
	f.mu.Unlock()
	p.SendText("\x16")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "no clipboard tool")
}
