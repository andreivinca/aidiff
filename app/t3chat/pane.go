// Package t3chat shows a T3 Code thread's conversation in a pane of the split
// and lets the user message its agent. Pane implements splitview's Terminal
// interface, so it sits in the right column the way a shell does.
package t3chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"

	"github.com/umputun/revdiff/app/clipboard"
	"github.com/umputun/revdiff/app/t3client"
)

// sendTimeout bounds one message or answer to the server.
const sendTimeout = 30 * time.Second

// Config tunes a pane. The function fields exist for tests; nil means the
// t3client function of the same purpose.
type Config struct {
	CopyText  func(context.Context, string) error
	NoColor   bool
	Dispatch  func(ctx context.Context, c *t3client.Conn, cmd t3client.Command) error
	Subscribe func(ctx context.Context, c *t3client.Conn, threadID string, h t3client.ThreadEvents) error
	Catalog   func(ctx context.Context, c *t3client.Conn) (t3client.Catalog, error)
	Clipboard func(ctx context.Context) (clipboard.Content, error)
	Persist   func(ctx context.Context, c *t3client.Conn, threadID, messageID string, images []t3client.ImageUpload) ([]json.RawMessage, error)
}

// Pane is a live view of one thread with an input line. Its methods are safe
// for concurrent use: the split drives input and rendering, the connection's
// reader delivers events, and sends run on goroutines of their own.
type Pane struct {
	cfg    Config
	styles styles

	mu             sync.Mutex
	conn           *t3client.Conn
	threadID       string
	title          string
	link           string // connection state for the header; "" once the snapshot arrived
	pending        []queuedMessage
	messages       map[string]t3client.Message
	activityCounts activityCounts
	items          []t3client.TurnItem
	index          map[string]int
	runs           map[string]t3client.Run
	requests       map[string]t3client.RuntimeRequest
	activityItems  map[string]t3client.TurnItem
	subagents      map[string]t3client.Subagent
	providers      map[string]t3client.ProviderThread
	providerID     string // the active provider thread, excluding older sessions' pending work
	activity       activityView
	stop           stopConfirmation
	input          editor
	sug            int            // the selected row of the slash-command popup
	sugHidden      bool           // esc closed the popup until the input changes
	answers        map[string]any // answers given so far to the questions of answersFor
	answersFor     string
	current        *t3client.ModelSelection // the thread's model
	interaction    string                   // default (build) or plan, as reported by the thread
	runtimeMode    string                   // the thread's permission level
	catalog        *t3client.Catalog        // the server's models and provider commands, nil until fetched
	catalogLoading bool
	fresh          bool           // /clear: the next message starts a fresh provider session
	showWork       bool           // /work: the agent's commands, edits and thoughts shown one per line instead of a summary
	images         []pendingImage // images pasted into the input, each shown there as its token
	imageSeq       int
	pasting        bool   // a clipboard read is running
	busy           bool   // a send or an answer is in flight
	note           string // the last failure or hint, shown until the next key
	scroll         int    // lines scrolled up from the end of the transcript; 0 follows it
	textBottom     int    // bottom of selectable transcript and queued messages
	bodyH          int    // transcript rows at the last render, the page size
	width          int
	height         int
	gen            uint64 // bumps on every visible change
	frame          frame
	selection      textSelection
	subID          uint64 // the current subscription; events from older ones are dropped
	cancel         context.CancelFunc

	updates chan struct{}
	closed  atomic.Bool
}

// frame is a Render result and what it was built from.
type frame struct {
	valid  bool
	gen    uint64
	cursor bool
	out    string
}

// New opens a pane on the thread and starts following it.
func New(conn *t3client.Conn, threadID, title string, width, height int, cfg Config) *Pane {
	if cfg.Dispatch == nil {
		cfg.Dispatch = t3client.Dispatch
	}
	if cfg.Subscribe == nil {
		cfg.Subscribe = t3client.SubscribeThread
	}
	if cfg.Catalog == nil {
		cfg.Catalog = t3client.FetchCatalog
	}
	if cfg.Clipboard == nil {
		cfg.Clipboard = clipboard.Read
	}
	if cfg.Persist == nil {
		cfg.Persist = t3client.PersistImages
	}
	p := &Pane{cfg: cfg, styles: newStyles(!cfg.NoColor), updates: make(chan struct{}, 1)}
	p.width, p.height = max(width, 1), max(height, 1)
	p.mu.Lock()
	p.reset(conn, threadID, title)
	p.mu.Unlock()
	return p
}

