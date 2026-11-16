package chat

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPane_EscapeStopNeedsExplicitConfirmation(t *testing.T) {
	p, f := newTestPane(t, 90, 20, fixture())
	p.SendText("unfinished draft")
	for range 8 {
		p.SendText("\x1b")
	}
	assert.Empty(t, f.calls, "repeated accidental escape presses never stop the agent")
	assert.Empty(t, p.stop.turn)
	assert.Equal(t, "unfinished draft", p.input.text())

	p.SendText("\x1b")
	assert.Contains(t, view(p), "Stop agent?")
	assert.Contains(t, view(p), "[ Keep working ]", "the safe choice is selected by default")
	p.SendText("\r")
	assert.Empty(t, f.calls, "enter on the default choice keeps working")
	assert.Empty(t, p.stop.turn)

	p.SendText("\x1b")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	assert.Contains(t, view(p), "[ Stop agent ]")
	p.SendText("\x1b")
	assert.Empty(t, f.calls, "escape cancels even after selecting Stop")

	p.SendText("\x1b")
	p.SendText("\t")
	assert.Contains(t, view(p), "[ Stop agent ]", "tab toggles the choice")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyRight})
	assert.Contains(t, view(p), "[ Keep working ]")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	c := ask(t, p, f, func() {
		p.SendText("\r")
	})
	assert.Equal(t, call{kind: "interrupt", id: "run-2"}, c)
	assert.Equal(t, "unfinished draft", p.input.text(), "confirmed stops keep the draft")

	f.set(errBoom, nil)
	p.SendText("\x1b")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	ask(t, p, f, func() {
		p.SendText("\r")
	})
	assert.Contains(t, view(p), "stop failed: boom")
}

func TestPane_EscapeDismissesOtherViewsFirst(t *testing.T) {
	p, f := newTestPane(t, 90, 20, fixture())
	p.SendText("/he")
	p.SendText("\x1b")
	assert.Empty(t, p.suggestionsLocked())
	assert.Empty(t, p.stop.turn, "escape only closes the visible slash popup")
	p.SendText("\x15")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendText("\x1b")
	assert.False(t, p.activity.open)
	assert.Empty(t, p.stop.turn, "escape only closes Activity")
	assert.Empty(t, f.calls)
}

func TestPane_StopConfirmationPinsTheTurn(t *testing.T) {
	p, f := newTestPane(t, 90, 20, fixture())
	p.SendText("draft\x1b")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	conv := fixture()
	conv.Running = []string{"new-run"}
	show(t, p, f, conv)
	assert.Contains(t, view(p), "This turn has finished")
	p.SendText("\r")
	assert.Empty(t, f.calls, "an old confirmation cannot stop a newer turn or send the draft")
	assert.Equal(t, "draft", p.input.text())

	p.SendText("\x1b")
	p.Follow(nil, "Another thread")
	assert.Empty(t, p.stop.turn, "following another backend dismisses the old confirmation")
}

func TestPane_StopDialogTypingPasteAndResize(t *testing.T) {
	p, f := newTestPane(t, 90, 20, fixture())
	p.SendText("draft\x1b")
	p.SendText("yes")
	assert.Empty(t, p.stop.turn)
	assert.Empty(t, f.calls, "typing is never an approval shortcut")
	assert.Equal(t, "draftyes", p.input.text())
	p.SendText("\x1b")
	p.Paste(" pasted")
	assert.Empty(t, p.stop.turn)
	assert.Equal(t, "draftyes pasted", p.input.text())
	p.SendText("\x1b")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	p.SendMouse(uv.MouseWheelEvent{Button: uv.MouseWheelUp})
	assert.Contains(t, view(p), "Stop agent?", "other keys and the wheel leave the dialog alone")
	for _, w := range []int{1, 6, 20, 60, 90} {
		for _, h := range []int{1, 2, 6, 10, 20} {
			p.Resize(w, h)
			frame := p.Render(true)
			assert.Equal(t, ansi.Strip(frame), frame, "NoColor emits no escape sequences")
			rows := strings.Split(frame, "\n")
			require.Len(t, rows, h)
			for _, row := range rows {
				assert.Equal(t, w, ansi.StringWidth(row))
			}
		}
	}
	assert.Empty(t, f.calls)
}

func TestPane_EscapeWhileIdle(t *testing.T) {
	conv := fixture()
	conv.Running = nil
	p, f := newTestPane(t, 90, 20, conv)
	p.SendText("\x1b")
	assert.Empty(t, p.stop.turn)
	p.SendText("/stop\r")
	assert.Contains(t, view(p), "nothing is running")
	assert.Empty(t, f.calls)
}

func TestPane_StopWithQueuedMessagesOffersResume(t *testing.T) {
	conv := fixture()
	conv.Queued = []Queued{{ID: "m3", Text: "now it works"}}
	p, f := newTestPane(t, 90, 20, conv)
	resumed := 0
	f.setCommands(Command{Name: "/resume", Description: "send the queued messages a stop held back", Run: func(string) (Reply, error) {
		resumed++
		return Reply{Note: "the queued messages go on"}, nil
	}})
	stop := func() {
		p.SendText("\x1b")
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
		ask(t, p, f, func() {
			p.SendText("\r")
		})
	}
	stop()
	assert.Equal(t, "/resume", p.input.text(), "an empty input is filled with /resume, so enter sends the queue")
	assert.Contains(t, view(p), "stopped; enter sends the queued messages")
	p.SendText("\x7f")
	assert.Empty(t, p.input.text(), "one backspace removes /resume whole")
	p.SendText("/resume")
	locked(p, func() {
		assert.Len(t, p.input.clusters, len("/resume"), "typed by hand it is plain text")
	})
	p.SendText("\r")
	assert.Equal(t, 1, resumed, "enter runs /resume")
	assert.Empty(t, p.input.text())

	p.SendText("\x15draft")
	stop()
	assert.Equal(t, "draft", p.input.text(), "a draft is kept")
	assert.Contains(t, view(p), "/resume sends them")
}

func TestPane_StopWithoutResumeOrQueue(t *testing.T) {
	p, f := newTestPane(t, 90, 20, fixture())
	f.setCommands(Command{Name: "/resume"})
	p.SendText("\x1b")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	ask(t, p, f, func() {
		p.SendText("\r")
	})
	assert.Empty(t, p.input.text(), "nothing queued, nothing to resume")

	conv := fixture()
	conv.Queued = []Queued{{ID: "m3", Text: "queued"}}
	p, f = newTestPane(t, 90, 20, conv)
	p.SendText("\x1b")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	ask(t, p, f, func() {
		p.SendText("\r")
	})
	assert.Empty(t, p.input.text(), "a backend without /resume gets no /resume")
}
