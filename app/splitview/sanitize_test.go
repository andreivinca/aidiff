package splitview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hostile is remote text that writes the clipboard (OSC 52), sets the
// window title (OSC 0, ST-terminated), and carries a CSI, controls, a C1
// control and an invalid byte.
const hostile = "a\x1b]52;c;aGk=\x07b\x1b]0;title\x1b\\c\x1b[31md\te\nf\u009bg\xffh\x08"

func TestCleanText(t *testing.T) {
	assert.Equal(t, "abcd e fgh", cleanLine(hostile))
	assert.Equal(t, "abcd e\nfgh", cleanParagraph(hostile), "a paragraph keeps its line breaks")
	assert.Equal(t, "plain ASCII", cleanLine("plain ASCII"))
	assert.Equal(t, "naïve – ✓", cleanLine("naïve – ✓"), "printable Unicode stays")
	assert.Equal(t, "\x1b[1;31mred\x1b[0m\nrow 2", cleanView("\x1b[1;31mred\x1b[0m\x1b]52;c;aGk=\x07\nrow\x1b[2J 2\x1b]0;t\x07"),
		"a styled view keeps its colors and rows, nothing else")
}

// assertSafe fails when view still carries an escape that is not a color.
func assertSafe(t *testing.T, view string) {
	t.Helper()
	assert.NotContains(t, view, "\x1b]", "no OSC reaches the terminal")
	assert.NotContains(t, view, "\x07")
	assert.NotContains(t, view, "\u009b")
	assert.NotContains(t, view, "\x1b[31m", "nor the caller's own CSI")
}

func TestSanitize_CallerTextIsCleanedWhereverItIsDrawn(t *testing.T) {
	hs := newHarnessOpts(t, 120, 30, false)
	hs.send(ShowPickerMsg{Title: "Threads " + hostile, Items: []PickerItem{
		{Label: "thread " + hostile, Detail: "detail " + hostile, Group: "group " + hostile},
	}})
	view := hs.m.View()
	assertSafe(t, view)
	assert.Contains(t, view, "thread abcd e fgh", "the text itself still shows")

	hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	hs.send(ShowPromptMsg{Title: "Name", Placeholder: "folder " + hostile})
	assertSafe(t, hs.m.View())
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(hostile), Paste: true})
	assertSafe(t, hs.m.View())

	hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	hs.send(FlashMsg{Text: "T3: open: " + hostile})
	view = hs.m.View()
	assertSafe(t, view)
	assert.Contains(t, view, "T3: open: abcd e fgh")

	hs.send(ConfirmMsg{Title: "Forget " + hostile + "?", Detail: hostile, Confirm: "Forget" + hostile})
	assertSafe(t, hs.m.View())
}

func TestSanitize_SidebarKeepsItsColorsOnly(t *testing.T) {
	sb := &styledSidebar{fakeSidebar{available: true}}
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Sidebar = &hostileSidebar{sb}
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 20})
	hs.send(ShowSidebarMsg{})
	require.True(t, hs.m.sideOpen)
	view := hs.m.View()
	assert.NotContains(t, view, "\x1b]")
	assert.Contains(t, view, "\x1b[38;5;6m", "the sidebar's own colors stay")
	assert.Contains(t, view, "project", "and its text")
}

// hostileSidebar draws a remote project title with an OSC 52 in each row.
type hostileSidebar struct {
	*styledSidebar
}

func (h *hostileSidebar) View(width, height int, focused bool) string {
	rows := strings.Split(h.styledSidebar.View(width, height, focused), "\n")
	for i := range rows {
		rows[i] = "\x1b]52;c;aGk=\x07" + rows[i]
	}
	return strings.Join(rows, "\n")
}