// Follow switches the pane to another thread, or to the same thread over a
// new connection, and starts over.
func (p *Pane) Follow(conn *t3client.Conn, threadID, title string) {
	p.mu.Lock()
	p.reset(conn, threadID, title)
	p.mu.Unlock()
	p.signal()
}

// ThreadID is the thread the pane follows.
func (p *Pane) ThreadID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.threadID
}

// reset clears the transcript and starts a subscription; mu must be held.
func (p *Pane) reset(conn *t3client.Conn, threadID, title string) {
	p.selection = textSelection{}
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.conn, p.threadID, p.title = conn, threadID, strings.TrimSpace(title)
	p.items, p.index = nil, map[string]int{}
	p.pending, p.messages = nil, map[string]t3client.Message{}
	p.activityCounts = activityCounts{}
	p.runs, p.requests = map[string]t3client.Run{}, map[string]t3client.RuntimeRequest{}
	p.activityItems = map[string]t3client.TurnItem{}
	p.subagents = map[string]t3client.Subagent{}
	p.providers = map[string]t3client.ProviderThread{}
	p.providerID, p.activity = "", activityView{}
	p.stop = stopConfirmation{}
	p.scroll, p.note, p.busy = 0, "", false
	p.current, p.catalog, p.catalogLoading, p.fresh = nil, nil, false, false
	p.interaction, p.runtimeMode = "", ""
	p.images, p.pasting = nil, false
	p.gen++
	p.subID++
	if conn == nil || p.closed.Load() {
		p.link = "not connected"
		return
	}
	p.link = "connecting…"
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go p.subscribe(ctx, p.subID, conn, threadID)
}

// subscribe follows the thread until the subscription is replaced, the pane
// closes or the connection drops.
func (p *Pane) subscribe(ctx context.Context, id uint64, conn *t3client.Conn, threadID string) {
	err := p.cfg.Subscribe(ctx, conn, threadID, t3client.ThreadEvents{
		Message:        func(m t3client.Message) { p.onMessage(id, m) },
		Snapshot:       func(pr t3client.ThreadProjection) { p.onSnapshot(id, pr) },
		TurnItem:       func(it t3client.TurnItem) { p.onItem(id, it) },
		Run:            func(r t3client.Run) { p.onRun(id, r) },
		Request:        func(r t3client.RuntimeRequest) { p.onRequest(id, r) },
		Thread:         func(th t3client.Thread) { p.onThread(id, th) },
		Subagent:       func(a t3client.Subagent) { p.onSubagent(id, a) },
		ProviderThread: func(th t3client.ProviderThread) { p.onProviderThread(id, th) },
	})
	if ctx.Err() != nil {
		return // replaced or closed
	}
	p.mu.Lock()
	if id == p.subID {
		p.link = "disconnected"
		if err != nil {
			p.link = "disconnected: " + err.Error()
		}
		p.gen++
	}
	p.mu.Unlock()
	p.signal()
}

