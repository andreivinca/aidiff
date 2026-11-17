package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paneModel is a review with a file loaded, the tree focused.
func paneModel() Model {
	m := testModel([]string{"a.go"}, nil)
	m.tree = testNewFileTree([]string{"a.go"})
	m.file.name = "a.go"
	m.layout.focus = paneTree
	return m
}

func TestModel_MovePaneFocus(t *testing.T) {
	m := paneModel()
	next, moved := m.MovePaneFocus(false)
	assert.False(t, moved, "the tree is the leftmost pane")
	assert.Equal(t, paneTree, next.(Model).layout.focus)

	next, moved = m.MovePaneFocus(true)
	require.True(t, moved, "right of the tree: the diff")
	m = next.(Model)
	assert.Equal(t, paneDiff, m.layout.focus)
	_, moved = m.MovePaneFocus(true)
	assert.False(t, moved, "the diff is the rightmost pane")

	next, moved = m.MovePaneFocus(false)
	require.True(t, moved)
	assert.Equal(t, paneTree, next.(Model).layout.focus)
}

func TestModel_MovePaneFocusStaysWhileBusy(t *testing.T) {
	m := paneModel()
	m.annot.annotating = true
	_, moved := m.MovePaneFocus(true)
	assert.False(t, moved, "typing an annotation keeps the pane")

	m = paneModel()
	m.search.active = true
	assert.Equal(t, paneTree, m.FocusPaneEdge(true).(Model).layout.focus, "and so does a search")
}

func TestModel_MovePaneFocusWithoutATree(t *testing.T) {
	m := paneModel()
	m.layout.treeHidden = true
	m.layout.focus = paneDiff
	_, moved := m.MovePaneFocus(false)
	assert.False(t, moved, "a hidden tree is no pane to move to")
	assert.Equal(t, paneDiff, m.FocusPaneEdge(false).(Model).layout.focus, "coming from the left lands on the diff then")
}

func TestModel_FocusPaneEdge(t *testing.T) {
	m := paneModel()
	assert.Equal(t, paneDiff, m.FocusPaneEdge(true).(Model).layout.focus, "from the right: the diff")
	assert.Equal(t, paneTree, m.FocusPaneEdge(false).(Model).layout.focus, "from the left: the tree")

	m.file.name = ""
	assert.Equal(t, paneTree, m.FocusPaneEdge(true).(Model).layout.focus, "with no file loaded the diff takes no focus")
}
