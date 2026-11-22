package chat

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lineText is a sticky row's text without the panel's rail and padding.
func lineText(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimSpace(strings.TrimPrefix(ansi.Strip(r), rail))
	}
	return out
}

func TestStickyRows(t *testing.T) {
	var text []string
	for i := 1; i <= 10; i++ {
		text = append(text, fmt.Sprintf("L%d", i))
	}
	st, _ := newStyles(false)
	panel := userPanel(UserMessage{Text: strings.Join(text, "\n")}, 40, st)
	require.Len(t, panel.rows, 12, "padding, ten lines, padding")
	all := text
	tests := []struct {
		off  int
		want []string
	}{
		{off: 30, want: []string{"L1", "L2", "L3", "…", ""}},        // long gone: its start only
		{off: 11, want: []string{"L1", "L2", "L3", "…", ""}},        // only its padding still in view
		{off: 10, want: []string{"L1", "L2", "L3", "…", "L10", ""}}, // its last line back in view
		{off: 9, want: []string{"L1", "L2", "L3", "…", "L9", "L10", ""}},
		{off: 6, want: []string{"L1", "L2", "L3", "…", "L6", "L7", "L8", "L9", "L10", ""}},
		{off: 5, want: append(slices.Clone(all), "")}, // the gap would hide one line: it closed
		{off: 1, want: append(slices.Clone(all), "")},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, lineText(stickyRows(panel, tt.off, 40, st).rows), "off %d", tt.off)
	}
	short := userPanel(UserMessage{Text: "one\ntwo\nthree\nfour"}, 40, st)
	assert.Equal(t, []string{"one", "two", "three", "four", ""}, lineText(stickyRows(short, 30, 40, st).rows),
		"four lines or fewer are never shortened")
}

func TestPane_StickyMessage(t *testing.T) {
	var prompt, reply []string
	for i := 1; i <= 10; i++ {
		prompt = append(prompt, fmt.Sprintf("ask %d", i))
	}
	for i := 1; i <= 60; i++ {
		reply = append(reply, fmt.Sprintf("answer line %d", i), "")
	}
	conv := Conversation{State: AgentIdle, Entries: []Entry{
		UserMessage{Text: strings.Join(prompt, "\n")},
		AgentMessage{Markdown: strings.Join(reply, "\n")},
	}}
	p, _ := newTestPane(t, 60, 30, conv)
	top := func() []string {
		rows := strings.Split(view(p), "\n")[1:6] // under the header
		return lineText(rows)
	}
	assert.Equal(t, []string{"ask 1", "ask 2", "ask 3", "…", ""}, top(), "the reply's end: the question stays on top, compact, apart from the reply")

	for range 400 {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	}
	assert.Equal(t, "ask 1", lineText(strings.Split(view(p), "\n")[2:3])[0], "at the top the message is in its place")
	assert.NotContains(t, lineText(strings.Split(view(p), "\n")), "…", "and whole: no gap row")
}

// TestPane_StickyMessageIsPushedByTheNext pins position: sticky's hand-over:
// the next message's panel, coming up under the sticky one, pushes it up a
// row a row and is never drawn over; once at the top it is the one kept.
func TestPane_StickyMessageIsPushedByTheNext(t *testing.T) {
	var reply []string
	for i := 1; i <= 40; i++ {
		reply = append(reply, fmt.Sprintf("answer line %d", i), "")
	}
	conv := Conversation{State: AgentIdle, Entries: []Entry{
		UserMessage{Text: "A1\nA2\nA3\nA4\nA5\nA6"},
		AgentMessage{Markdown: strings.Join(reply, "\n")},
		UserMessage{Text: "B1\nB2"},
		AgentMessage{Markdown: strings.Join(reply, "\n")},
	}}
	const w, h = 60, 20
	p, _ := newTestPane(t, w, 30, conv)
	var lines block
	var spans []panelSpan
	locked(p, func() { lines, spans = p.transcriptPanelsLocked(w) })
	require.Len(t, spans, 2)
	bStart, n := spans[1].start, len(lines.rows)
	window := func(top int) []string {
		var rows []string
		locked(p, func() {
			p.scroll = n - h - top
			rows, _, _, _, _ = p.transcriptWindowLocked(w, h)
		})
		return lineText(rows)
	}
	full := []string{"A1", "A2", "A3", "…", ""} // A compact, and the blank row under it

	assert.Equal(t, full, window(bStart - 20)[:5], "far from B: A is kept on top, whole")
	assert.Equal(t, full, window(bStart - 5)[:5], "B right under it: A still whole")
	got := window(bStart - 2)
	assert.Equal(t, []string{"…", ""}, got[:2], "B two rows from the top: A pushed up by three")
	assert.Equal(t, "B1", got[3], "and B in its place, under its padding row, not drawn over")
	got = window(bStart)
	assert.Equal(t, "B1", got[1], "B at the top: A is gone, B shows in its place")
	assert.NotContains(t, got, "A1")
	assert.Equal(t, []string{"B1", "B2", ""}, window(bStart + 1)[:3], "B scrolled past the top: B is the one kept")
}

// TestPane_ClickOnStickyMessageScrollsBackToIt: a click on the message kept
// on top scrolls the transcript back to it, whole, in its place, with the
// start of the answer under it; it starts no selection. A click anywhere
// else still does.
func TestPane_ClickOnStickyMessageScrollsBackToIt(t *testing.T) {
	var prompt, reply []string
	for i := 1; i <= 10; i++ {
		prompt = append(prompt, fmt.Sprintf("ask %d", i))
	}
	for i := 1; i <= 60; i++ {
		reply = append(reply, fmt.Sprintf("answer line %d", i), "")
	}
	conv := Conversation{State: AgentIdle, Entries: []Entry{
		UserMessage{Text: strings.Join(prompt, "\n")},
		AgentMessage{Markdown: strings.Join(reply, "\n")},
	}}
	p, _ := newTestPane(t, 60, 30, conv)
	rows := func() []string { return lineText(strings.Split(view(p), "\n")) }
	require.Equal(t, []string{"ask 1", "ask 2", "ask 3", "…", ""}, rows()[1:6], "compact on top at the reply's end")

	p.SendMouse(uv.MouseClickEvent{X: 3, Y: 4, Button: uv.MouseLeft}) // on its "…"
	p.SendMouse(uv.MouseReleaseEvent{X: 3, Y: 4, Button: uv.MouseLeft})
	got := rows()
	assert.Equal(t, "ask 1", got[2], "the message is back in its place, at the top under its padding row")
	assert.Equal(t, "ask 10", got[11], "whole")
	assert.NotContains(t, got, "…", "with no gap")
	assert.Equal(t, "answer line 1", got[14], "and the start of the answer under it")
	locked(p, func() { assert.False(t, p.selection.dragging, "the click selected nothing") })

	for range 400 {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	}
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyEnd})
	_ = view(p)
	p.SendMouse(uv.MouseClickEvent{X: 3, Y: 12, Button: uv.MouseLeft}) // under the sticky rows: the answer
	locked(p, func() { assert.True(t, p.selection.dragging, "a click on the answer starts a selection as before") })
	p.SendMouse(uv.MouseReleaseEvent{X: 3, Y: 12, Button: uv.MouseLeft})
}