func (p *Pane) onSnapshot(id uint64, pr t3client.ThreadProjection) {
	p.mu.Lock()
	if id != p.subID {
		p.mu.Unlock()
		return
	}
	ordered := append([]t3client.ProjectedTurnItem(nil), pr.VisibleTurnItems...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })
	p.items, p.index = p.items[:0], map[string]int{}
	p.activityCounts.valid = false
	p.messages = map[string]t3client.Message{}
	for _, m := range pr.Messages {
		if m.Role == "user" {
			p.messages[m.ID] = m
		}
	}
	p.activityItems = map[string]t3client.TurnItem{}
	for _, it := range pr.TurnItems {
		p.activityItems[it.ID] = it
	}
	for _, it := range ordered {
		p.upsertLocked(it.Item)
	}
	p.subagents = map[string]t3client.Subagent{}
	for _, a := range pr.Subagents {
		p.subagents[a.ID] = a
	}
	p.providers = map[string]t3client.ProviderThread{}
	for _, th := range pr.ProviderThreads {
		p.providers[th.ID] = th
	}
	p.runs = map[string]t3client.Run{}
	for _, r := range pr.Runs {
		p.runs[r.ID] = r
		if !r.Active() {
			p.removePendingLocked(r.UserMessageID)
		}
	}
	p.requests = map[string]t3client.RuntimeRequest{}
	for _, r := range pr.RuntimeRequests {
		p.requests[r.ID] = r
	}
	if t := strings.TrimSpace(pr.Thread.Title); t != "" {
		p.title = t
	}
	p.threadSettingsLocked(pr.Thread)
	p.link = ""
	p.gen++
	p.mu.Unlock()
	p.signal()
}

func (p *Pane) onItem(id uint64, it t3client.TurnItem) {
	p.mu.Lock()
	if id == p.subID {
		p.upsertLocked(it)
		p.gen++
	}
	p.mu.Unlock()
	p.signal()
}

func (p *Pane) onRun(id uint64, r t3client.Run) {
	p.mu.Lock()
	if id == p.subID {
		p.runs[r.ID] = r
		if !r.Active() {
			p.removePendingLocked(r.UserMessageID)
		}
		p.activityCounts.valid = false
		p.gen++
	}
	p.mu.Unlock()
	p.signal()
}

func (p *Pane) onRequest(id uint64, r t3client.RuntimeRequest) {
	p.mu.Lock()
	if id == p.subID {
		p.requests[r.ID] = r
		p.gen++
	}
	p.mu.Unlock()
	p.signal()
}

func (p *Pane) onThread(id uint64, th t3client.Thread) {
	p.mu.Lock()
	if id == p.subID && th.ID == p.threadID {
		if t := strings.TrimSpace(th.Title); t != "" && t != p.title {
			p.title = t
			p.gen++
		}
		p.threadSettingsLocked(th)
		p.gen++
	}
	p.mu.Unlock()
	p.signal()
}

// threadSettingsLocked keeps the composer in sync with settings changed here
// or in another T3 client. Metadata events may omit unchanged fields.
func (p *Pane) threadSettingsLocked(th t3client.Thread) {
	if th.ActiveProviderThreadID != nil {
		p.providerID = *th.ActiveProviderThreadID
		p.activityCounts.valid = false
	}
	if th.ModelSelection != nil && th.ModelSelection.Model != "" {
		p.current = th.ModelSelection
		p.ensureCatalogLocked()
	}
	if th.InteractionMode != "" {
		p.interaction = th.InteractionMode
	}
	if th.RuntimeMode != "" {
		p.runtimeMode = th.RuntimeMode
	}
}

// upsertLocked replaces an item by id or appends a new one; mu must be held.
func (p *Pane) upsertLocked(it t3client.TurnItem) {
	if it.Type == "command_execution" || it.Type == "subagent" {
		p.activityCounts.valid = false
	}
	if it.Type == "user_message" && it.MessageID != "" {
		p.removePendingLocked(it.MessageID)
	}
	if previous, ok := p.activityItems[it.ID]; ok {
		if it.PromptOmitted {
			it.Prompt = previous.Prompt
		}
		if it.ResultOmitted {
			it.Result = previous.Result
		}
	}
	p.activityItems[it.ID] = it
	if i, ok := p.index[it.ID]; ok {
		p.items[i] = it
		return
	}
	p.index[it.ID] = len(p.items)
	p.items = append(p.items, it)
}

// signal wakes the split; a pending wake-up is enough.
func (p *Pane) signal() {
	select {
	case p.updates <- struct{}{}:
	default:
	}
}

// Updates delivers a value whenever the server or a finished send changed the
// pane. Typing shows on the next render without one.
func (p *Pane) Updates() <-chan struct{} { return p.updates }

