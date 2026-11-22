package t3chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// waitLimit bounds every wait of a test on the backend or a fake.
const waitLimit = 2 * time.Second

// fakeServer feeds a backend a snapshot, lets tests push events, and
// records what the backend dispatches.
type fakeServer struct {
	snapshot   t3client.ThreadProjection
	cmds       chan t3client.Command
	subscribed chan struct{} // receives once a subscription had its snapshot

	mu         sync.Mutex
	handlers   t3client.ThreadEvents
	fail       error  // what Dispatch returns
	refuse     string // a command type Dispatch fails, as a server that does not know it
	catalogErr error
	catalogs   int           // FetchCatalog calls
	catalogNow chan struct{} // when set, FetchCatalog waits for it to close
	persisted  []persistCall
	uploaded   []uploadCall
	deleted    chan string   // the uploads deleted
	order      []string      // what the server was asked, in order: upload <name>, persist, dispatch
	uploadErr  error         // what UploadFile returns, after reading the content
	uploadHold chan struct{} // when set, UploadFile waits for it to close, or for its context
}

type uploadCall struct {
	name, mimeType string
	size           int64
	content        string
}

type persistCall struct {
	threadID, messageID string
	images              []t3client.ImageUpload
}

func newFakeServer(snap t3client.ThreadProjection) *fakeServer {
	return &fakeServer{snapshot: snap, cmds: make(chan t3client.Command, 8), subscribed: make(chan struct{}, 4), deleted: make(chan string, 8)}
}

func (f *fakeServer) SubscribeThread(ctx context.Context, _ string, h t3client.ThreadEvents) error {
	f.mu.Lock()
	f.handlers = h
	f.mu.Unlock()
	h.Snapshot(f.snapshot)
	f.subscribed <- struct{}{}
	<-ctx.Done()
	return fmt.Errorf("subscription ended: %w", ctx.Err())
}

func (f *fakeServer) Dispatch(_ context.Context, cmd t3client.Command) error {
	f.cmds <- cmd
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, "dispatch")
	if f.refuse != "" && cmd["type"] == f.refuse {
		return fmt.Errorf("unknown command %s", f.refuse)
	}
	return f.fail
}

// AnswerSecret records the answer as a command of its own, with the
// secrets.answerRequest payload, on the commands channel.
func (f *fakeServer) AnswerSecret(_ context.Context, threadID, itemID, secret string, decline bool) error {
	f.cmds <- t3client.Command{"type": "secrets.answerRequest", "threadId": threadID, "turnItemId": itemID, "secret": secret, "decline": decline}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fail
}

func (f *fakeServer) FetchCatalog(context.Context) (t3client.Catalog, error) {
	f.mu.Lock()
	f.catalogs++
	hold := f.catalogNow
	f.mu.Unlock()
	if hold != nil {
		<-hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.catalogErr != nil {
		return t3client.Catalog{}, f.catalogErr
	}
	return t3client.Catalog{
		Models: []t3client.ModelChoice{
			{InstanceID: "claude", Provider: "Claude Code", Slug: "claude-opus-4", Name: "Opus 4"},
			{InstanceID: "claude", Provider: "Claude Code", Slug: "claude-sonnet-4", Name: "Sonnet 4",
				Capabilities: effortCapabilities("effort")},
			{InstanceID: "codex", Provider: "Codex", Slug: "gpt-5", Name: "GPT-5", Capabilities: effortCapabilities("reasoningEffort")},
		},
		Commands: []t3client.ProviderCommand{
			{InstanceID: "claude", Name: "compact", Description: "Summarize the conversation and reduce context usage"},
			{InstanceID: "claude", Name: "review", Description: "Review a pull request"},
			{InstanceID: "codex", Name: "goal", Hint: "Objective, or pause, resume, clear"},
		},
	}, nil
}

// effortCapabilities offer low, medium and high effort under the option id.
func effortCapabilities(id string) t3client.ModelCapabilities {
	return t3client.ModelCapabilities{OptionDescriptors: []t3client.ModelOptionDescriptor{{ID: id, Type: "select",
		CurrentValue: json.RawMessage(`"medium"`), Options: []t3client.ModelOptionValue{{ID: "low"}, {ID: "medium"}, {ID: "high"}}}}}
}

// UploadFile reads the content as the server would and records it; the
// attachment is named after the file.
func (f *fakeServer) UploadFile(ctx context.Context, up t3client.FileUpload, progress func(sent int64)) (t3client.Upload, error) {
	f.mu.Lock()
	hold := f.uploadHold
	f.order = append(f.order, "upload "+up.Name)
	f.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return t3client.Upload{}, fmt.Errorf("upload %s: %w", up.Name, ctx.Err())
		}
	}
	content, err := io.ReadAll(io.LimitReader(up.Content, up.Size))
	if err != nil {
		return t3client.Upload{}, fmt.Errorf("upload %s: %w", up.Name, err)
	}
	if progress != nil {
		progress(int64(len(content)))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.uploadErr != nil {
		return t3client.Upload{}, f.uploadErr
	}
	f.uploaded = append(f.uploaded, uploadCall{name: up.Name, mimeType: up.MimeType, size: up.Size, content: string(content)})
	id := "pending-" + up.Name
	return t3client.Upload{ID: id, Attachment: json.RawMessage(fmt.Sprintf(`{"type":"file","id":%q,"name":%q}`, id, up.Name))}, nil
}

func (f *fakeServer) DeleteUpload(_ context.Context, id string) error {
	f.deleted <- id
	return nil
}

