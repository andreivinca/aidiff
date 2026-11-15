package splitview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplit_VisibleResizeGrips(t *testing.T) {
	hs := newHarness(t, 100, 20)
	hs.runPalette("open")
	rows := strings.Split(ansi.Strip(hs.m.View()), "\n")
	require.Len(t, rows, 20)
	for y, row := range rows {
		assert.Equal(t, 100, ansi.StringWidth(row))
		if y == 10 {
			assert.Equal(t, '↔', []rune(row)[59], "the review divider has a visible grip")
			assert.Equal(t, '↔', []rune(row)[80], "terminal dividers have the same grip")
		}
	}
}

func TestSplit_DragCommitsReleaseAndKeepsGrabOffset(t *testing.T) {
	for _, grab := range []int{59, 60} {
		hs := newHarness(t, 100, 20)
		hs.send(tea.MouseMsg{X: grab, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		require.True(t, hs.m.dragging)
		hs.send(tea.MouseMsg{X: grab, Y: 10, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
		assert.Equal(t, 60, hs.m.leftW, "grabbing the adjacent cell does not shift the divider")
		hs.send(tea.MouseMsg{X: grab, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
		assert.True(t, hs.m.dragging, "an incidental wheel event does not lose the grab")
		hs.send(tea.MouseMsg{X: grab - 20, Y: 10, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
		assert.False(t, hs.m.dragging)
		assert.Equal(t, 40, hs.m.leftW, "a release with no motion event still resizes the review")
		assert.Equal(t, 60, hs.term(0).w)
		assert.Equal(t, tea.WindowSizeMsg{Width: 40, Height: 20}, hs.lastInnerSize())
		assert.Empty(t, hs.term(0).mice, "resize events stay in the wrapper")
	}
}