// Exited reports whether the pane was closed.
func (p *Pane) Exited() bool { return p.closed.Load() }

// QuitNote is what the quit dialog says about closing the pane.
func (p *Pane) QuitNote() string {
	return "Closes the chat. The agent keeps working on the server; reopen it from the palette."
}

// SendText takes typed bytes: text goes into the input line, control bytes
// edit it (backspace, ctrl+a, ctrl+e, ctrl+u, ctrl+w) or send it (enter).
// With the slash-command popup open, tab completes the selected row, enter
// completes it and runs a complete command, esc closes the popup. Alt chords
// are dropped, except alt+enter and shift+enter, which insert a line break.
func (p *Pane) SendText(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.selection.rows != nil {
		p.selection = textSelection{}
		p.gen++
		if s == "\x1b" {
			return
		}
	}
	p.note = ""
	p.gen++
	if p.modalTextLocked(s) {
		return
	}
	if p.modifiedTextLocked(s) {
		return
	}
	for _, r := range s {
		switch r {
		case '\r':
			if !p.applySuggestionLocked(true) {
				p.submitLocked()
			}
		case '\t':
			p.applySuggestionLocked(false)
		case '\x1b':
			p.escapeLocked()
		case '\n':
			p.input.insert("\n")
			p.inputChanged()
		case 0x7f, '\b':
			p.input.backspace()
			p.inputChanged()
		case 0x01:
			p.input.home()
		case 0x05:
			p.input.end()
		case 0x16:
			p.pasteClipboardLocked()
		case 0x15:
			p.input.clear()
			p.inputChanged()
		case 0x17:
			p.input.killWord()
			p.inputChanged()
		default:
			if r >= 0x20 {
				p.input.insert(string(r))
				p.inputChanged()
			}
		}
	}
}

// modifiedTextLocked handles terminal-encoded alt chords and modified enter.
func (p *Pane) modifiedTextLocked(s string) bool {
	if !strings.HasPrefix(s, "\x1b") || len(s) <= 1 {
		return false
	}
	if s == "\x1b\r" || s == "\x1b\n" || isEnterSequence(s) {
		p.input.insert("\n")
		p.inputChanged()
	}
	return true
}

// isEnterSequence reports an enter with a modifier the terminal encoded on
// its own: CSI u ("\x1b[13;2u" is shift+enter) or xterm's modifyOtherKeys
// form ("\x1b[27;2;13~").
func isEnterSequence(s string) bool {
	return (strings.HasPrefix(s, "\x1b[13;") && strings.HasSuffix(s, "u")) ||
		(strings.HasPrefix(s, "\x1b[27;") && strings.HasSuffix(s, ";13~"))
}

// SendKey moves the cursor (left, right, home, end, delete), picks a row of
// the slash-command popup (up, down) or scrolls the transcript (up, down,
// page up, page down).
func (p *Pane) SendKey(k uv.KeyPressEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.selection = textSelection{}
	p.note = ""
	p.gen++
	if p.stop.runID != "" {
		p.stopKeyLocked(k)
		return
	}
	if p.activity.open {
		p.activityKeyLocked(k)
		return
	}
	switch k.Code {
	case uv.KeyLeft:
		if len(p.input.runes) == 0 && k.Mod == 0 {
			p.activity.open = true
		} else {
			p.input.left()
		}
	case uv.KeyRight:
		p.input.right()
	case uv.KeyHome:
		p.input.home()
	case uv.KeyEnd:
		p.input.end()
	case uv.KeyDelete:
		p.input.del()
		p.inputChanged()
	case uv.KeyUp:
		if !p.moveSuggestionLocked(-1) {
			p.scrollLocked(1)
		}
	case uv.KeyDown:
		if !p.moveSuggestionLocked(1) {
			p.scrollLocked(-1)
		}
	case uv.KeyPgUp:
		p.scrollLocked(max(1, p.bodyH-1))
	case uv.KeyPgDown:
		p.scrollLocked(-max(1, p.bodyH-1))
	}
}

