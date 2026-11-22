package chat

import (
	"errors"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPane_RendersTranscript(t *testing.T) {
	p, f := newTestPane(t, 50, 24, fixture())
	v := view(p)
	lines := strings.Split(v, "\n")
	require.Len(t, lines, 24)
	for i, l := range lines {
		assert.Equal(t, 50, ansi.StringWidth(l), "row %d keeps the width", i)
	}
	assert.Contains(t, v, "Fix login · running")
	assert.Contains(t, v, "┃  please fix the login page", "the user's text sits in a rail panel")
	assert.Contains(t, v, "+ Ran 1 command and changed 1 file", "the agent's work is one quiet line, saying what it did")
	assert.NotContains(t, v, "secret thoughts", "finished reasoning is never shown")
	assert.NotContains(t, v, "$ go test")
	assert.Contains(t, v, "   Looking at the handler▍", "the agent's prose is inset and shows a cursor while streaming")
	assert.Contains(t, v, "│   █message the agent")
	assert.Less(t, strings.Index(v, "please fix the login page"), strings.Index(v, "Looking at the handler"), "entries keep their order")

	p.SendText("/work\r")
	v = view(p)
	assert.Contains(t, v, "$ go test ./... ✗ exit 1", "/work shows every step")
	assert.Contains(t, v, "✎ login.go +3 −1")
	assert.Contains(t, v, "· thought")
	p.SendText("/work\r")
	assert.NotContains(t, view(p), "$ go test")

	conv := fixture()
	conv.State, conv.Running = AgentIdle, nil
	conv.Entries[4] = AgentMessage{Markdown: "## Found it\n\nThe bug is in `validate()`, see **line 12** of [login.go](/srv/app/login.go)." +
		"\n\n- fix the check\n- add a test\n\n```go\nreturn ok\n```"}
	show(t, p, f, conv)
	v = view(p)
	assert.Contains(t, v, "   Found it", "a heading loses its marker")
	assert.Contains(t, v, "The bug is in validate(), see line 12 of", "inline markers and links are concealed")
	assert.Contains(t, v, "   login.go.")
	assert.NotContains(t, v, "/srv/app")
	assert.Contains(t, v, "   • fix the check", "bullets become dots")
	assert.Contains(t, v, "     return ok", "fenced code is kept, indented")
	assert.NotContains(t, v, "```")
	assert.NotContains(t, v, "▍")
	assert.Contains(t, v, "Fix login · idle")
	assert.NotContains(t, ansi.Strip(p.Render(false)), "█", "no cursor without focus")
}

func TestPane_RendersEveryEntry(t *testing.T) {
	conv := Conversation{Entries: []Entry{
		Plan{Markdown: "1. read the code"},
		TodoList{Steps: []TodoStep{{Text: "read", State: Done}, {Text: "fix", State: Running}, {Text: "test", State: Pending}}},
		Failure{Text: "rate limited\nretry later"},
		Step{Kind: StepThought, Text: "pondering", State: Running},
		Step{Kind: StepSearch, Text: "*.go", State: Pending},
		Step{Kind: StepWeb, Text: "go modules", State: Waiting},
		Step{Kind: StepTool, Text: "lint ./...", State: Failed},
		Step{Kind: StepSubagent, Text: "review the change"},
		Step{Kind: StepNotice, Text: "context is almost full"},
		Step{Kind: StepInterrupt, Text: "interrupted by the user"},
		Step{Kind: StepCompaction, Text: "context compacted"},
		Step{Kind: StepOther, Text: "image generation: logo", State: Running},
	}}
	p, _ := newTestPane(t, 70, 30, conv)
	v := view(p)
	assert.Contains(t, v, "   Plan")
	assert.Contains(t, v, "1. read the code")
	assert.Contains(t, v, "☑ read")
	assert.Contains(t, v, "▸ fix")
	assert.Contains(t, v, "☐ test")
	assert.Contains(t, v, "✗ rate limited")
	assert.Contains(t, v, "\n     retry later", "a failure shows all of its text, hanging under the mark")
	assert.Contains(t, v, "⋯ Searched 2 times, used 1 tool, started 1 subagent and took 4",
		"work still going shows ⋯; thoughts count only alone; kinds without a word of their own count as steps")
	assert.Contains(t, v, "\n   other steps · /work shows them", "a long summary wraps, never cut")

	p.SendText("/work\r")
	v = view(p)
	for _, want := range []string{"· thinking… pondering", "✱ *.go …", "◈ go modules", "→ lint ./... ✗", "↳ review the change",
		"ℹ context is almost full", "■ interrupted by the user", "⇅ context compacted", "· image generation: logo …"} {
		assert.Contains(t, v, want)
	}
	assert.NotContains(t, v, "◈ go modules …", "waiting work shows no running mark")
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
	assert.True(t, p.SendModifiedKey(uv.KeyPressEvent{Code: uv.KeyEnter, Mod: uv.ModShift}), "the pane takes shift+enter")
	p.SendText("more")
	assert.Contains(t, view(p), "\n│   more█", "shift+enter breaks the line")
	p.SendText("\x7f\x7f\x7f\x7f\x7f") // back to "there\nagain"

	c := ask(t, p, f, func() {
		p.SendText("\r")
	})
	assert.Equal(t, "send", c.kind)
	assert.Equal(t, "hello there\nagain", c.msg.Text)
	assert.Len(t, c.msg.ID, 36, "the pane names the message with a UUID")
	assert.Contains(t, view(p), "message the agent", "the input is cleared once sent")

	f.set(errBoom, nil)
	ask(t, p, f, func() {
		p.SendText("retry\r")
	})
	v := view(p)
	assert.Contains(t, v, "send failed: boom")
	assert.Contains(t, v, "│   retry█", "a failed message stays in the input")
}

func TestPane_EditsWholeCharacters(t *testing.T) {
	p, _ := newTestPane(t, 60, 10, fixture())
	p.SendText("a👩‍💻b🇩🇪c")
	p.Paste("e\u0301")
	assert.Len(t, p.input.clusters, 6, "an emoji sequence, a flag and an accented letter are one character each")
	p.SendText("\x7f")
	assert.Equal(t, "a👩‍💻b🇩🇪c", p.input.text(), "backspace takes the accent with its letter")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendText("\x7f")
	assert.Equal(t, "a👩‍💻🇩🇪c", p.input.text(), "the cursor steps over a flag as one character")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDelete})
	assert.Equal(t, "a🇩🇪c", p.input.text(), "delete removes the whole emoji sequence")
	p.SendText("e")
	p.SendText("\u0301")
	assert.Equal(t, "ae\u0301🇩🇪c", p.input.text())
	assert.Equal(t, 2, p.input.cur, "a combining accent joins the letter before the cursor")
}

