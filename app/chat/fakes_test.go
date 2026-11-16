package chat

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// waitLimit bounds every wait of a test on the pane or a fake.
const waitLimit = 2 * time.Second

// call is what the pane asked of the backend.
type call struct {
	kind     string // send, interrupt, decide, answer or dismiss
	id       string // the turn or the prompt
	msg      Outgoing
	decision Decision
	answers  map[string]Answer
}

// fakeBackend shows a pane a conversation, lets a test show it more, and
// records what the pane asks of it.
type fakeBackend struct {
	initial Conversation  // shown when the pane subscribes
	shown   chan struct{} // receives once the initial conversation is shown
	calls   chan call     // what the pane asked, in order
	lost    chan error    // ends the subscription with the error sent
	clip    *fakeClipboard
	files   fakeFiles

	mu       sync.Mutex
	viewer   Viewer
	fail     error         // what the calls return
	hold     chan struct{} // when set, calls wait for it to close
	commands []Command
	maxImage int64 // the largest image CheckImage takes; 0 takes any
}

func newFakeBackend(initial Conversation) *fakeBackend {
	return &fakeBackend{initial: initial, shown: make(chan struct{}, 1), calls: make(chan call, 8), lost: make(chan error, 1),
		clip: &fakeClipboard{copied: make(chan string, 1)}, files: fakeFiles{}}
}

func (f *fakeBackend) Subscribe(ctx context.Context, v Viewer) error {
	f.mu.Lock()
	f.viewer = v
	f.mu.Unlock()
	v.Show(f.initial)
	f.shown <- struct{}{}
	select {
	case <-ctx.Done():
		return fmt.Errorf("subscription ended: %w", ctx.Err())
	case err := <-f.lost:
		return err
	}
}

// record notes a call and returns what the test set up for it.
func (f *fakeBackend) record(c call) error {
	f.calls <- c
	f.mu.Lock()
	hold, fail := f.hold, f.fail
	f.mu.Unlock()
	if hold != nil {
		<-hold
	}
	return fail
}

func (f *fakeBackend) Send(_ context.Context, m Outgoing) error {
	return f.record(call{kind: "send", msg: m})
}

func (f *fakeBackend) Interrupt(_ context.Context, turnID string) error {
	return f.record(call{kind: "interrupt", id: turnID})
}

func (f *fakeBackend) Decide(_ context.Context, promptID string, d Decision) error {
	return f.record(call{kind: "decide", id: promptID, decision: d})
}

func (f *fakeBackend) Answer(_ context.Context, promptID string, answers map[string]Answer) error {
	return f.record(call{kind: "answer", id: promptID, answers: answers})
}

func (f *fakeBackend) Dismiss(_ context.Context, promptID string) error {
	return f.record(call{kind: "dismiss", id: promptID})
}

func (f *fakeBackend) Commands() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commands
}

func (f *fakeBackend) CheckImage(name string, size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.maxImage > 0 && size > f.maxImage {
		return fmt.Errorf("%s is too large", name)
	}
	return nil
}

// set changes what later calls return and whether they wait.
func (f *fakeBackend) set(fail error, hold chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail, f.hold = fail, hold
}

// setCommands makes the backend offer commands.
func (f *fakeBackend) setCommands(commands ...Command) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = commands
}

// live is the viewer of the backend's subscription.
func (f *fakeBackend) live() Viewer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.viewer
}

// fakeClipboard holds what a paste reads and records what is copied.
type fakeClipboard struct {
	mu      sync.Mutex
	content Clip
	err     error
	copied  chan string
}

func (c *fakeClipboard) Read(context.Context) (Clip, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.content, c.err
}

func (c *fakeClipboard) Write(_ context.Context, text string) error {
	c.copied <- text
	return nil
}

func (c *fakeClipboard) set(content Clip, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.content, c.err = content, err
}

// fakeFiles are the image files a test made, by path.
type fakeFiles map[string]Image