// deletedUploads waits for n uploads to be deleted and returns them.
func (f *fakeServer) deletedUploads(t *testing.T, n int) []string {
	t.Helper()
	var out []string
	for range n {
		select {
		case id := <-f.deleted:
			out = append(out, id)
		case <-time.After(waitLimit):
			t.Fatalf("only %d of %d uploads were deleted", len(out), n)
		}
	}
	return out
}

func (f *fakeServer) PersistImages(_ context.Context, threadID, messageID string, images []t3client.ImageUpload) ([]json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, "persist")
	f.persisted = append(f.persisted, persistCall{threadID, messageID, images})
	out := make([]json.RawMessage, 0, len(images))
	for i, img := range images {
		out = append(out, json.RawMessage(fmt.Sprintf(`{"type":"image","id":"att-%d","name":%q}`, i+1, img.Name)))
	}
	return out, nil
}

// live is the handlers of the latest subscription.
func (f *fakeServer) live() t3client.ThreadEvents {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handlers
}

// set changes what Dispatch and FetchCatalog return from now on.
func (f *fakeServer) set(fail, catalogErr error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail, f.catalogErr = fail, catalogErr
}

// fetches is how often the catalog was fetched.
func (f *fakeServer) fetches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.catalogs
}

// fakeViewer records what a backend shows.
type fakeViewer struct {
	shown chan chat.Conversation
	notes chan string

	mu   sync.Mutex
	last chat.Conversation
}

func newFakeViewer() *fakeViewer {
	return &fakeViewer{shown: make(chan chat.Conversation, 256), notes: make(chan string, 8)}
}

func (v *fakeViewer) Show(c chat.Conversation) {
	v.mu.Lock()
	v.last = c
	v.mu.Unlock()
	v.shown <- c
}

func (v *fakeViewer) Note(text string) {
	v.notes <- text
}

// latest is the conversation shown last.
func (v *fakeViewer) latest() chat.Conversation {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.last
}

// next waits for the next conversation shown.
func (v *fakeViewer) next(t *testing.T) chat.Conversation {
	t.Helper()
	select {
	case c := <-v.shown:
		return c
	case <-time.After(waitLimit):
		t.Fatal("the backend showed nothing")
		return chat.Conversation{}
	}
}

// subscription is a backend subscribed for a test.
type subscription struct {
	b *Backend
	v *fakeViewer
}

// subscribe opens the backend of thread th-1 on f, subscribes it for a
// recording viewer and waits for the snapshot and the catalog to be shown.
func subscribe(t *testing.T, f *fakeServer) subscription {
	t.Helper()
	return subscribeOn(t, newChats(f), f)
}

// subscribeOn is subscribe with the backends of chats, which serve f.
func subscribeOn(t *testing.T, chats *Chats, f *fakeServer) subscription {
	t.Helper()
	b, ok := chats.Thread("th-1").(*Backend)
	if !ok {
		t.Fatal("no backend")
	}
	fetching := b.catalog.unknown()
	s := subscription{b: b, v: newFakeViewer()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = b.Subscribe(ctx, s.v)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case <-f.subscribed:
	case <-time.After(waitLimit):
		t.Fatal("never subscribed")
	}
	s.v.next(t) // the snapshot
	if fetching {
		s.v.next(t) // the catalog
	}
	return s
}

// command is the backend's command named name.
func (s subscription) command(t *testing.T, name string) chat.Command {
	t.Helper()
	for _, c := range s.b.Commands() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no command %s", name)
	return chat.Command{}
}

// run runs the command typed and sends what its reply has left to send.
func (s subscription) run(t *testing.T, name, arg string) (chat.Reply, error) {
	t.Helper()
	reply, err := s.command(t, name).Run(arg)
	if err != nil || reply.Send == nil {
		return reply, err //nolint:wrapcheck // the tests check the command's own words
	}
	err = reply.Send(context.Background())
	return reply, err //nolint:wrapcheck // the tests check the command's own words
}

func nextCommand(t *testing.T, f *fakeServer) t3client.Command {
	t.Helper()
	select {
	case cmd := <-f.cmds:
		return cmd
	case <-time.After(waitLimit):
		t.Fatal("nothing was dispatched")
		return nil
	}
}

// fixture is a thread with a running turn: the user's message, the agent's
// work and its reply as it streams.
func fixture() t3client.ThreadProjection {
	run := "run-2"
	return t3client.ThreadProjection{
		Thread: t3client.Thread{ID: "th-1", Title: "Fix login"},
		Runs:   []t3client.Run{{ID: "run-1", Ordinal: 1, Status: "completed"}, {ID: "run-2", Ordinal: 2, Status: "running"}},
		VisibleTurnItems: []t3client.ProjectedTurnItem{
			{Position: 2, Item: t3client.TurnItem{ID: "i2", Type: "reasoning", Status: "completed", Text: "secret thoughts"}},
			{Position: 1, Item: t3client.TurnItem{ID: "i1", Type: "user_message", Status: "completed", Text: "please fix the login page", MessageID: "m1"}},
			{Position: 3, Item: t3client.TurnItem{ID: "i3", Type: "command_execution", Status: "completed",
				Input: json.RawMessage(`"go test ./..."`), ExitCode: new(1)}},
			{Position: 4, Item: t3client.TurnItem{ID: "i4", Type: "file_change", Status: "completed",
				FileName: "login.go", Additions: new(3), Deletions: new(1)}},
			{Position: 5, Item: t3client.TurnItem{ID: "i5", Type: "assistant_message", Status: "running", Streaming: true,
				RunID: &run, Text: "Looking at the handler"}},
			{Position: 6, Item: t3client.TurnItem{ID: "i6", Type: "checkpoint", Status: "completed"}},
		},
	}
}