// longConversation is fixture with thirty more messages.
func longConversation() Conversation {
	conv := fixture()
	for range 30 {
		conv.Entries = append(conv.Entries, UserMessage{Text: "line"})
	}
	return conv
}

func TestPane_EndJumpsToTheBottom(t *testing.T) {
	p, _ := newTestPane(t, 60, 8, longConversation())
	p.SendText("draft")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyHome})
	view(p)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyPgUp})
	assert.Contains(t, view(p), "end jumps down")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyEnd})
	assert.Contains(t, view(p), "end jumps down", "with the cursor inside the draft, end first moves the cursor")
	assert.True(t, p.input.atEnd())
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyEnd})
	assert.NotContains(t, view(p), "↑", "then it jumps to the bottom")
	assert.Equal(t, "draft", p.input.text())
}

func TestPane_NotConnectedKeepsTheInput(t *testing.T) {
	p := New(nil, "Offline", 80, 12, Config{NoColor: true})
	t.Cleanup(p.Close)
	assert.Contains(t, view(p), "Offline · not connected")
	p.SendText("/side a long question worth keeping\r")
	assert.Contains(t, view(p), "not connected", "a command nobody knows goes out as a message, which cannot")
	assert.Equal(t, "/side a long question worth keeping", p.input.text())
	p.SendText("\x15hello\r")
	assert.Equal(t, "hello", p.input.text())
	assert.Equal(t, defaultQuitNote, p.QuitNote())
}

