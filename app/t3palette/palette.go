// Package t3palette is aidiff's T3 Code controller behind the ctrl+p
// palette: it connects to servers, lists and starts threads, puts a thread's
// review on screen, opens chat panes on it and messages its agent. It owns
// the T3 sessions it reaches and closes each once nothing uses it.
package t3palette

import (
	"context"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// Config is what the palette needs from the program it serves.
type Config struct {
	// Store keeps the tokens of the servers the user connects to from the
	// palette.
	Store t3client.TokenStore
	// NewReview builds the review model of r over store, the annotations it
	// starts with.
	NewReview func(r *Review, store *annotation.Store) (tea.Model, error)
	// SaveHistory files the annotations of a review leaving the screen: r is
	// that review, nil for the run's own local one. It runs in the
	// background.
	SaveHistory func(r *Review, annotations string, files []string)
	NoChat      bool // never open a chat pane along with a thread
	NoWatch     bool // no live refresh of the thread on screen
	// DiffLabel names what a review compares by default (the run's
	// --t3-source), for a review T3: Diff… did not change.
	DiffLabel string
	Chat      chat.Config // how chat panes look and what they reach on this machine
	// LocalName names this machine in the connections sidebar.
	LocalName string
	// StartStore holds the annotations of the review the run opens with.
	StartStore *annotation.Store
}

// Program receives what the palette sends outside a command: the refreshes
// of the thread on screen, a redrawn list, the opening chat pane.
// *tea.Program implements it.
type Program interface {
	Send(msg tea.Msg)
}

// dialFunc connects to the server the user named.
type dialFunc func(ctx context.Context, spec string) (Session, error)

// Palette is the T3 state behind the palette. Its sessions are the live one,
// which the commands talk to, the one of the review on screen, which may be
// an older one after the user connected elsewhere, and those of reviews on
// their way to the screen; a session none of them uses is closed. The review
// on screen changes only where the split switches workspaces (switched), so it
// always matches what the user sees.
type Palette struct {
	cfg  Config
	dial dialFunc
	// localServer reports whether T3 Code runs on this machine.
	localServer func() bool
	bg          sync.WaitGroup // background work: thread watches, closing sessions, stopping refreshes, saving history

	mu         sync.Mutex
	closed     bool
	program    Program                      // set by Start
	held       map[Session]bool             // the sessions the palette holds open
	pending    map[*Review]bool             // reviews built for the screen and not there yet
	live       Session                      // the session the commands use; nil before the first connection
	onScreen   *Review                      // the T3 review on screen; nil while the run's local review is
	stop       func()                       // live refresh of the review on screen
	chats      []*openChat                  // open chat panes; they follow the thread on screen
	shell      t3client.Shell               // the live server's projects and threads, from the last fetch
	shellAt    time.Time                    // when shell was fetched
	refreshing Session                      // the session a background fetch of shell runs for
	project    *t3client.Project            // the live server's project new threads start in; nil until chosen
	diff       diffChoice                   // what T3: Diff… chose for the reviews of its folder
	ws         string                       // the split's workspace on screen: wsKey of its review, "" for the run's local one
	parked     map[string]*Review           // the reviews of the workspaces not on screen, by key; their sessions stay open
	connected  map[Session]bool             // the newest connection to each server the user connected to; they stay open
	localOnly  bool                         // this machine is selected, with no T3 Code on it: live is nil
	stores     map[string]*annotation.Store // by reviewKey: the annotations of what was on screen
}

// New builds the palette. start is the T3 review the run opens with, nil for
// a local review; the palette owns its session from here on and ends it in
// Close.
func New(cfg Config, start *Review) *Palette {
	dial := func(ctx context.Context, spec string) (Session, error) {
		return Connect(ctx, spec, t3client.ConnectOptions{Store: cfg.Store})
	}
	return newPalette(cfg, start, dial)
}

// newPalette is New with the way palette connections are made.
func newPalette(cfg Config, start *Review, dial dialFunc) *Palette {
	t := &Palette{
		cfg:  cfg,
		dial: dial,
		localServer: func() bool {
			_, err := t3client.LocalOrigin()
			return err == nil
		},
		held:      map[Session]bool{},
		pending:   map[*Review]bool{},
		stop:      noop,
		parked:    map[string]*Review{},
		connected: map[Session]bool{},
		stores:    map[string]*annotation.Store{},
	}
	if start != nil {
		t.held[start.Session] = true
		t.live = start.Session
		t.connected[start.Session] = true
		t.onScreen = start
		t.project = &start.Target.Project
		t.ws = wsKey(start)
		t.rememberLocked(start, cfg.StartStore)
	}
	return t
}

// Start hands the palette the running program and the live refresh of the
// run's local review (nil when none), and opens the chat of the thread the
// run opened with. It is called once, before the program runs.
func (t *Palette) Start(p Program, localStop func()) {
	t.mu.Lock()
	t.program = p
	switch {
	case t.onScreen != nil:
		t.stop = t.watchLocked(t.onScreen)
	case localStop != nil:
		t.stop = localStop
	}
	autoChat := t.autoChatLocked(t.onScreen)
	t.mu.Unlock()
	t.refreshShell() // the projects sidebar lists the live server's projects
	if autoChat != nil {
		go p.Send(autoChat) // the split holds the request until the window has a size
	}
}

// Close stops the live refresh, ends every session the palette holds, and
// waits for the work it left running in the background. Whatever lands
// afterwards closes its own session.
func (t *Palette) Close() {
	t.mu.Lock()
	t.closed = true
	stop := t.stop
	t.stop = noop
	sessions := make([]Session, 0, len(t.held))
	for s := range t.held {
		sessions = append(sessions, s)
	}
	clear(t.held)
	clear(t.connected)
	clear(t.pending)
	clear(t.parked)
	t.live = nil
	t.mu.Unlock()
	stop()
	closeAll(sessions)
	t.bg.Wait()
}

// OnScreen is the T3 review on screen, nil while the run's local review is.
func (t *Palette) OnScreen() *Review {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.onScreen
}

// state is a consistent copy of what the commands show.
type state struct {
	live     Session
	onScreen *Review
	shell    t3client.Shell
	project  *t3client.Project
}

// state copies what the commands show.
func (t *Palette) state() state {
	t.mu.Lock()
	defer t.mu.Unlock()
	return state{live: t.live, onScreen: t.onScreen, shell: t.shell, project: t.project}
}

// liveSession is the session the commands use, nil before the first
// connection.
func (t *Palette) liveSession() Session {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.live
}

// adopt makes s the live session, with sh as its cached listing. With
// replacing set (a reconnect), s takes the place of that session only: when
// another one became live meanwhile, s is closed instead and adopt reports
// false. It reports false too, closing s, once the palette is closed.
func (t *Palette) adopt(s Session, sh t3client.Shell, replacing Session) bool {
	t.mu.Lock()
	if t.closed || (replacing != nil && t.live != replacing) {
		t.mu.Unlock()
		_ = s.Close()
		return false
	}
	defer t.mu.Unlock()
	if t.live == nil || t.live.Label() != s.Label() {
		t.project = nil // a project belongs to its server
	}
	t.held[s] = true
	t.live = s
	t.connectLocked(s)
	t.shell, t.shellAt = sh, time.Now()
	t.closeLaterLocked(t.takeUnusedLocked())
	return true
}

// connectLocked records s as the connection to its server, in place of an
// earlier one to the same server (a reconnect). A connection stays open
// while the user works elsewhere, so the connections sidebar can go back to
// it. mu must be held.
func (t *Palette) connectLocked(s Session) {
	for c := range t.connected {
		if c.Label() == s.Label() {
			delete(t.connected, c)
		}
	}
	t.connected[s] = true
	t.localOnly = false
}

// prepare records r as on its way to the screen, which keeps its session
// open until it gets there. It fails once that session is closed.
func (t *Palette) prepare(r *Review) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || !t.held[r.Session] {
		return false
	}
	t.pending[r] = true
	return true
}

