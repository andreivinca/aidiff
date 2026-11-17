package splitview

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
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
	unknown := unknownCSIMsg(t, "\x1b[5;9z")
	hs := newHarness(t, 100, 30)
	hs.send(unknown)
	assert.Equal(t, []string{"\x1b[5;9z"}, hs.term(0).text, "a sequence the split cannot decode goes to the terminal")
	hs.send(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	hs.send(unknown)
	assert.Equal(t, []string{"\x1b[5;9z"}, hs.term(0).text, "with the diff focused it does not")
	assert.Contains(t, *hs.inner, unknown, "it goes to the diff instead")
}

// modifiedKeyTerm is a pane that takes modified keys, as a chat does.
type modifiedKeyTerm struct {
	*fakeTerm
	modified []uv.KeyPressEvent
}

func (f *modifiedKeyTerm) SendModifiedKey(k uv.KeyPressEvent) bool {
	if k.Code != uv.KeyEnter {
		return false
	}
	f.modified = append(f.modified, k)
	return true
}

func TestSplit_ModifiedKeys(t *testing.T) {
	for _, seq := range []string{"\x1b[27;2;13~", "\x1b[13;2u"} {
		shiftEnter := unknownCSIMsg(t, seq)

		hs := newHarness(t, 100, 30)
		hs.send(shiftEnter)
		assert.Equal(t, []string{"\r"}, hs.term(0).text, "%q reaches a shell as the enter it sends without the mode", seq)

		chat := &modifiedKeyTerm{fakeTerm: hs.term(0)}
		hs.m.terms[0] = chat
		hs.send(shiftEnter)
		assert.Equal(t, []uv.KeyPressEvent{{Code: uv.KeyEnter, Mod: uv.ModShift}}, chat.modified,
			"%q reaches a pane that takes modified keys with its modifier", seq)
		assert.Equal(t, []string{"\r"}, chat.text, "and nothing else reaches it")

		hs.send(unknownCSIMsg(t, "\x1b[27;6;65~"))
		assert.Equal(t, []string{"\r", "\x01"}, chat.text, "a key the pane does not take arrives as its plain key")

		hs.send(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
		hs.send(shiftEnter)
		assert.Contains(t, *hs.inner, tea.KeyMsg{Type: tea.KeyEnter}, "with the diff focused it is enter, as before")
	}
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

func TestSplit_ForceQuitSkipsTheDialog(t *testing.T) {
	hs := newHarness(t, 100, 30)
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	hs.key("open")
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotEmpty(t, hs.m.terms)

	hs.send(quitRequestMsg{})
	require.True(t, hs.m.quitDialog, "a plain quit asks first")

	cmd := hs.send(ForceQuitMsg{})
	require.NotNil(t, cmd)
	assert.False(t, hs.m.quitDialog, "the unanswerable dialog is gone")
	quit := hs.run(cmd)
	assert.IsType(t, tea.QuitMsg{}, quit)
	assert.Equal(t, quit, filterQuit(hs.m, quit), "the guard lets a forced quit through")
}