func TestPane_ScrollResizeFollowClose(t *testing.T) {
	p, f := newTestPane(t, 40, 8, longConversation())
	assert.NotContains(t, view(p), "↑")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyPgUp})
	assert.Contains(t, view(p), "↑", "scrolled up shows how far")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyPgDown})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyPgDown})
	assert.NotContains(t, view(p), "↑")
	for range 300 {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	}
	view(p)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	locked(p, func() {
		assert.Equal(t, p.frame.layout.maxScroll-1, p.scroll, "scrolling stops at the top, so one line down moves at once")
	})

	p.Resize(30, 6)
	lines := strings.Split(view(p), "\n")
	require.Len(t, lines, 6)
	assert.Equal(t, 30, ansi.StringWidth(lines[0]))

	old := f.live()
	other := newFakeBackend(Conversation{Title: "Other thread"})
	follow(t, p, other, "")
	old.Show(Conversation{Entries: []Entry{UserMessage{Text: "from the old subscription"}}})
	assert.NotContains(t, view(p), "from the old subscription", "what a replaced subscription shows is dropped")
	assert.Contains(t, view(p), "Other thread", "the backend names the conversation")

	p.Close()
	assert.True(t, p.Exited())
}

func TestPane_CloseEndsUpdates(t *testing.T) {
	p, f := newTestPane(t, 40, 8, fixture())
	p.Close()
	select {
	case _, open := <-p.Updates():
		assert.False(t, open, "a closed pane's updates are closed, so the split's wait ends")
	case <-time.After(waitLimit):
		t.Fatal("updates stay open after Close")
	}
	f.live().Show(Conversation{Entries: []Entry{AgentMessage{Markdown: "late"}}})
	f.live().Note("late")
	p.Close() // a late delivery and a second close are harmless
	assert.NotContains(t, view(p), "late")
}

func TestPane_LostSubscription(t *testing.T) {
	p, f := newTestPane(t, 60, 12, fixture())
	f.lost <- errors.New("connection reset")
	waitUpdate(t, p)
	v := view(p)
	assert.Contains(t, v, "disconnected: connection reset", "the header says why")
	assert.Contains(t, v, "Looking at the handler", "what was shown stays")
}

func TestPane_StaleRequestLeavesTheNewBackendAlone(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	release := make(chan struct{})
	f.set(errors.New("old failure"), release)
	p.SendText("on the old thread\r")
	nextCall(t, f)

	other := newFakeBackend(fixture())
	follow(t, p, other, "Other thread")
	blocked := make(chan struct{})
	other.set(nil, blocked)
	p.SendText("on the new thread\r")
	nextCall(t, other)
	close(release)
	waitUpdate(t, p)
	assert.Equal(t, "on the old thread", p.input.text(),
		"the old thread's failed message is back in the input, not lost: the draft outlives the conversation")
	assert.Contains(t, view(p), "send failed: old failure", "the note says why it is back")
	p.SendText("\x15again\r")
	assert.Contains(t, view(p), "still being sent", "the old request did not end the new one's wait")
	close(blocked)
	waitUpdate(t, p)
}

func TestPane_BackendNotes(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	f.live().Note("server catalog: offline")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "server catalog: offline")
	p.SendText("x")
	assert.NotContains(t, view(p), "server catalog", "a note lasts until the next key")
}

func TestPane_QuitNote(t *testing.T) {
	p := New(nil, "", 10, 5, Config{QuitNote: "Closes the chat; the agent keeps working."})
	t.Cleanup(p.Close)
	assert.Equal(t, "Closes the chat; the agent keeps working.", p.QuitNote())
	assert.Contains(t, view(p), defaultTitle)
}

func TestPane_SendModifiedKeyTakesOnlyModifiedEnter(t *testing.T) {
	p, _ := newTestPane(t, 60, 10, fixture())
	assert.False(t, p.SendModifiedKey(uv.KeyPressEvent{Code: uv.KeyEnter}), "a plain enter is left to SendText")
	assert.False(t, p.SendModifiedKey(uv.KeyPressEvent{Code: 'a', Mod: uv.ModCtrl}), "other keys are left alone")
	p.SendText("one")
	assert.True(t, p.SendModifiedKey(uv.KeyPressEvent{Code: uv.KeyEnter, Mod: uv.ModCtrl}), "ctrl+enter breaks the line too")
	p.SendText("two")
	assert.Contains(t, view(p), "\n│   two█")

	p.SendText("\x15") // ctrl+u: an empty input, so ← opens the activity view
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	require.True(t, p.activity.open)
	assert.False(t, p.SendModifiedKey(uv.KeyPressEvent{Code: uv.KeyEnter, Mod: uv.ModShift}),
		"the activity view takes enter as itself")
}

