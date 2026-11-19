package splitview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// panedInner is a wrapped model with two panes, the left one cols wide,
// focus on the left one when left is set.
type panedInner struct {
	innerModel
	cols int
	left bool
}

func (p panedInner) FocusedColumns() (from, to int) {
	if p.left {
		return 0, p.cols
	}
	return p.cols, 1000 // past the column: clamped
}

func (p panedInner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	*p.got = append(*p.got, msg)
	return p, nil
}

// bgAt is the truecolor background of column x of line, "" for none.
func bgAt(line string, x int) string {
	cell := ansi.Cut(line, x, x+1) // every escape of the line, and the cell's character
	head := 0                      // the escapes before the character are the state it is drawn in
	for _, loc := range sgrSeq.FindAllStringIndex(cell, -1) {
		if cell[head:loc[0]] != "" {
			break
		}
		head = loc[1]
	}
	cell = cell[:head]
	bg := ""
	for _, seq := range sgrSeq.FindAllString(cell, -1) {
		params := sgrParams(seq)
		for i := 0; i < len(params); i++ {
			switch params[i] {
			case "38", "58":
				i += colorArgs(params, i)
			case "48":
				bg = strings.Join(params[i+1:min(len(params), i+5)], ";")
				i += colorArgs(params, i)
			case "0", "", "49":
				bg = ""
			}
		}
	}
	return bg
}

func TestSplit_FocusMarkFollowsTheFocusedPanel(t *testing.T) {
	got := []tea.Msg{}
	hs := &harness{inner: &got}
	hs.m = New(panedInner{innerModel: innerModel{got: &got}, cols: 10, left: true}, hs.config())
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 20})
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	hs.send(tea.KeyMsg{Type: tea.KeyEnter}) // Open Terminal
	require.Len(t, hs.m.terms, 1)
	bar := "\x1b[38;2;10;10;10;48;2;200;100;50m" + strings.Repeat("x", 100) + "\x1b[0m"
	const orange, green = "2;200;100;50", "2;0;215;0"

	marked := hs.m.withFocusMark(bar)
	assert.Equal(t, strings.Repeat("x", 100), ansi.Strip(marked), "only colors change")
	term := hs.m.leftW + hs.m.termRects()[0].x
	assert.Equal(t, orange, bgAt(marked, term-1))
	assert.Equal(t, green, bgAt(marked, term), "the focused terminal's columns are green")
	assert.Equal(t, green, bgAt(marked, 99))

	hs.m.focus = focusDiff
	marked = hs.m.withFocusMark(bar)
	assert.Equal(t, green, bgAt(marked, 0), "the wrapped model's focused pane")
	assert.Equal(t, green, bgAt(marked, 9))
	assert.Equal(t, orange, bgAt(marked, 10), "its other pane is not")

	hs.m.inner = panedInner{innerModel: innerModel{got: &got}, cols: 10}
	marked = hs.m.withFocusMark(bar)
	assert.Equal(t, orange, bgAt(marked, 9))
	assert.Equal(t, green, bgAt(marked, hs.m.leftW-1), "the right pane, up to the column's edge")
	assert.Equal(t, orange, bgAt(marked, hs.m.leftW), "not past it")

	hs.send(tea.WindowSizeMsg{Width: 140, Height: 20})
	marked = hs.m.withFocusMark(strings.Replace(bar, strings.Repeat("x", 100), strings.Repeat("x", 140), 1))
	assert.Equal(t, green, bgAt(marked, hs.m.leftW-1), "a resize moves the mark with the panel")

	plain := strings.Repeat("y", 140)
	assert.Contains(t, hs.m.withFocusMark(plain), "\x1b[7m", "no background to shade: reverse video")
}

func TestWithBackground(t *testing.T) {
	const shade = "48;2;1;2;3"
	assert.Equal(t, "\x1b[38;2;9;9;9;48;2;1;2;3m", withColors("\x1b[38;2;9;9;9;48;2;200;100;50m", shade, ""),
		"a foreground's numbers are not read as a background")
	assert.Equal(t, "\x1b[48;2;1;2;3m", withColors("\x1b[49m", shade, ""))
	assert.Equal(t, "\x1b[0;48;2;1;2;3m", withColors("\x1b[0m", shade, ""), "a reset keeps the shade")
	assert.Equal(t, "\x1b[;48;2;1;2;3m", withColors("\x1b[m", shade, ""))
	assert.Equal(t, "\x1b[1;38;5;3m", withColors("\x1b[1;38;5;3m", shade, ""))

	const text = "38;2;4;5;6"
	assert.Equal(t, "\x1b[38;2;4;5;6;48;2;1;2;3m", withColors("\x1b[38;2;9;9;9;48;2;200;100;50m", shade, text), "a foreground becomes the mark's")
	assert.Equal(t, "\x1b[1;38;2;4;5;6m", withColors("\x1b[1;39m", shade, text))
	assert.Equal(t, "\x1b[0;48;2;1;2;3;38;2;4;5;6m", withColors("\x1b[0m", shade, text), "a reset keeps both")
	assert.Equal(t, focusGreen, focusBackground("\x1b[38;5;48m\x1b[48;2;7;8;9mx"), "a foreground's numbers are not read as a background")
	assert.Equal(t, focusGreenPalette, focusBackground("\x1b[48;5;208mx"), "a palette line gets the palette's green")
	assert.Empty(t, focusBackground("\x1b[1mx"), "no background: reverse video")
}

// coloredInner is a paned wrapped model that colors the focus mark.
type coloredInner struct{ panedInner }

func (c coloredInner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	*c.got = append(*c.got, msg)
	return c, nil
}

func (coloredInner) FocusMarkColors() (bg, fg string) {
	return "\x1b[48;2;30;60;20m", "\x1b[38;2;220;220;220m"
}

func TestSplit_FocusMarkTakesTheWrappedModelsColors(t *testing.T) {
	got := []tea.Msg{}
	hs := &harness{inner: &got}
	hs.m = New(coloredInner{panedInner{innerModel: innerModel{got: &got}, cols: 10, left: true}}, hs.config())
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 20})
	bar := "\x1b[38;2;10;10;10;48;2;200;100;50m" + strings.Repeat("x", 100) + "\x1b[0m"
	marked := hs.m.withFocusMark(bar)
	assert.Equal(t, "2;30;60;20", bgAt(marked, 0), "the model's own green")
	assert.Contains(t, ansi.Cut(marked, 0, 1), "38;2;220;220;220", "and its text color, readable on it")
	assert.Equal(t, "2;200;100;50", bgAt(marked, 10))
}
