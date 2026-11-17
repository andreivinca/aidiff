package chat

import (
	"context"
	"strings"
	"sync"
	"time"
)

// requestTimeout bounds one message, answer or command sent to the backend.
const requestTimeout = 30 * time.Second

// The connection states the header shows in place of the agent's.
const (
	linkOffline    = "not connected"
	linkConnecting = "connecting…"
	linkLost       = "disconnected"
)

// defaultQuitNote is what the quit dialog says without Config.QuitNote.
const defaultQuitNote = "Closes the chat."

// Config tunes a pane.
type Config struct {
	NoColor   bool       // draw without colors
	Clipboard Clipboard  // what ctrl+v pastes from and a selection is copied to; nil for none
	Files     ImageFiles // the image files the user attaches; nil for none
	QuitNote  string     // what the quit dialog says about closing the pane
	// HostKeys are shortcuts the pane's host handles, such as a command
	// palette; the composer's hint row lists them with its own.
	HostKeys []Hint
	// HintsOutside leaves the hint row out of the composer: the host shows
	// the shortcuts itself (StatusHints), e.g. on its status bar.
	HintsOutside bool
}

// Hint is a shortcut as the composer's hint row shows it.
type Hint struct {
	Key   string // the key, e.g. "ctrl+p"
	Label string // what it does
}

// Pane is a live view of a backend's conversation with a composer. Its
// methods are safe for concurrent use: the split drives input and
// rendering, the backend delivers, and requests run on goroutines of their
// own.
type Pane struct {
	styles   styles // the pane's colors
	composer styles // the same colors on the composer's background
	clip     Clipboard
	files    ImageFiles
	quitNote string
	hostKeys []Hint    // the host's shortcuts for the hint row
	hintsOut bool      // the host shows the hints (Config.HintsOutside)
	builtins []command // the pane's own slash commands
	box      *mailbox  // what the backend delivered; it owns Updates

	mu             sync.Mutex
	backend        Backend // nil when not connected
	title          string  // the pane's title until the conversation names itself
	link           string  // the connection's state for the header; "" once the backend showed the conversation
	conv           Conversation
	activities     []Activity // the conversation's activity, grouped for the list
	counts         activityCounts
	pending        []queuedMessage // messages sent from here the backend has not taken over
	activity       activityView
	stop           stopConfirmation
	input          editor
	inputTop       int               // the draft's first row shown in the input box; it moves only to keep the cursor in view
	sug            int               // the selected row of the slash-command popup, kept within it where shown
	sugHidden      bool              // esc closed the popup until the input changes
	answers        map[string]Answer // answers given so far to the questions of answersFor
	answersFor     string
	questionTab    int
	questionDrafts map[int]string
	questionChoice int
	showWork       bool           // /work: the agent's steps shown one per line instead of a summary
	images         []pendingImage // images pasted into the input, each shown there as its token
	imageSeq       int
	reading        int    // clipboard and file reads in flight
	busy           bool   // a request the composer waits on is in flight
	note           string // the last failure or hint, shown until the next key
	scroll         int    // lines scrolled up from the end of the transcript; 0 follows it
	width          int
	height         int
	gen            uint64 // bumps on every visible change
	frame          frame
	rowCache       rowCache         // the transcript's rendered messages (rowcache.go)
	wheel          wheelState       // the run of wheel notches, which speeds scrolling up (wheel.go)
	now            func() time.Time // the clock the wheel reads; tests set it
	selection      textSelection
	subID          uint64 // the current subscription; what older ones deliver is dropped
	cancel         context.CancelFunc
	closed         bool
}

// frame is a Render result and what it was built from.
type frame struct {
	valid  bool
	gen    uint64
	cursor bool
	rows   []string // the pane's rows before the selection is drawn on them
	layout layout
	out    string
}

// New opens a pane on the backend's conversation; a nil backend leaves it
// not connected. title names the conversation until the backend does.
func New(b Backend, title string, width, height int, cfg Config) *Pane {
	base, composer := newStyles(!cfg.NoColor)
	p := &Pane{
		now:      time.Now,
		styles:   base,
		composer: composer,
		clip:     cfg.Clipboard,
		files:    cfg.Files,
		quitNote: cfg.QuitNote,
		hostKeys: cfg.HostKeys,
		hintsOut: cfg.HintsOutside,
		box:      newMailbox(),
		width:    max(width, 1),
		height:   max(height, 1),
	}
	if p.clip == nil {
		p.clip = noClipboard{}
	}
	if p.files == nil {
		p.files = noFiles{}
	}
	if p.quitNote == "" {
		p.quitNote = defaultQuitNote
	}
	p.builtins = p.builtinCommands()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attachLocked(b, title)
	return p
}

