package t3chat

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/t3client"
)

func TestPane_EscapeStopNeedsExplicitConfirmation(t *testing.T) {
	p, f := newTestPane(t, 90, 20, fixture())
	p.SendText("unfinished draft")
	for range 8 {
		p.SendText("\x1b")
	}
	assert.Empty(t, f.cmds, "repeated accidental escape presses never stop the agent")
	assert.Empty(t, p.stop.runID)
	assert.Equal(t, "unfinished draft", p.input.text())

	p.SendText("\x1b")
	assert.Contains(t, view(p), "Stop agent?")
	assert.Contains(t, view(p), "[ Keep working ]", "the safe choice is selected by default")
	p.SendText("\r")
	assert.Empty(t, f.cmds, "enter on the default choice keeps working")
	assert.Empty(t, p.stop.runID)

	p.SendText("\x1b")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	assert.Contains(t, view(p), "[ Stop agent ]")
	p.SendText("\x1b")
	assert.Empty(t, f.cmds, "escape cancels even after selecting Stop")

	p.SendText("\x1b")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendText("\r")
	cmd := nextCommand(t, f)
	assert.Equal(t, "run.interrupt", cmd["type"])
	assert.Equal(t, "run-2", cmd["runId"])
	waitUpdate(t, p)
	assert.Equal(t, "unfinished draft", p.input.text(), "confirmed stops keep the draft")
}

func TestPane_EscapeDismissesOtherViewsFirst(t *testing.T) {
	p, f := withCatalog(t)
	p.SendText("/mo")
	p.SendText("\x1b")
	assert.Empty(t, p.suggestionsLocked())
	assert.Empty(t, p.stop.runID, "escape only closes the visible slash popup")
	p.SendText("\x15")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendText("\x1b")
	assert.False(t, p.activity.open)
	assert.Empty(t, p.stop.runID, "escape only closes Activity")
	assert.Empty(t, f.cmds)
}

func TestPane_StopConfirmationPinsTheTurn(t *testing.T) {
	p, f := newTestPane(t, 90, 20, fixture())
	p.SendText("draft\x1b")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	f.live().Run(t3client.Run{ID: "run-2", Ordinal: 2, Status: "completed"})
	f.live().Run(t3client.Run{ID: "new-run", Ordinal: 3, Status: "running"})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "This turn has finished")
	p.SendText("\r")
	assert.Empty(t, f.cmds, "an old confirmation cannot stop a newer turn or send the draft")
	assert.Equal(t, "draft", p.input.text())

	p.SendText("\x1b")
	p.Follow(nil, "th-2", "Another thread")
	assert.Empty(t, p.stop.runID, "switching threads dismisses the old confirmation")
}

func TestPane_StopDialogTypingPasteAndResize(t *testing.T) {
	p, f := newTestPane(t, 90, 20, fixture())
	p.SendText("draft\x1b")
	p.SendText("yes")
	assert.Empty(t, p.stop.runID)
	assert.Empty(t, f.cmds, "typing is never an approval shortcut")
	assert.Equal(t, "draftyes", p.input.text())
	p.SendText("\x1b")
	p.Paste(" pasted")
	assert.Empty(t, p.stop.runID)
	assert.Equal(t, "draftyes pasted", p.input.text())
	p.SendText("\x1b")
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
	assert.Empty(t, f.cmds)
}

func TestPane_EscapeWhileIdle(t *testing.T) {
	snap := fixture()
	snap.Runs = nil
	p, f := newTestPane(t, 90, 20, snap)
	p.SendText("\x1b")
	assert.Empty(t, p.stop.runID)
	assert.Empty(t, f.cmds)
}