func (f fakeFiles) IsImage(path string) bool {
	switch filepath.Ext(path) {
	case ".png", ".jpg":
		return true
	}
	return false
}

func (f fakeFiles) Read(path string, check func(name string, size int64) error) (Image, error) {
	img, ok := f[path]
	if !ok {
		return Image{}, fmt.Errorf("attach: %w", fs.ErrNotExist)
	}
	if err := check(img.Name, int64(len(img.Data))); err != nil {
		return Image{}, fmt.Errorf("attach: %w", err)
	}
	return img, nil
}

// fixture is a conversation with a running turn: the user's message, the
// agent's work and its reply as it streams.
func fixture() Conversation {
	return Conversation{
		State:   AgentRunning,
		Running: []string{"run-2"},
		Entries: []Entry{
			UserMessage{Text: "please fix the login page"},
			Step{Kind: StepThought, Text: "secret thoughts"},
			Step{Kind: StepCommand, Text: "go test ./...", State: Failed, Why: "exit 1"},
			Step{Kind: StepEdit, Text: "login.go +3 −1"},
			AgentMessage{Markdown: "Looking at the handler", Streaming: true},
		},
		Settings: Settings{Mode: "Build", Model: "Sonnet 4", Details: []string{"Claude Code", "full access"}},
	}
}

// newTestPane opens a pane on a fake backend showing conv.
func newTestPane(t *testing.T, w, h int, conv Conversation) (*Pane, *fakeBackend) {
	t.Helper()
	f := newFakeBackend(conv)
	return startPane(t, f, w, h), f
}

// startPane opens a pane on f, its clipboard and files, and takes the
// conversation f shows first.
func startPane(t *testing.T, f *fakeBackend, w, h int) *Pane {
	t.Helper()
	p := New(f, "Fix login", w, h, Config{NoColor: true, Clipboard: f.clip, Files: f.files})
	t.Cleanup(p.Close)
	awaitShown(t, p, f)
	return p
}

// follow switches the pane to b and waits for b's first conversation.
func follow(t *testing.T, p *Pane, b *fakeBackend, title string) {
	t.Helper()
	p.Follow(b, title)
	awaitShown(t, p, b)
}

// awaitShown waits for f to show its first conversation and takes the
// redraw the pane asked for, so the next update a test waits on is the
// next change.
func awaitShown(t *testing.T, p *Pane, f *fakeBackend) {
	t.Helper()
	select {
	case <-f.shown:
	case <-time.After(waitLimit):
		t.Fatal("the backend was never subscribed")
	}
	select {
	case <-p.Updates():
	default:
	}
	view(p)
}

// show makes the backend show conv and waits for the pane to hear of it.
func show(t *testing.T, p *Pane, f *fakeBackend, conv Conversation) {
	t.Helper()
	f.live().Show(conv)
	waitUpdate(t, p)
}

// waitUpdate waits for the pane to report a change.
func waitUpdate(t *testing.T, p *Pane) {
	t.Helper()
	select {
	case <-p.Updates():
	case <-time.After(waitLimit):
		t.Fatal("no update from the pane")
	}
}

// nextCall is the next thing the pane asked of the backend.
func nextCall(t *testing.T, f *fakeBackend) call {
	t.Helper()
	select {
	case c := <-f.calls:
		return c
	case <-time.After(waitLimit):
		t.Fatal("nothing was asked of the backend")
		return call{}
	}
}

// ask runs act, which asks the backend something, and waits for the
// pane to settle the reply.
func ask(t *testing.T, p *Pane, f *fakeBackend, act func()) call {
	t.Helper()
	act()
	c := nextCall(t, f)
	waitUpdate(t, p)
	return c
}

func view(p *Pane) string {
	return ansi.Strip(p.Render(true))
}

// locked runs fn with the pane's lock held, after it took what the backend
// delivered.
func locked(p *Pane, fn func()) {
	p.lock()
	defer p.mu.Unlock()
	fn()
}

var errBoom = errors.New("boom")
