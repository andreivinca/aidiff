package splitview

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chatTerm is a pane with its own word on what quitting closes, like a chat.
type chatTerm struct{ *fakeTerm }

func (chatTerm) QuitNote() string { return "Closes the chat. The agent keeps working." }

func TestSplit_OpenPaneMsgWaitsForSizeAndQuitAsks(t *testing.T) {
	got := []tea.Msg{}
	hs := &harness{inner: &got}
	hs.m = New(innerModel{got: &got, quitKey: "q"}, Config{
		WidthPercent: 40,
		Start: func(cw, ch int) (Terminal, error) {
			ft := newFakeTerm(cw, ch)
			hs.started = append(hs.started, ft)
			return ft, nil
		},
	})
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
	assert.True(t, hs.m.confirmQuit)
	assert.Contains(t, ansi.Strip(hs.m.View()), "Closes the chat. The agent keeps working.", "in the pane's own words")
	hs.key("n")
	assert.False(t, hs.m.confirmQuit)

	// a shell beside it brings the shell's warning instead
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	hs.key("open")
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	require.Len(t, hs.m.terms, 2)
	hs.send(quitRequestMsg{})
	assert.True(t, hs.m.confirmQuit)
	assert.Contains(t, ansi.Strip(hs.m.View()), "Closes the terminal and everything running in it.")
	cmd := hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

// unknownCSISequenceMsg stands in for Bubble Tea's unexported message of the
// same name: rawSequence recognizes it by that name.
type unknownCSISequenceMsg []byte

func TestSplit_UnnamedSequenceReachesFocusedPane(t *testing.T) {
	hs := newHarness(t, 100, 30)
	hs.send(unknownCSISequenceMsg("\x1b[13;2u"))
	assert.Equal(t, []string{"\x1b[13;2u"}, hs.term(0).text, "shift+enter's CSI u form goes to the terminal")
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	hs.send(unknownCSISequenceMsg("\x1b[13;2u"))
	assert.Equal(t, []string{"\x1b[13;2u"}, hs.term(0).text, "with the diff focused it does not")
}

func TestSplit_CtrlQAlwaysAsks(t *testing.T) {
	hs := newHarnessOpts(t, 100, 30, false)
	require.Empty(t, hs.m.terms)
	assert.Nil(t, hs.send(tea.KeyMsg{Type: tea.KeyCtrlQ}), "ctrl+q asks even with nothing open")
	assert.True(t, hs.m.confirmQuit)
	assert.Contains(t, ansi.Strip(hs.m.View()), "Leaves the review.")
	hs.key("n")
	assert.False(t, hs.m.confirmQuit)

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	hs.key("quit")
	assert.Nil(t, hs.send(tea.KeyMsg{Type: tea.KeyEnter}), "the palette's Quit asks too")
	assert.True(t, hs.m.confirmQuit)
	cmd := hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())

	cmd = hs.send(quitRequestMsg{})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd(), "the review's own quit still goes through with nothing open")
}