// Paste inserts text at the cursor, line breaks included.
func (p *Pane) Paste(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.selection = textSelection{}
	p.note = ""
	p.gen++
	p.activity.open = false
	p.stop = stopConfirmation{}
	if path := strings.TrimSpace(s); !strings.ContainsAny(path, "\n") && p.attachFileLocked(path, false) {
		return
	}
	p.input.insert(strings.ReplaceAll(s, "\r\n", "\n"))
	p.inputChanged()
}

// SendMouse selects text or scrolls the transcript with the wheel.
func (p *Pane) SendMouse(m vt.Mouse) {
	if p.selectionMouse(m) {
		return
	}
	ev, ok := m.(uv.MouseWheelEvent)
	if !ok {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stop.runID != "" {
		return
	}
	if p.activity.open {
		switch ev.Button {
		case uv.MouseWheelUp:
			p.moveActivityLocked(-3)
		case uv.MouseWheelDown:
			p.moveActivityLocked(3)
		default:
			return
		}
		p.gen++
		return
	}
	switch ev.Button {
	case uv.MouseWheelUp:
		p.scrollLocked(3)
	case uv.MouseWheelDown:
		p.scrollLocked(-3)
	default:
		return
	}
	p.gen++
}

// scrollLocked moves the transcript n lines towards older output; the upper
// bound is applied at render time. mu must be held.
func (p *Pane) scrollLocked(n int) {
	p.selection = textSelection{}
	p.scroll = max(0, p.scroll+n)
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

// Render returns exactly height lines of exactly width cells. With showCursor
// the input line shows its cursor.
func (p *Pane) Render(showCursor bool) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if f := p.frame; f.valid && f.gen == p.gen && f.cursor == showCursor {
		return f.out
	}
	out := p.selectionRenderLocked(p.renderLocked(showCursor))
	p.frame = frame{valid: true, gen: p.gen, cursor: showCursor, out: out}
	return out
}

// Close ends the subscription. Safe to call more than once.
func (p *Pane) Close() {
	if p.closed.Swap(true) {
		return
	}
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.mu.Unlock()
	p.signal()
}

// submitLocked sends the input line, or runs its slash command; mu must be held.
func (p *Pane) submitLocked() {
	text := strings.TrimSpace(p.input.text())
	if text == "" || p.busy {
		return
	}
	if strings.HasPrefix(text, "/") {
		p.commandLocked(text)
		return
	}
	p.sendMessageLocked(text)
}

// sendMessageLocked hands text to the agent; after /clear it opens a fresh
// session. mu must be held.
func (p *Pane) sendMessageLocked(text string) {
	if p.conn == nil {
		p.note = "not connected"
		return
	}
	images := p.imagesInLocked(text)
	messageID := t3client.NewMessageID()
	cmd := t3client.MessageCommandWith(p.threadID, messageID, text, nil)
	if !p.hasUserMessageLocked() {
		if seed := t3client.TitleSeed(stripImageTokens(text), len(images)); seed != "" {
			cmd["titleSeed"] = seed // the first message names a thread still called "New thread"
		}
	}
	if p.fresh {
		cmd["freshContext"] = true
		p.fresh = false
	}
	p.pending = append(p.pending, queuedMessage{id: messageID, text: text, sending: true, queued: p.activeRunLocked() != ""})
	p.busy = true
	p.input.clear()
	p.inputChanged()
	p.scroll = 0
	p.images = nil
	go p.send(p.conn, p.subID, cmd, text, images)
}

// hasUserMessageLocked reports whether the thread already holds a message
// of the user's; mu must be held.
func (p *Pane) hasUserMessageLocked() bool {
	if len(p.pending) > 0 {
		return true
	}
	for _, m := range p.messages {
		if m.Role == "user" {
			return true
		}
	}
	for _, it := range p.items {
		if it.Type == "user_message" {
			return true
		}
	}
	return false
}

// stripImageTokens drops the "[Image #n]" tokens, which name nothing.
func stripImageTokens(text string) string {
	return strings.TrimSpace(imageToken.ReplaceAllString(text, ""))
}

// imageToken matches the tokens pasted images leave in the input.
var imageToken = regexp.MustCompile(`\[Image #\d+\]`)

// imagesInLocked is the pasted images whose token still appears in text; a
// token deleted from the input drops its image. mu must be held.
func (p *Pane) imagesInLocked(text string) []pendingImage {
	var out []pendingImage
	for _, img := range p.images {
		if strings.Contains(text, img.token) {
			out = append(out, img)
		}
	}
	return out
}

// send stores the message's images, delivers it to the agent and reports
// the outcome.
func (p *Pane) send(conn *t3client.Conn, subscription uint64, cmd t3client.Command, text string, images []pendingImage) {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	var err error
	if len(images) > 0 {
		uploads := make([]t3client.ImageUpload, 0, len(images))
		for _, img := range images {
			uploads = append(uploads, img.upload)
		}
		threadID, _ := cmd["threadId"].(string)
		messageID, _ := cmd["messageId"].(string)
		var atts []json.RawMessage
		if atts, err = p.cfg.Persist(ctx, conn, threadID, messageID, uploads); err == nil {
			cmd["attachments"] = t3client.MessageCommandWith(threadID, messageID, text, atts)["attachments"]
		}
	}
	if err == nil {
		err = p.cfg.Dispatch(ctx, conn, cmd)
	}
	p.mu.Lock()
	if subscription != p.subID {
		p.mu.Unlock()
		return
	}
	messageID, _ := cmd["messageId"].(string)
	for i := range p.pending {
		if p.pending[i].id == messageID {
			p.pending[i].sending = false
		}
	}
	p.busy = false
	if err != nil {
		p.removePendingLocked(messageID)
		p.note = "send failed: " + err.Error()
		if p.input.text() == "" {
			p.input.insert(text) // the user keeps what they typed, images included
			p.images = append(p.images, images...)
		}
	}
	p.gen++
	p.mu.Unlock()
	p.signal()
}

// pendingApprovalLocked is the newest approval the agent still waits on, or "".
func (p *Pane) pendingApprovalLocked() string {
	for _, it := range slices.Backward(p.items) {
		if it.Type != "approval_request" || it.RequestID == "" || it.Done() {
			continue
		}
		if r, known := p.requests[it.RequestID]; known && !r.Pending() {
			continue
		}
		return it.RequestID
	}
	return ""
}

// activeRunLocked is the newest run still going, or ""; mu must be held.
func (p *Pane) activeRunLocked() string {
	var latest *t3client.Run
	for id := range p.runs {
		r := p.runs[id]
		if r.Active() && (latest == nil || r.Ordinal > latest.Ordinal) {
			latest = &r
		}
	}
	if latest == nil {
		return ""
	}
	return latest.ID
}

// dispatchLocked sends a command and clears the input; mu must be held.
func (p *Pane) dispatchLocked(cmd t3client.Command, what string) {
	if p.conn == nil {
		p.note = "not connected"
		return
	}
	p.busy = true
	p.input.clear()
	go p.dispatch(p.conn, cmd, what)
}

// dispatchSeq sends commands one after another, stopping at the first
// failure, and reports the outcome.
func (p *Pane) dispatchSeq(conn *t3client.Conn, cmds []t3client.Command, what, done string) {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	var err error
	for _, cmd := range cmds {
		if err = p.cfg.Dispatch(ctx, conn, cmd); err != nil {
			break
		}
	}
	p.mu.Lock()
	p.busy = false
	p.note = done
	if err != nil {
		p.note = what + " failed: " + err.Error()
	}
	p.gen++
	p.mu.Unlock()
	p.signal()
}

// dispatch sends a command and reports a failure.
func (p *Pane) dispatch(conn *t3client.Conn, cmd t3client.Command, what string) {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	err := p.cfg.Dispatch(ctx, conn, cmd)
	p.mu.Lock()
	p.busy = false
	if err != nil {
		p.note = what + " failed: " + err.Error()
	}
	p.gen++
	p.mu.Unlock()
	p.signal()
}

// editor is the input line: runes and a cursor.
type editor struct {
	runes []rune
	cur   int
}

func (e *editor) text() string { return string(e.runes) }

func (e *editor) clear() {
	e.runes, e.cur = e.runes[:0], 0
}

func (e *editor) insert(s string) {
	r := []rune(s)
	e.runes = slices.Insert(e.runes, e.cur, r...)
	e.cur += len(r)
}

func (e *editor) backspace() {
	if e.cur > 0 {
		e.runes = append(e.runes[:e.cur-1], e.runes[e.cur:]...)
		e.cur--
	}
}

func (e *editor) del() {
	if e.cur < len(e.runes) {
		e.runes = append(e.runes[:e.cur], e.runes[e.cur+1:]...)
	}
}

func (e *editor) left() {
	if e.cur > 0 {
		e.cur--
	}
}

func (e *editor) right() {
	if e.cur < len(e.runes) {
		e.cur++
	}
}

func (e *editor) home() { e.cur = 0 }

func (e *editor) end() { e.cur = len(e.runes) }

// killWord deletes back to the start of the word before the cursor.
func (e *editor) killWord() {
	i := e.cur
	for i > 0 && unicode.IsSpace(e.runes[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(e.runes[i-1]) {
		i--
	}
	e.runes = append(e.runes[:i], e.runes[e.cur:]...)
	e.cur = i
}

// pendingImage is an image pasted into the input, shown there as its token.
type pendingImage struct {
	token  string // "[Image #1]"
	upload t3client.ImageUpload
}

// imageMimes maps the file extensions the pane attaches as images.
var imageMimes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
}

// pasteClipboardLocked reads the clipboard in the background: an image is
// attached, text is inserted. mu must be held.
func (p *Pane) pasteClipboardLocked() {
	if p.pasting {
		return
	}
	p.pasting = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, err := p.cfg.Clipboard(ctx)
		p.mu.Lock()
		p.pasting = false
		switch {
		case err != nil:
			p.note = "clipboard: " + err.Error()
		case len(c.Image) > 0:
			p.attachImageLocked("clipboard"+extFor(c.Mime), c.Mime, c.Image)
		case c.Text != "":
			p.input.insert(strings.ReplaceAll(c.Text, "\r\n", "\n"))
			p.inputChanged()
		default:
			p.note = "the clipboard is empty"
		}
		p.gen++
		p.mu.Unlock()
		p.signal()
	}()
}

// attachImageLocked keeps the image for the next message and puts its token
// into the input at the cursor; mu must be held.
func (p *Pane) attachImageLocked(name, mime string, data []byte) {
	if len(data) > t3client.MaxImageBytes {
		p.note = fmt.Sprintf("%s is %.1f MB; T3 takes images up to %d MB", name, float64(len(data))/(1<<20), t3client.MaxImageBytes>>20)
		return
	}
	p.imageSeq++
	token := fmt.Sprintf("[Image #%d]", p.imageSeq)
	p.images = append(p.images, pendingImage{token: token, upload: t3client.ImageUpload{Name: name, MimeType: mime, Data: data}})
	if cur := p.input.cur; cur > 0 && p.input.runes[cur-1] != ' ' && p.input.runes[cur-1] != '\n' {
		p.input.insert(" ")
	}
	p.input.insert(token + " ")
	p.inputChanged()
	p.note = fmt.Sprintf("%s attached: %s, %s", token, name, sizeText(len(data)))
}

// attachFileLocked attaches the image file at path when it is one and can be
// read, reporting a failure only when told to; mu must be held.
func (p *Pane) attachFileLocked(path string, report bool) bool {
	mime := imageMimes[strings.ToLower(filepath.Ext(path))]
	if mime == "" {
		if report {
			p.note = "not an image file: " + path
		}
		return false
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	data, err := os.ReadFile(path) //nolint:gosec // the user's own file, named by them
	if err != nil {
		if report {
			p.note = "attach: " + err.Error()
		}
		return report
	}
	p.attachImageLocked(filepath.Base(path), mime, data)
	return true
}

func extFor(mime string) string {
	for ext, m := range imageMimes {
		if m == mime && ext != ".jpeg" {
			return ext
		}
	}
	return ".img"
}

func sizeText(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
