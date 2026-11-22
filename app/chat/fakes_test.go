package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitLimit bounds every wait of a test on the pane or a fake.
const waitLimit = 2 * time.Second

// call is what the pane asked of the backend.
type call struct {
	kind     string // send, interrupt, decide, answer, dismiss or unqueue
	id       string // the turn or the prompt
	msg      Outgoing
	decision Decision
	answers  map[string]Answer
	secret   string // what ProvideSecret got
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
	maxFile  int64 // the largest file CheckAttachments takes; 0 takes any
	maxAtts  int   // the most attachments CheckAttachments takes; 0 takes any
	checked  [][]Attachment
	// uploading, when set, makes a message with files send half its first
	// file and wait for it to close, or for the send's context to end
	uploading chan struct{}
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

func (f *fakeBackend) Send(ctx context.Context, m Outgoing) error {
	f.mu.Lock()
	uploading := f.uploading
	f.mu.Unlock()
	if uploading != nil && len(m.Files) > 0 {
		first := m.Files[0]
		m.Progress(first.Name, first.Size/2, first.Size) // halfway, then it waits
		f.calls <- call{kind: "upload", msg: m}
		select {
		case <-uploading:
		case <-ctx.Done():
			return fmt.Errorf("upload %s: %w", first.Name, ctx.Err())
		}
	}
	return f.record(call{kind: "send", msg: m})
}

func (f *fakeBackend) Unqueue(_ context.Context, messageID string) error {
	return f.record(call{kind: "unqueue", id: messageID})
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

func (f *fakeBackend) ProvideSecret(_ context.Context, requestID, secret string) error {
	return f.record(call{kind: "secret", id: requestID, secret: secret})
}

func (f *fakeBackend) DeclineSecret(_ context.Context, requestID string) error {
	return f.record(call{kind: "decline secret", id: requestID})
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

// CheckAttachments takes empty files and a message of any size, up to the
// limits the test set.
func (f *fakeBackend) CheckAttachments(atts []Attachment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checked = append(f.checked, slices.Clone(atts))
	last := atts[len(atts)-1]
	switch {
	case last.Kind == AttachedFile && last.Size == 0:
		return fmt.Errorf("%s is empty", last.Name)
	case f.maxFile > 0 && last.Size > f.maxFile:
		return fmt.Errorf("%s is too large to send", last.Name)
	case f.maxAtts > 0 && len(atts) > f.maxAtts:
		return fmt.Errorf("up to %d attachments a message", f.maxAtts)
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

// fakeFile is a file a test made: an image, or any other file with its
// content, or one that Stat turns down with err.
type fakeFile struct {
	image   *Image
	mime    string
	content string
	err     error
	opened  *int // counts Open, when set
}

// imageFile is an image file holding img.
func imageFile(img Image) fakeFile {
	return fakeFile{image: &img}
}

// fakeFiles are the files a test made, by path.
type fakeFiles map[string]fakeFile

func (f fakeFiles) Stat(path string) (FileInfo, error) {
	ff, ok := f[path]
	switch {
	case !ok:
		return FileInfo{}, fmt.Errorf("attach: stat %s: %w", path, fs.ErrNotExist)
	case ff.err != nil:
		return FileInfo{}, ff.err
	case ff.image != nil:
		return FileInfo{Name: ff.image.Name, Size: int64(len(ff.image.Data)), MIME: ff.image.MIME, Image: true}, nil
	}
	return FileInfo{Name: filepath.Base(path), Size: int64(len(ff.content)), MIME: ff.mime}, nil
}

func (f fakeFiles) ReadImage(path string, check func(name string, size int64) error) (Image, error) {
	ff, ok := f[path]
	if !ok || ff.image == nil {
		return Image{}, fmt.Errorf("attach: %w", fs.ErrNotExist)
	}
	if err := check(ff.image.Name, int64(len(ff.image.Data))); err != nil {
		return Image{}, fmt.Errorf("attach: %w", err)
	}
	return *ff.image, nil
}

func (f fakeFiles) Open(path string) (io.ReadCloser, int64, error) {
	ff, ok := f[path]
	if !ok {
		return nil, 0, fmt.Errorf("attach: open %s: %w", path, fs.ErrNotExist)
	}
	if ff.opened != nil {
		*ff.opened++
	}
	return io.NopCloser(strings.NewReader(ff.content)), int64(len(ff.content)), nil
}

// fileContent reads what an attached file holds when it is sent.
func fileContent(t *testing.T, f File) string {
	t.Helper()
	rc, size, err := f.Open()
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, int64(len(b)), size)
	return string(b)
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
	return startPaneOn(t, f, f, w, h)
}

// startPaneOn is startPane with b, a backend that delivers through f.
func startPaneOn(t *testing.T, b Backend, f *fakeBackend, w, h int) *Pane {
	t.Helper()
	p := New(b, "Fix login", w, h, Config{NoColor: true, Clipboard: f.clip, Files: f.files})
	t.Cleanup(p.Close)
	awaitShown(t, p, f)
	return p
}

// imagesOnly is a backend that takes no files besides images: it is no
// FileTaker.
type imagesOnly struct {
	Backend
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
