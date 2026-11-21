package splitview

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDialog_QuitAndConfirmAreOneDialog(t *testing.T) {
	hs := newHarness(t, 100, 30)
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlQ})
	require.True(t, hs.m.quitDialog)
	quit := hs.m.quitDialogView()
	hs.send(tea.KeyMsg{Type: tea.KeyEsc})

	hs.send(ConfirmMsg{Title: "Quit aidiff?", Detail: "Closes the terminal and everything running in it.", Confirm: "Quit"})
	assert.Equal(t, quit, hs.m.confirmView(), "the same question looks the same in either")
}

func TestDialog_ConfirmTakesTheQuitDialogsKeys(t *testing.T) {
	hs := newHarnessOpts(t, 100, 30, false)
	yes := func() tea.Msg { return confirmedMsg{} }
	hs.send(ConfirmMsg{Title: "Revert a.go?", Yes: yes})

	hs.key("q")
	assert.NotNil(t, hs.m.confirm, "q is just another key, as in the quit dialog")
	assert.Nil(t, hs.send(tea.KeyMsg{Type: tea.KeyCtrlQ}))
	assert.NotNil(t, hs.m.confirm, "the quit key confirms a quit, never a revert")
	hs.key("l")
	assert.Equal(t, confirmButton, hs.m.confirm.sel)
	cmd := hs.send(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	require.NotNil(t, cmd, "space presses the selected button")
	assert.Equal(t, confirmedMsg{}, cmd())
}
