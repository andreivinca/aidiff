package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
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

func dividerMouse(t *testing.T, m Model, x, y int, button tea.MouseButton, action tea.MouseAction) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(tea.MouseMsg(tea.MouseEvent{X: x, Y: y, Button: button, Action: action}))
	return next.(Model), cmd
}

func TestModel_dragTreeDivider_ReleaseOverTheToolbarEndsIt(t *testing.T) {
	m := toolbarModel(t)
	require.True(t, m.toolbarShown())
	border := m.layout.treeWidth + 1
	m, _ = dividerMouse(t, m, border, 5, tea.MouseButtonLeft, tea.MouseActionPress)
	require.True(t, m.divider.dragging)
	m, _ = dividerMouse(t, m, border+4, toolbarRow, tea.MouseButtonLeft, tea.MouseActionMotion)
	assert.Equal(t, border+3, m.layout.treeWidth, "motion over the toolbar row still drags")
	m, _ = dividerMouse(t, m, border+4, toolbarRow, tea.MouseButtonLeft, tea.MouseActionRelease)
	assert.False(t, m.divider.dragging, "the release over the toolbar row ends the drag")

	width := m.layout.treeWidth
	m, _ = dividerMouse(t, m, 60, 10, tea.MouseButtonLeft, tea.MouseActionPress)
	m, _ = dividerMouse(t, m, 70, 10, tea.MouseButtonLeft, tea.MouseActionMotion)
	assert.Equal(t, width, m.layout.treeWidth, "a later press and move elsewhere leaves the divider")
}

func TestModel_dragTreeDivider_StaleDragEnds(t *testing.T) {
	lines := make([]diff.DiffLine, 20)
	for i := range lines {
		lines[i] = diff.DiffLine{OldNum: i + 1, NewNum: i + 1, Content: fmt.Sprintf("line %d", i), ChangeType: diff.ChangeContext}
	}
	m := loadFileIntoModel(t, []string{"a.go", "b.go"}, map[string][]diff.DiffLine{"a.go": lines})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = next.(Model)
	width := m.layout.treeWidth

	m.divider.dragging = true // the release was lost outside the window
	m, _ = dividerMouse(t, m, width+20, 5, tea.MouseButtonNone, tea.MouseActionMotion)
	assert.False(t, m.divider.dragging, "motion with no button held ends it")
	assert.Equal(t, width, m.layout.treeWidth, "without moving the divider")

	m.divider.dragging = true
	m.layout.focus = paneTree
	m, _ = dividerMouse(t, m, width+10, 6, tea.MouseButtonLeft, tea.MouseActionPress)
	assert.False(t, m.divider.dragging, "a fresh press ends it")
	assert.Equal(t, paneDiff, m.layout.focus, "and clicks as any other press")
	assert.Equal(t, width, m.layout.treeWidth)
}

func TestModel_dragTreeDivider_RendersAtTheNewWidth(t *testing.T) {
	long := strings.Repeat("word ", 60)
	lines := []diff.DiffLine{
		{OldNum: 1, NewNum: 1, Content: long, ChangeType: diff.ChangeContext},
		{OldNum: 2, NewNum: 2, Content: long, ChangeType: diff.ChangeContext},
		{OldNum: 3, NewNum: 3, Content: "end", ChangeType: diff.ChangeContext},
	}
	m := loadFileIntoModel(t, []string{"a.go", "b.go"}, map[string][]diff.DiffLine{"a.go": lines})
	m.modes.wrap = true
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = next.(Model)
	m.nav.diffCursor = 2
	m.syncViewportToCursor()
	border := m.layout.treeWidth + 1

	m, _ = dividerMouse(t, m, border, 5, tea.MouseButtonLeft, tea.MouseActionPress)
	m, cmd := dividerMouse(t, m, border+30, 5, tea.MouseButtonLeft, tea.MouseActionMotion)
	assert.Nil(t, cmd, "the first width renders at once")
	assert.Equal(t, strings.Split(m.renderDiff(), "\n"), m.layout.viewport.lines, "wrapped at the new width")
	top, _ := m.cursorVisualRange()
	assert.Contains(t, ansi.Strip(m.layout.viewport.lines[top]), "end", "the cursor's range matches what is painted")

	m, cmd = dividerMouse(t, m, border+20, 5, tea.MouseButtonLeft, tea.MouseActionMotion)
	require.NotNil(t, cmd, "a width right after waits for the scheduled render")
	assert.True(t, m.divider.renderOwed)
	_, again := dividerMouse(t, m, border+15, 5, tea.MouseButtonLeft, tea.MouseActionMotion)
	assert.Nil(t, again, "one scheduled render at a time")
	assert.IsType(t, dividerRenderMsg{}, cmd())
	rendered, _ := m.Update(dividerRenderMsg{})
	assert.Equal(t, strings.Split(m.renderDiff(), "\n"), rendered.(Model).layout.viewport.lines, "the scheduled render")

	m, _ = dividerMouse(t, m, border+10, 5, tea.MouseButtonLeft, tea.MouseActionRelease)
	assert.False(t, m.divider.renderOwed, "the release renders what is owed")
	assert.Equal(t, strings.Split(m.renderDiff(), "\n"), m.layout.viewport.lines)
}
