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
	panel := userPanel(strings.Join(text, "\n"), 40, st)
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
	short := userPanel("one\ntwo\nthree\nfour", 40, st)
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