// Escape sequences of modified editing keys, as the split forwards them.
const (
	ctrlLeft       = "\x1b[1;5D"
	ctrlRight      = "\x1b[1;5C"
	shiftLeft      = "\x1b[1;2D"
	shiftRight     = "\x1b[1;2C"
	ctrlShiftLeft  = "\x1b[1;6D"
	ctrlShiftRight = "\x1b[1;6C"
	shiftHome      = "\x1b[1;2H"
	ctrlDelete     = "\x1b[3;5~"
)

// press sends each key on its own, as the split does.
func press(p *Pane, keys ...string) {
	for _, k := range keys {
		p.SendText(k)
	}
}

func TestPane_WordMoves(t *testing.T) {
	p, _ := newTestPane(t, 60, 10, fixture())
	p.SendText("fix the_login, now")
	p.SendText(ctrlLeft)
	assert.Equal(t, 15, p.input.cur, "to the start of the last word")
	p.SendText(ctrlLeft)
	assert.Equal(t, 13, p.input.cur, "punctuation is a word of its own")
	p.SendText(ctrlLeft)
	assert.Equal(t, 4, p.input.cur, "an underscore belongs to the word")
	press(p, ctrlLeft, ctrlLeft, ctrlLeft)
	assert.Equal(t, 0, p.input.cur, "and stops at the start")
	p.SendText(ctrlRight)
	assert.Equal(t, 3, p.input.cur, "to the end of the next word")
	p.SendText("\x1b[1;3C")
	assert.Equal(t, 13, p.input.cur, "alt moves by word too")

	p.SendText(ctrlDelete)
	assert.Equal(t, "fix the_login now", p.input.text(), "ctrl+delete deletes the word after the cursor")
	p.SendText(ctrlDelete)
	assert.Equal(t, "fix the_login", p.input.text(), "with the space before it")

	p.SendText("\x15one\ntwo")
	press(p, ctrlLeft, ctrlLeft)
	assert.Equal(t, 0, p.input.cur, "a line break is space between words")
	p.SendText("\x1b[1;5A")
	assert.Equal(t, "one\ntwo", p.input.text(), "a modified ↑ is dropped, not typed")
}

func TestPane_SelectAndEdit(t *testing.T) {
	p, _ := newTestPane(t, 60, 10, fixture())
	p.SendText("hello brave world")
	press(p, shiftLeft, shiftLeft)
	lo, hi, ok := p.input.selection()
	require.True(t, ok)
	assert.Equal(t, [2]int{15, 17}, [2]int{lo, hi}, "shift+← selects a character at a time")
	p.SendText("LD")
	assert.Equal(t, "hello brave worLD", p.input.text(), "typing replaces the selection")

	press(p, ctrlLeft, ctrlShiftLeft)
	lo, hi, _ = p.input.selection()
	assert.Equal(t, "brave ", strings.Join(p.input.clusters[lo:hi], ""), "ctrl+shift+← selects a word")
	p.SendText("\x7f")
	assert.Equal(t, "hello worLD", p.input.text(), "backspace deletes the selection")

	press(p, ctrlShiftRight, shiftRight)
	lo, hi, _ = p.input.selection()
	assert.Equal(t, "worLD", strings.Join(p.input.clusters[lo:hi], ""), "a selection grows from its anchor")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	_, _, ok = p.input.selection()
	assert.False(t, ok, "← drops the selection")
	assert.Equal(t, 6, p.input.cur, "at its start")

	p.SendText(shiftHome)
	p.SendText(ctrlDelete)
	assert.Equal(t, "worLD", p.input.text(), "ctrl+delete deletes a selection")

	p.SendText(shiftRight)
	p.SendText("\x1b")
	_, _, ok = p.input.selection()
	assert.False(t, ok, "esc drops the selection")
	assert.Equal(t, "worLD", p.input.text())
	assert.False(t, p.stop.turn != "", "and does nothing else")

	p.SendText(shiftLeft)
	p.Paste("H")
	assert.Equal(t, "HorLD", p.input.text(), "a paste replaces the selection")
}

func TestPane_SelectionIsDrawn(t *testing.T) {
	f := newFakeBackend(fixture())
	p := New(f, "Fix login", 60, 10, Config{Clipboard: f.clip, Files: f.files})
	t.Cleanup(p.Close)
	p.lock()
	row := p.inputRowLocked([]string{"a", "b", "c"}, 0, 3, 1, 3)
	p.mu.Unlock()
	assert.Equal(t, p.composer.text.Render("a")+p.composer.selected.Render("bc")+p.composer.text.Render("█"), row)
}
