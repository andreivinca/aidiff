package splitview

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type confirmedMsg struct{}

func TestConfirm_PopupAsksAndRuns(t *testing.T) {
	hs := newHarnessOpts(t, 100, 30, false)
	ran := 0
	yes := func() tea.Msg { ran++; return confirmedMsg{} }
	ask := ConfirmMsg{Title: "Revert a.go?", Detail: "Its changes are lost.", Confirm: "Revert", Yes: yes}

	hs.send(ask)
	require.NotNil(t, hs.m.confirm)
	view := ansi.Strip(hs.m.View())
	assert.Contains(t, view, "Revert a.go?")
	assert.Contains(t, view, "Its changes are lost.")
	assert.Contains(t, view, "y revert · n/esc cancel")

	before := len(*hs.inner)
	hs.key("j")
	assert.NotNil(t, hs.m.confirm, "another key keeps it open")
	assert.Len(t, *hs.inner, before, "and does not reach the review")
	assert.Nil(t, hs.send(tea.KeyMsg{Type: tea.KeyEnter}), "enter on Cancel, selected first, runs nothing")
	assert.Nil(t, hs.m.confirm)

	hs.send(ask)
	hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Nil(t, hs.m.confirm, "esc cancels")
	assert.Zero(t, ran)

	hs.send(ask)
	cmd := hs.key("y")
	assert.Nil(t, hs.m.confirm)
	require.NotNil(t, cmd)
	assert.Equal(t, confirmedMsg{}, cmd(), "y runs Yes")

	hs.send(ask)
	hs.send(tea.KeyMsg{Type: tea.KeyLeft})
	cmd = hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd, "enter on the confirm button runs it too")
	assert.Equal(t, 2, func() int { cmd(); return ran }())
}

func TestConfirm_SwallowsTheMouse(t *testing.T) {
	hs := newHarnessOpts(t, 100, 30, false)
	hs.send(ConfirmMsg{Title: "Sure?"})
	assert.True(t, hs.m.modal())
	assert.Equal(t, "OK", hs.m.confirm.msg.Confirm, "the confirm button is OK by default")
	before := len(*hs.inner)
	hs.send(tea.MouseMsg{X: 5, Y: 5, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	assert.Len(t, *hs.inner, before)
}

func TestConfirm_FromTheReviewReachesTheSplit(t *testing.T) {
	hs := newHarnessOpts(t, 100, 30, false)
	msg := tagMsg(hs.m.wsID, ConfirmMsg{Title: "Revert a.go?", Yes: func() tea.Msg { return confirmedMsg{} }})
	ask, ok := msg.(ConfirmMsg)
	require.True(t, ok, "a review's ConfirmMsg is the split's to show")
	hs.send(ask)
	require.NotNil(t, hs.m.confirm)
	cmd := hs.key("y")
	require.NotNil(t, cmd)
	assert.Equal(t, innerMsg{ws: hs.m.wsID, msg: confirmedMsg{}}, cmd(), "the answer goes back to the review that asked")
}