// Follow switches the pane to another backend and starts over; a nil
// backend leaves it not connected. The draft and its images stay.
func (p *Pane) Follow(b Backend, title string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attachLocked(b, title)
	p.changedLocked()
}

// lock takes mu and, with it, what the backend delivered since.
func (p *Pane) lock() {
	p.mu.Lock()
	conv, note := p.box.take()
	if conv != nil {
		p.showLocked(*conv)
	}
	if note != "" {
		p.note = note
		p.gen++
	}
}

// attachLocked forgets the conversation shown and subscribes to b's. mu
// must be held.
func (p *Pane) attachLocked(b Backend, title string) {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.subID++
	p.box.follow(p.subID)
	p.backend, p.title = b, strings.TrimSpace(title)
	p.clearConversationLocked()
	if b == nil || p.closed {
		p.link = linkOffline
		return
	}
	p.link = linkConnecting
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go p.subscribe(ctx, p.subID, b)
}

// clearConversationLocked forgets everything shown of the conversation; the
// draft and its images stay. mu must be held.
func (p *Pane) clearConversationLocked() {
	p.selection = textSelection{}
	p.conv, p.activities, p.counts = Conversation{}, nil, activityCounts{}
	p.pending = nil
	p.activity, p.stop = activityView{}, stopConfirmation{}
	p.answers, p.answersFor = nil, ""
	p.scroll, p.note, p.busy, p.reading = 0, "", false, 0
	p.gen++
}

// subscribe follows the backend until the subscription is replaced, the
// pane closes or the backend loses the conversation.
func (p *Pane) subscribe(ctx context.Context, sub uint64, b Backend) {
	err := b.Subscribe(ctx, delivery{box: p.box, sub: sub})
	if ctx.Err() != nil {
		return // replaced or closed
	}
	p.lock()
	defer p.mu.Unlock()
	if sub != p.subID {
		return
	}
	p.link = linkLost
	if err != nil {
		p.link = linkLost + ": " + err.Error()
	}
	p.changedLocked()
}

// showLocked takes the conversation the backend showed. mu must be held.
func (p *Pane) showLocked(c Conversation) {
	if p.link == linkConnecting {
		p.link = ""
	}
	p.conv = c
	p.activities = groupActivity(c.Activity)
	p.counts = countActivity(p.activities)
	p.prunePendingLocked()
	p.gen++
}

// goLocked runs work on a goroutine of its own and then, with mu held,
// applies the change work returned, unless the pane moved on to another
// subscription meanwhile. mu must be held.
func (p *Pane) goLocked(work func() (apply func())) {
	sub := p.subID
	go func() {
		apply := work()
		p.lock()
		defer p.mu.Unlock()
		if sub != p.subID {
			return
		}
		apply()
		p.changedLocked()
	}()
}

// changedLocked records a change that did not come from the split's own
// input and wakes the split. mu must be held.
func (p *Pane) changedLocked() {
	p.gen++
	p.box.wake()
}

// Updates delivers a value whenever the backend or a finished request
// changed the pane, and is closed once the pane is closed. Typing shows on
// the next render without one.
func (p *Pane) Updates() <-chan struct{} {
	return p.box.updates
}

// Exited reports whether the pane was closed.
func (p *Pane) Exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// QuitNote is what the quit dialog says about closing the pane.
func (p *Pane) QuitNote() string {
	return p.quitNote
}

// Resize sets the pane's size in cells.
func (p *Pane) Resize(width, height int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	width, height = max(width, 1), max(height, 1)
	if width == p.width && height == p.height {
		return
	}
	p.selection = textSelection{}
	p.width, p.height = width, height
	p.gen++
}

// Render returns exactly height lines of exactly width cells, showing what
// the backend delivered last. With showCursor the input line shows its
// cursor.
func (p *Pane) Render(showCursor bool) string {
	p.lock()
	defer p.mu.Unlock()
	if f := p.frame; f.valid && f.gen == p.gen && f.cursor == showCursor {
		return f.out
	}
	rows, l := p.renderLocked(showCursor)
	out := p.selection.draw(rows, p.width)
	p.frame = frame{valid: true, gen: p.gen, cursor: showCursor, rows: rows, layout: l, out: out}
	return out
}

// Close ends the subscription and closes Updates. Safe to call more than
// once.
func (p *Pane) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.box.close()
}