// abandon forgets a prepared review that will not reach the screen.
func (t *Palette) abandon(r *Review) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pending, r)
	if !t.closed {
		t.closeLaterLocked(t.takeUnusedLocked())
	}
}

// note is annotations leaving the screen, filed under review (nil: the
// run's local one).
type note struct {
	review      *Review
	annotations string
	files       []string
}

// noteOf is what model leaves to history, nothing when the model coming in
// keeps its store (next).
func noteOf(review *Review, model tea.Model, next *annotation.Store) []note {
	annotations, files := leavingNotes(model, next)
	if annotations == "" {
		return nil
	}
	return []note{{review: review, annotations: annotations, files: files}}
}

// switched brings the palette in line with what a SwitchWorkspaceMsg did
// (sw): the workspace key is on screen, with r, the prepared review the
// message carried (nil when none), or the review it had when parked. It is
// the message's Done, so it runs on the UI goroutine at the moment the split
// switches, and matches what the user sees whatever happened since the
// message was built. The review left keeps its session while parked; one
// whose workspace closed, or that r replaced, files its annotations in
// history unless r keeps them (store is r's). The old refresh stops, the
// sessions nobody uses any more close and the history is saved in the
// background, in that order.
func (t *Palette) switched(key string, r *Review, store *annotation.Store, sw splitview.WorkspaceSwitch) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pending, r)
	if t.closed {
		return
	}
	prev, prevStop := t.onScreen, t.stop
	var notes []note
	on := prev
	if sw.Left != nil { // another workspace
		if prev != nil {
			t.stores[reviewKey(prev)] = storeOf(sw.Left) // kept for coming back to it
		}
		notes = append(notes, noteOf(prev, sw.Left, nil)...) // a safety net, parked or not
		if sw.LeftKept && prev != nil {
			t.parked[t.ws] = prev
		} else {
			t.dropChatsLocked(t.ws) // the split closed them with their workspace
		}
		on = t.parked[key]
		delete(t.parked, key)
	}
	if sw.Created || sw.Replaced != nil {
		if sw.Replaced != nil {
			if on != nil {
				t.stores[reviewKey(on)] = storeOf(sw.Replaced)
			}
			notes = append(notes, noteOf(on, sw.Replaced, store)...)
		}
		on = r
	}
	t.ws = key
	if on == nil || (on == prev && sw.Left == nil) {
		t.closeLaterLocked(t.takeUnusedLocked()) // nothing changed on screen
		return
	}
	t.rememberLocked(on, store)
	t.onScreen = on
	t.stop = t.watchLocked(on)
	if on.Session == t.live {
		t.project = &on.Target.Project
	}
	if on == r {
		t.followChatsLocked(r)
	} else if p := t.program; p != nil {
		t.laterLocked(func() {
			p.Send(splitview.FilesChangedMsg{}) // back from the background: catch up
		})
	}
	unused := t.takeUnusedLocked()
	t.laterLocked(func() {
		prevStop()
		closeAll(unused)
		for _, n := range notes {
			t.cfg.SaveHistory(n.review, n.annotations, n.files)
		}
	})
}

