package ui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
)

type fakeReverter struct {
	change     []int // start, end of the last RevertChange
	changeFile diff.FileEntry
	file       *diff.FileEntry
	err        error
}

func (f *fakeReverter) RevertChange(file diff.FileEntry, _ []diff.DiffLine, start, end int) error {
	f.change, f.changeFile = []int{start, end}, file
	return f.err
}

func (f *fakeReverter) RevertFile(file diff.FileEntry) error {
	f.file = &file
	return f.err
}

// revertModel is a review of a.go on screen, its cursor on line idx.
func revertModel(r Reverter, idx int) Model {
	lines := []diff.DiffLine{
		{OldNum: 1, NewNum: 1, ChangeType: diff.ChangeContext, Content: "a"},
		{OldNum: 2, ChangeType: diff.ChangeRemove, Content: "b"},
		{NewNum: 2, ChangeType: diff.ChangeAdd, Content: "B"},
		{OldNum: 3, NewNum: 3, ChangeType: diff.ChangeContext, Content: "c"},
	}
	m := testModel([]string{"a.go"}, map[string][]diff.DiffLine{"a.go": lines})
	m.tree = testNewFileTree([]string{"a.go"})
	m.file.name, m.file.lines = "a.go", lines
	m.nav.diffCursor = idx
	m.revert.reverter = r
	return m
}

func pressKey(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	var msg tea.KeyMsg
	if len(key) == 1 {
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	} else {
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	}
	out, cmd := m.Update(msg)
	return out.(Model), cmd
}

func TestRevert_ChangeAfterConfirm(t *testing.T) {
	r := &fakeReverter{}
	m, cmd := pressKey(t, revertModel(r, 2), "x")
	assert.Nil(t, cmd)
	assert.Contains(t, m.transientHint(), "Revert this change? y to confirm")
	assert.Nil(t, r.change, "nothing before y")

	m, cmd = pressKey(t, m, "y")
	require.NotNil(t, cmd)
	msg := cmd()
	assert.Equal(t, []int{1, 2}, r.change, "the whole block: the removed and the added line")
	assert.Equal(t, "a.go", r.changeFile.Path)

	out, cmd := m.handleReverted(msg.(revertedMsg))
	m = out.(Model)
	assert.Equal(t, "Reverted the change in a.go", m.transientHint())
	require.NotNil(t, cmd)
	assert.IsType(t, RefreshMsg{}, cmd(), "a quiet reload shows the result and keeps the annotations")
}

func TestRevert_CancelledByAnyOtherKey(t *testing.T) {
	r := &fakeReverter{}
	m, _ := pressKey(t, revertModel(r, 1), "X")
	assert.Contains(t, m.transientHint(), "Revert a.go?")
	m, cmd := pressKey(t, m, "n")
	assert.Nil(t, cmd)
	assert.Nil(t, r.file)
	assert.Equal(t, "Revert cancelled", m.transientHint())
	assert.False(t, m.revert.pending)
}

func TestRevert_WholeFile(t *testing.T) {
	r := &fakeReverter{}
	m, _ := pressKey(t, revertModel(r, 0), "X")
	_, cmd := pressKey(t, m, "y")
	require.NotNil(t, cmd)
	cmd()
	require.NotNil(t, r.file)
	assert.Equal(t, "a.go", r.file.Path)
}

func TestRevert_NotOnAChange(t *testing.T) {
	r := &fakeReverter{}
	m, _ := pressKey(t, revertModel(r, 0), "x")
	assert.False(t, m.revert.pending)
	assert.Contains(t, m.transientHint(), "Put the cursor on a change")
}

func TestRevert_Unavailable(t *testing.T) {
	m, _ := pressKey(t, revertModel(nil, 2), "x")
	assert.False(t, m.revert.pending)
	assert.Contains(t, m.transientHint(), "local git review")
}

func TestRevert_FailureIsShown(t *testing.T) {
	m := revertModel(&fakeReverter{}, 2)
	out, cmd := m.handleReverted(revertedMsg{what: "a.go", err: errors.New("boom")})
	assert.Nil(t, cmd, "nothing to reload")
	assert.Equal(t, "Revert failed: boom", out.(Model).transientHint())
}

func TestRevert_AsksInThePopup(t *testing.T) {
	r := &fakeReverter{}
	m := revertModel(r, 2)
	var title, detail, button string
	var yes tea.Cmd
	m.revert.confirm = func(t, d, b string, y tea.Cmd) tea.Cmd {
		title, detail, button, yes = t, d, b, y
		return func() tea.Msg { return nil }
	}
	m, cmd := pressKey(t, m, "x")
	require.NotNil(t, cmd, "the popup opens")
	assert.False(t, m.revert.pending, "nothing waits on the status bar")
	assert.Empty(t, m.transientHint())
	assert.Equal(t, "Revert this change?", title)
	assert.Equal(t, "Undoes the 2 changed lines under the cursor in a.go.", detail)
	assert.Equal(t, "Revert", button)
	assert.Nil(t, r.change, "nothing before the popup is confirmed")

	msg := yes()
	assert.Equal(t, []int{1, 2}, r.change)
	assert.Equal(t, revertedMsg{what: "the change in a.go"}, msg)

	m.tree = testNewFileTree([]string{"a.go"})
	_, _ = pressKey(t, m, "X")
	assert.Equal(t, "Revert a.go?", title)
	assert.Equal(t, "Revert", button)
}
