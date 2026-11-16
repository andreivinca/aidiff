package t3palette

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// fakeSession is a Session that reaches nothing: its server answers from
// memory.
type fakeSession struct {
	label   string
	sshHost string
	local   bool
	notices []string
	server  *fakeServer
	redial  func() (Session, error) // nil: redialing fails

	mu     sync.Mutex
	closed int
}

func newFakeSession(label string, sh t3client.Shell) *fakeSession {
	return &fakeSession{label: label, server: &fakeServer{shell: sh}}
}

func (s *fakeSession) Label() string {
	return s.label
}

func (s *fakeSession) SSHHost() string {
	return s.sshHost
}

func (s *fakeSession) Local() bool {
	return s.local
}

func (s *fakeSession) MachineName() string {
	return s.label
}

func (s *fakeSession) Notices() []string {
	return s.notices
}

func (s *fakeSession) Server() Server {
	return s.server
}

func (s *fakeSession) Conn() *t3client.Conn {
	return nil
}

func (s *fakeSession) Redial(context.Context) (Session, error) {
	if s.redial == nil {
		return nil, errors.New("reconnect: server unreachable")
	}
	return s.redial()
}

func (s *fakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
	return nil
}

// closes is how many times the session was closed.
func (s *fakeSession) closes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// fakeServer answers the palette's requests from memory.
type fakeServer struct {
	mu       sync.Mutex
	shell    t3client.Shell
	fetchErr error
	watching []string // the threads watched, in order
	sent     []string // the messages sent
}

func (f *fakeServer) FetchShell(context.Context) (t3client.Shell, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shell, f.fetchErr
}

func (f *fakeServer) WatchThread(ctx context.Context, threadID string, _ func(t3client.Thread)) error {
	f.mu.Lock()
	f.watching = append(f.watching, threadID)
	f.mu.Unlock()
	<-ctx.Done()
	return fmt.Errorf("watch: %w", ctx.Err())
}

func (f *fakeServer) Browse(context.Context, string) (parent string, entries []t3client.DirEntry, err error) {
	return "/", nil, nil
}

func (f *fakeServer) CreateProject(_ context.Context, title, root string) (t3client.Project, error) {
	return t3client.Project{ID: "new", Title: title, WorkspaceRoot: root}, nil
}

func (f *fakeServer) DefaultModel(context.Context, t3client.Project) (t3client.ModelSelection, error) {
	return t3client.ModelSelection{}, nil
}

func (f *fakeServer) LaunchThread(context.Context, t3client.LaunchRequest) (string, error) {
	return "", errors.New("not supported")
}

func (f *fakeServer) SendMessage(_ context.Context, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}

// watched is the list of threads watched so far.
func (f *fakeServer) watched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.watching...)
}

// fakeProgram records what the palette sends.
type fakeProgram struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (p *fakeProgram) Send(msg tea.Msg) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, msg)
}

// fakeModel is a review model holding a store.
type fakeModel struct {
	tea.Model
	store  *annotation.Store
	review *Review
}

func (m fakeModel) Store() *annotation.Store {
	return m.store
}

// saved is one SaveHistory call.
type saved struct {
	review      *Review
	annotations string
}

// harness is a palette with fake dependencies, and what they recorded.
type harness struct {
	p       *Palette
	program *fakeProgram
	dials   map[string]*fakeSession // what dialing each spec reaches

	mu    sync.Mutex
	saves []saved
}

// newHarness builds a palette started like a run with start on screen (nil:
// a local review).
func newHarness(t *testing.T, cfg Config, start *Review) *harness {
	t.Helper()
	h := &harness{program: &fakeProgram{}, dials: map[string]*fakeSession{}}
	cfg.NewReview = func(r *Review, store *annotation.Store) (tea.Model, error) {
		return fakeModel{store: store, review: r}, nil
	}
	cfg.SaveHistory = func(r *Review, annotations string, _ []string) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.saves = append(h.saves, saved{review: r, annotations: annotations})
	}
	dial := func(_ context.Context, spec string) (Session, error) {
		s, ok := h.dials[spec]
		if !ok {
			return nil, errors.New("no such server")
		}
		return s, nil
	}
	h.p = newPalette(cfg, start, dial)
	h.p.Start(h.program, nil)
	t.Cleanup(h.p.Close)
	return h
}

// settle waits for the palette's background work: closes, stops, saves.
func (h *harness) settle() {
	h.p.bg.Wait()
}

// history is what SaveHistory received so far.
func (h *harness) history() []saved {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]saved(nil), h.saves...)
}

// run executes cmd and every command its messages batch or sequence, and
// returns the messages that would reach the program, in order.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	return expand(cmd())
}

// expand flattens a batch or a sequence of commands into their messages.
func expand(msg tea.Msg) []tea.Msg {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != reflect.TypeFor[tea.Cmd]() {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for i := range v.Len() {
		c, _ := reflect.TypeAssert[tea.Cmd](v.Index(i))
		out = append(out, run(c)...)
	}
	return out
}

// find is the first message of type T in msgs.
func find[T any](t *testing.T, msgs []tea.Msg) T {
	t.Helper()
	for _, m := range msgs {
		if v, ok := m.(T); ok {
			return v
		}
	}
	var zero T
	require.Failf(t, "message not found", "no %T among %v", zero, msgs)
	return zero
}

// flashes are the texts of the flash messages among msgs.
func flashes(msgs []tea.Msg) []string {
	var out []string
	for _, m := range msgs {
		if f, ok := m.(splitview.FlashMsg); ok {
			out = append(out, f.Text)
		}
	}
	return out
}

// swap does what the split does with a ReplaceInnerMsg: it retires old and
// returns the incoming model.
func swap(msg splitview.ReplaceInnerMsg, old tea.Model) tea.Model {
	msg.Retire(old)
	return msg.Model
}