// annotated is a review model, which keeps its annotations in a store.
type annotated interface {
	Store() *annotation.Store
}

// leavingNotes are the annotations of the model leaving the screen, none
// when the model coming in keeps its store.
func leavingNotes(old tea.Model, next *annotation.Store) (annotations string, files []string) {
	m, ok := old.(annotated)
	if !ok || m.Store() == next || m.Store().Count() == 0 {
		return "", nil
	}
	return m.Store().FormatOutput(), m.Store().Files()
}

// storeOf is the annotation store of a review model, a new one for any other
// model.
func storeOf(m tea.Model) *annotation.Store {
	if r, ok := m.(annotated); ok {
		return r.Store()
	}
	return annotation.NewStore()
}

// takeUnusedLocked forgets the sessions nothing uses any more, neither live
// nor on screen nor under a review on its way there, and returns them for
// the caller to close. mu must be held.
func (t *Palette) takeUnusedLocked() []Session {
	var unused []Session
	for s := range t.held {
		if !t.usedLocked(s) {
			delete(t.held, s)
			unused = append(unused, s)
		}
	}
	return unused
}

// closeLaterLocked closes sessions in the background. mu must be held and
// the palette open.
func (t *Palette) closeLaterLocked(sessions []Session) {
	if len(sessions) == 0 {
		return
	}
	t.laterLocked(func() {
		closeAll(sessions)
	})
}

// usedLocked reports whether s is live, on screen or under a pending review.
func (t *Palette) usedLocked(s Session) bool {
	if s == t.live || t.connected[s] || (t.onScreen != nil && t.onScreen.Session == s) {
		return true
	}
	for _, r := range t.parked {
		if r.Session == s {
			return true
		}
	}
	for r := range t.pending {
		if r.Session == s {
			return true
		}
	}
	return false
}

// laterLocked runs work on a goroutine of its own, for what must not hold up
// the caller: a thread watch, closing a session, which can take seconds on a
// dead link, and saving history, which shells out to git. Close waits for it.
// mu must be held and the palette open, so that Close's wait covers the work.
func (t *Palette) laterLocked(work func()) {
	t.bg.Go(work)
}

// watchLocked starts the live refresh of r: a refresh whenever its thread's
// agent starts or finishes a run. There is none with NoWatch, before Start,
// or for a project's root. The watch runs in the background until stopped.
// mu must be held and the palette open.
func (t *Palette) watchLocked(r *Review) (stop func()) {
	if t.cfg.NoWatch || t.program == nil || r.thread() == nil {
		return noop
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := t.program
	t.laterLocked(func() {
		r.follow(ctx, p)
	})
	return cancel
}

// Place is where a new terminal opens.
type Place struct {
	SSHHost        string // the host to ssh to; empty: this machine
	SSHControlPath string // shares the T3 tunnel's connection to SSHHost; may be empty
	Dir            string // the folder; empty: where a terminal starts by default
}

// TerminalPlace is where a new terminal opens: the machine and folder of the
// T3 review on screen, else of the live server in its selected project. The
// folders of a remote server reached without SSH are not this machine's, so
// a terminal for one starts in no folder.
func (t *Palette) TerminalPlace() Place {
	t.mu.Lock()
	defer t.mu.Unlock()
	var s Session
	var dir string
	switch {
	case t.onScreen != nil:
		s, dir = t.onScreen.Session, t.onScreen.Target.Cwd
	case t.live != nil:
		s = t.live
		if t.project != nil {
			dir = t.project.WorkspaceRoot
		}
	default:
		return Place{}
	}
	if host := s.SSHHost(); host != "" {
		return Place{SSHHost: host, SSHControlPath: s.SSHControlPath(), Dir: dir}
	}
	if s.Local() {
		return Place{Dir: dir}
	}
	return Place{}
}

// closeAll ends every session in sessions.
func closeAll(sessions []Session) {
	for _, s := range sessions {
		_ = s.Close()
	}
}

// noop is the stop function of a refresh that never started.
func noop() {
}
