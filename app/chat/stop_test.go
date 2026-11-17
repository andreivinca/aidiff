package chat

import (
	"fmt"
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

func TestPane_StopWithQueuedMessagesLeavesTheInput(t *testing.T) {
	conv := fixture()
	conv.Queued = []Queued{{ID: "m3", Text: "now it works"}}
	p, f := newTestPane(t, 90, 20, conv)
	f.setCommands(Command{Name: "/resume"})
	stop := func() {
		p.SendText("\x1b")
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
		ask(t, p, f, func() {
			p.SendText("\r")
		})
	}
	stop()
	assert.Empty(t, p.input.text(), "the backend starts the queue itself; nothing is put in the input")

	f.set(fmt.Errorf("%w, but the queued messages did not start: %w", ErrStopped, errBoom), nil)
	stop()
	assert.Contains(t, view(p), "stopped, but the queued messages did not start: boom")
	assert.NotContains(t, view(p), "stop failed", "a stop that went through is not reported as failed")
}
