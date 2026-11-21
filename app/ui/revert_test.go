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
	m.revert.confirm = func(t, d, b string, y, _ tea.Cmd) tea.Cmd {
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

// popupRevertModel is revertModel asking in a popup, refreshes otherwise
// allowed; *yes is the popup's confirm command once asked.
func popupRevertModel(t *testing.T, r Reverter, yes *tea.Cmd) Model {
	t.Helper()
	m := revertModel(r, 2)
	m.reload.applicable, m.filesLoaded = true, true
	m.revert.confirm = func(_, _, _ string, y, _ tea.Cmd) tea.Cmd {
		*yes = y
		return func() tea.Msg { return nil }
	}
	require.True(t, m.CanRefresh())
	return m
}

func TestRevert_PopupHoldsRefreshesUntilAnswered(t *testing.T) {
	var yes tea.Cmd
	m, cmd := pressKey(t, popupRevertModel(t, &fakeReverter{}, &yes), "x")
	require.NotNil(t, cmd)
	assert.False(t, m.CanRefresh(), "the lines asked about stay on screen while the popup is open")
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "the popup and the wait's expiry")
	assert.Len(t, batch, 2)

	out, _ := m.Update(yes()) // confirmed: the revert reports back
	assert.True(t, out.(Model).CanRefresh())
}

func TestRevert_PopupHoldEndsWhenTheReviewGetsInputAgain(t *testing.T) {
	var yes tea.Cmd
	asked, _ := pressKey(t, popupRevertModel(t, &fakeReverter{}, &yes), "x")
	require.False(t, asked.CanRefresh())

	m, _ := pressKey(t, asked, "j") // the popup took the cancel; this key reached the review
	assert.True(t, m.CanRefresh(), "a key")

	out, _ := asked.Update(tea.MouseMsg{X: 5, Y: 5, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	assert.True(t, out.(Model).CanRefresh(), "a mouse event")
}

func TestRevert_PopupCancelEndsTheHoldAtOnce(t *testing.T) {
	var no []tea.Cmd
	m := revertModel(&fakeReverter{}, 2)
	m.reload.applicable, m.filesLoaded = true, true
	m.revert.confirm = func(_, _, _ string, _, n tea.Cmd) tea.Cmd {
		no = append(no, n)
		return func() tea.Msg { return nil }
	}
	asked, _ := pressKey(t, m, "x")
	require.False(t, asked.CanRefresh())
	require.Len(t, no, 1)
	require.NotNil(t, no[0], "the popup is told how to say no")

	out, _ := asked.Update(no[0]())
	assert.True(t, out.(Model).CanRefresh(), "cancelled: refreshes go on without waiting for the expiry")

	again, _ := pressKey(t, out.(Model), "x")
	require.Len(t, no, 2)
	out, _ = again.Update(no[0]())
	assert.False(t, out.(Model).CanRefresh(), "the first popup's no ends nothing of the second")
	out, _ = again.Update(no[1]())
	assert.True(t, out.(Model).CanRefresh())
}

func TestRevert_PopupHoldExpires(t *testing.T) {
	var yes tea.Cmd
	m, _ := pressKey(t, popupRevertModel(t, &fakeReverter{}, &yes), "x")
	out, _ := m.Update(revertAskEndedMsg{seq: m.revert.askSeq - 1})
	assert.False(t, out.(Model).CanRefresh(), "an earlier popup's expiry ends nothing")
	out, _ = m.Update(revertAskEndedMsg{seq: m.revert.askSeq})
	assert.True(t, out.(Model).CanRefresh(), "dismissed unseen, a workspace switch say: the wait ends anyway")
}

func TestRevert_NamesAreSanitized(t *testing.T) {
	r := &fakeReverter{}
	m := revertModel(r, 2)
	name := "a\x1b[31m\nb.go"
	m.tree = testNewFileTree([]string{name})
	m.file.name = name
	var title, detail string
	var yes tea.Cmd
	m.revert.confirm = func(t, d, _ string, y, _ tea.Cmd) tea.Cmd {
		title, detail, yes = t, d, y
		return nil
	}
	m, _ = pressKey(t, m, "X")
	assert.Equal(t, "Revert a[31mb.go?", title)
	assert.NotContains(t, detail, "\x1b")
	assert.NotContains(t, detail, "\n")
	out, _ := m.handleReverted(yes().(revertedMsg))
	assert.Equal(t, "Reverted a[31mb.go", out.(Model).transientHint())

	m.revert.confirm = nil
	m, _ = pressKey(t, m, "X")
	assert.NotContains(t, m.transientHint(), "\x1b", "the status-bar question too")
}

func TestRevert_MouseClearsTheHint(t *testing.T) {
	m := revertModel(&fakeReverter{}, 2)
	m.revert.hint = "Reverted a.go"
	out, _ := m.Update(tea.MouseMsg{X: 5, Y: 5, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	assert.Empty(t, out.(Model).revert.hint)
}
