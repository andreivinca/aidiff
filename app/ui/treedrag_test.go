package ui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/ui/sidepane"
)

func TestModel_dragTreeDivider(t *testing.T) {
	m := testModel([]string{"a.go", "b.go"}, nil)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = mm.(Model)
	require.False(t, m.treePaneHidden())
	border := m.layout.treeWidth + 1
	was := m.layout.treeWidth

	mouse := func(x int, action tea.MouseAction) {
		next, _ := m.Update(tea.MouseMsg(tea.MouseEvent{X: x, Y: 5, Button: tea.MouseButtonLeft, Action: action}))
		m = next.(Model)
	}
	mouse(border, tea.MouseActionPress)
	assert.True(t, m.divider.dragging, "a press on the tree's right border starts the drag")
	mouse(border+10, tea.MouseActionMotion)
	assert.Equal(t, was+10, m.layout.treeWidth, "motion moves the divider")
	assert.Equal(t, 120-m.layout.treeWidth-4, m.layout.viewport.Width)
	mouse(5, tea.MouseActionMotion)
	assert.Equal(t, minTreeWidth, m.layout.treeWidth, "the tree keeps its minimum")
	mouse(300, tea.MouseActionMotion)
	assert.Equal(t, 120-4-minDiffWidth, m.layout.treeWidth, "the diff keeps its minimum")
	mouse(300, tea.MouseActionRelease)
	assert.False(t, m.divider.dragging)

	mm, _ = m.Update(tea.WindowSizeMsg{Width: 240, Height: 40})
	m = mm.(Model)
	assert.InDelta(t, float64(120-4-minDiffWidth)/120, m.divider.share, 0.5/120)
	assert.Equal(t, int(float64(240)*m.divider.share+0.5), m.layout.treeWidth, "a resize keeps the dragged share")

	mouse(border, tea.MouseActionPress) // not on the border any more: an ordinary click
	assert.False(t, m.divider.dragging)
}

func TestModel_HandleMouse_WheelInTreeScrollsWithoutSelecting(t *testing.T) {
	files := make([]string, 60)
	for i := range files {
		files[i] = fmt.Sprintf("%02d.go", i)
	}
	m := mouseTestModel(t, files, nil)
	selected := m.tree.SelectedFile()
	result, cmd := m.Update(wheelMsg(tea.MouseButtonWheelDown, 5, 3, false))
	model := result.(Model)
	assert.Nil(t, cmd)
	assert.Equal(t, selected, model.tree.SelectedFile())
	assert.Equal(t, wheelStep, model.tree.ScrollState().Offset)
	model.View()
	assert.Equal(t, wheelStep, model.tree.ScrollState().Offset, "rendering keeps the scrolled window")
	result, _ = model.Update(wheelMsg(tea.MouseButtonWheelUp, 5, 3, false))
	model = result.(Model)
	assert.Equal(t, 0, model.tree.ScrollState().Offset)
	assert.Equal(t, selected, model.tree.SelectedFile())
	model.tree.Move(sidepane.MotionDown)
	model.tree.EnsureVisible(model.treePageSize())
	assert.Equal(t, files[1], model.tree.SelectedFile())
}
