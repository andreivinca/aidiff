package splitview

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chatTerm is a pane with its own word on what quitting closes, like a chat.
type chatTerm struct {
	*fakeTerm
}

func (chatTerm) QuitNote() string {
	return "Closes the chat. The agent keeps working."
}

func TestSplit_OpenPaneMsgWaitsForSizeAndQuitAsks(t *testing.T) {
	hs := newSplit((*harness).config)
	var chat *chatTerm
	hs.send(OpenPaneMsg{Start: func(w, h int) (Terminal, error) {
		chat = &chatTerm{newFakeTerm(w, h)}
		return chat, nil
	}})
	assert.Empty(t, hs.m.terms, "no window size yet: the request waits")

	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	require.Len(t, hs.m.terms, 1, "opened once the window has a size")
	require.NotNil(t, chat)
	assert.Equal(t, 0, hs.m.focus, "the new pane takes focus")
	assert.Less(t, hs.m.leftW, 100, "the diff made room for it")

	assert.Nil(t, hs.send(quitRequestMsg{}), "an open chat asks before quitting")
	assert.True(t, hs.m.quitDialog)
	assert.Contains(t, ansi.Strip(hs.m.View()), "Closes the chat. The agent keeps working.", "in the pane's own words")
	hs.key("n")
	assert.False(t, hs.m.quitDialog)

	// a shell beside it adds the shell's warning to the chat's note
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	hs.key("open")
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	require.Len(t, hs.m.terms, 2)
	hs.send(quitRequestMsg{})
	assert.True(t, hs.m.quitDialog)
	view := ansi.Strip(hs.m.View())
	assert.Contains(t, view, "Closes the terminal and everything running in it.")
	assert.Contains(t, view, "Closes the chat. The agent keeps working.")
	cmd := hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, hs.run(cmd))
}

func TestSplit_UnnamedSequenceReachesFocusedPane(t *testing.T) {
	shiftEnter := unknownCSIMsg(t, "\x1b[13;2u")
	hs := newHarness(t, 100, 30)
	hs.send(shiftEnter)
	assert.Equal(t, []string{"\x1b[13;2u"}, hs.term(0).text, "shift+enter's CSI u form goes to the terminal")
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	hs.send(shiftEnter)
	assert.Equal(t, []string{"\x1b[13;2u"}, hs.term(0).text, "with the diff focused it does not")
	assert.Contains(t, *hs.inner, shiftEnter, "it goes to the diff instead")
}

func TestSplit_CtrlQAlwaysAsks(t *testing.T) {
	hs := newHarnessOpts(t, 100, 30, false)
	require.Empty(t, hs.m.terms)
	assert.Nil(t, hs.send(tea.KeyMsg{Type: tea.KeyCtrlQ}), "ctrl+q asks even with nothing open")
	assert.True(t, hs.m.quitDialog)
	assert.Contains(t, ansi.Strip(hs.m.View()), "Leaves the review.")
	hs.key("n")
	assert.False(t, hs.m.quitDialog)

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	hs.key("quit")
	assert.Nil(t, hs.send(tea.KeyMsg{Type: tea.KeyEnter}), "the palette's Quit asks too")
	assert.True(t, hs.m.quitDialog)
	cmd := hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, hs.run(cmd))

	hs = newHarnessOpts(t, 100, 30, false)
	cmd = hs.key("q")
	require.NotNil(t, cmd)
	msg := hs.run(cmd)
	require.Equal(t, quitRequestMsg{}, msg, "the review's own quit is a request")
	cmd = hs.send(msg)
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, hs.run(cmd), "which goes through with nothing open")
}
