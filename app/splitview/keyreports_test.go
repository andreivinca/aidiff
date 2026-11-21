package splitview

import (
	"os/exec"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Kitty protocol reports the tests type: the flags reply, and the modifier
// keys let go.
const (
	kittyReply       = "\x1b[?11u"
	ctrlLetGo        = "\x1b[57442;1:3u"
	rightCtrlLetGo   = "\x1b[57448;1:3u"
	shiftLetGoOnCtrl = "\x1b[57441;5:3u" // shift up, ctrl still down
	altLetGo         = "\x1b[57443;1:3u"
)

// termLog records what the split writes to the terminal.
type termLog struct {
	writes []string
}

func (l *termLog) write(seq string) {
	l.writes = append(l.writes, seq)
}

// count is how many times seq was written.
func (l *termLog) count(seq string) int {
	n := 0
	for _, w := range l.writes {
		if w == seq {
			n++
		}
	}
	return n
}

// newKeyHarness is newSwitcherHarness writing to a recorded terminal; with
// kitty the terminal speaks the kitty protocol and has said so, as it does
// after the first push.
func newKeyHarness(t *testing.T, kitty bool) (*harness, *termLog) {
	t.Helper()
	log := &termLog{}
	hs := newSwitcherHarnessWith(t, func(cfg *Config) { cfg.Output = log.write })
	if kitty {
		hs.send(unknownCSIMsg(t, kittyReply))
		require.True(t, hs.m.keys.releases())
	}
	return hs, log
}

// assertBalanced checks that every push was popped and nothing is left on
// the terminal's stack.
func assertBalanced(t *testing.T, hs *harness, log *termLog, pushes int, what string) {
	t.Helper()
	assert.Equal(t, pushes, log.count(pushKeyReports), "%s: pushed", what)
	assert.Equal(t, pushes, log.count(popKeyReports), "%s: popped as often", what)
	assert.False(t, hs.m.keys.pushed, "%s: nothing left pushed", what)
}

func TestKeyReports_PushedWhileOpenPoppedOnEveryWayOut(t *testing.T) {
	ways := map[string]func(t *testing.T, hs *harness){
		"enter":       func(_ *testing.T, hs *harness) { hs.send(tea.KeyMsg{Type: tea.KeyEnter}) },
		"esc":         func(_ *testing.T, hs *harness) { hs.send(tea.KeyMsg{Type: tea.KeyEsc}) },
		"ctrl let go": func(t *testing.T, hs *harness) { hs.raw(t, ctrlLetGo) },
		"another key": func(_ *testing.T, hs *harness) { hs.key("x") },
		"a digit":     func(_ *testing.T, hs *harness) { hs.key("3") },
		"a click": func(_ *testing.T, hs *harness) {
			hs.send(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		},
		"another popup": func(_ *testing.T, hs *harness) { hs.send(ConfirmMsg{Title: "Sure?"}) },
		"a switch from elsewhere": func(_ *testing.T, hs *harness) {
			hs.send(SwitchWorkspaceMsg{Key: "a"})
		},
		"its sessions closing": func(t *testing.T, hs *harness) {
			hs.m.parked = nil // every other session gone
			hs.raw(t, ctrlTab)
		},
		"the program quitting": func(_ *testing.T, hs *harness) {
			hs.m.quitConfirmed = true
			assert.Equal(t, tea.QuitMsg{}, filterQuit(hs.m, tea.QuitMsg{}), "the quit goes on")
		},
		"an external program": func(_ *testing.T, hs *harness) {
			msg := tea.ExecProcess(exec.Command("true"), nil)()
			assert.Equal(t, msg, filterQuit(hs.m, msg))
		},
	}
	for name, close := range ways {
		t.Run(name, func(t *testing.T) {
			hs, log := newKeyHarness(t, false)
			hs.raw(t, ctrlTab)
			require.True(t, hs.m.switcher.open)
			require.Equal(t, []string{pushKeyReports, queryKeyReports}, log.writes, "opening pushes the flags and asks what took")
			close(t, hs)
			assertBalanced(t, hs, log, 1, name)
		})
	}

	hs, log := newKeyHarness(t, false)
	hs.raw(t, ctrlTab)
	hs.raw(t, ctrlTab)
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	hs.raw(t, ctrlTab)
	hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	assertBalanced(t, hs, log, 2, "twice")
	assert.Equal(t, 2, log.count(queryKeyReports), "asked again while no answer came")

	hs.m.quitConfirmed = true
	filterQuit(hs.m, tea.QuitMsg{})
	assertBalanced(t, hs, log, 2, "a quit with nothing pushed pops nothing")

	hs, log = newKeyHarness(t, false)
	hs.runPalette("switch session")
	require.True(t, hs.m.switcher.open)
	assert.Empty(t, log.writes, "opened from the palette nothing is held, so nothing is asked of the terminal")
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Empty(t, log.writes)
}

func TestKeyReports_SuspendPopsAndResumePushesAgain(t *testing.T) {
	hs, log := newKeyHarness(t, true)
	hs.raw(t, ctrlTab)
	filterQuit(hs.m, tea.SuspendMsg{})
	assert.False(t, hs.m.keys.pushed, "taken back before the terminal is handed over")
	assert.Equal(t, 1, log.count(popKeyReports))
	hs.send(tea.ResumeMsg{})
	assert.True(t, hs.m.keys.pushed, "and pushed again once it is back, the switcher still open")
	assert.True(t, hs.m.switcher.open)
	hs.raw(t, ctrlLetGo)
	assertBalanced(t, hs, log, 2, "suspended")
}

func TestKeyReports_ReplyIsNeverTyped(t *testing.T) {
	hs, log := newKeyHarness(t, false)
	hs.send(SwitchWorkspaceMsg{Key: "b"}) // its terminal has focus
	term := hs.term(0)
	hs.raw(t, kittyReply) // a late answer, the switcher closed
	assert.Empty(t, term.text, "the answer reaches no pane")
	assert.True(t, hs.m.keys.releases(), "it is remembered")

	hs.raw(t, ctrlTab)
	assert.Equal(t, []string{pushKeyReports}, log.writes, "once answered, nothing is asked again")
	hs.raw(t, "\x1b[?0u")
	assert.True(t, hs.m.switcher.open, "an answer while open is no key")
	assert.False(t, hs.m.keys.releases(), "a terminal that took no flags reports no releases")
	hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Empty(t, term.text)
}

func TestSwitcher_LettingGoOfCtrlCommits(t *testing.T) {
	for _, letGo := range []string{ctrlLetGo, rightCtrlLetGo} {
		hs, _ := newKeyHarness(t, true)
		hs.raw(t, ctrlTab) // before the flags took: the old encoding
		assert.Contains(t, ansi.Strip(hs.m.View()), "release ctrl to switch")
		hs.raw(t, "\x1b[9;5:2u") // tab held down: repeats
		assert.Equal(t, "a", hs.selected(), "a repeat moves as a press does")
		hs.raw(t, "\x1b[9;5u")
		assert.Equal(t, "c", hs.selected())
		for _, up := range []string{"\x1b[9;5:3u", "\x1b[106;5:3u", "\x1b[1;5:3B", "\x1b[13;5:3u"} {
			hs.raw(t, up)
			assert.True(t, hs.m.switcher.open, "%q: another key let go changes nothing", up)
		}
		hs.raw(t, "\x1b[57442;5u")
		assert.True(t, hs.m.switcher.open, "ctrl pressed again: still held")
		hs.raw(t, ctrlTab)
		require.Equal(t, "b", hs.selected())

		hs.raw(t, letGo)
		assert.False(t, hs.m.switcher.open, "%q commits", letGo)
		assert.Equal(t, "b", hs.m.Workspace())
		assert.Equal(t, []string{"b", "c", "a"}, hs.m.sessionKeys())
	}
}

func TestSwitcher_LettingGoOfShiftAloneDoesNot(t *testing.T) {
	hs, _ := newKeyHarness(t, true)
	hs.raw(t, ctrlShiftTab)
	require.Equal(t, "a", hs.selected())
	hs.raw(t, shiftLetGoOnCtrl)
	assert.True(t, hs.m.switcher.open, "ctrl is still down")
	hs.raw(t, "\x1b[57441;6u") // shift down again
	hs.raw(t, "\x1b[9;6u")     // ctrl+shift+tab
	assert.Equal(t, "b", hs.selected())
	hs.raw(t, ctrlLetGo)
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, "b", hs.m.Workspace())
}

func TestSwitcher_LettingGoOfAltCommitsTheFallbackKey(t *testing.T) {
	hs, _ := newKeyHarness(t, true)
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true})
	require.True(t, hs.m.switcher.open)
	assert.Contains(t, ansi.Strip(hs.m.View()), "release alt to switch")
	hs.raw(t, "\x1b[115;3u") // alt+s
	assert.Equal(t, "a", hs.selected())
	hs.raw(t, "\x1b[115;4u") // alt+S
	assert.Equal(t, "b", hs.selected())
	hs.raw(t, "\x1b[57442;3:3u") // a ctrl let go, alt still down
	assert.True(t, hs.m.switcher.open, "not the modifier that opened it")
	hs.raw(t, altLetGo)
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, "b", hs.m.Workspace())
}

func TestSwitcher_LockBitsAreIgnored(t *testing.T) {
	hs, _ := newKeyHarness(t, true)
	hs.raw(t, "\x1b[9;69u") // ctrl+tab with caps lock on
	require.True(t, hs.m.switcher.open)
	hs.raw(t, "\x1b[9;197u") // and num lock
	assert.Equal(t, "a", hs.selected())
	hs.raw(t, "\x1b[9;69:3u")
	assert.True(t, hs.m.switcher.open, "caps lock is no modifier let go")
	hs.raw(t, "\x1b[57442;65:3u")
	assert.False(t, hs.m.switcher.open, "ctrl let go with caps lock on")
	assert.Equal(t, "a", hs.m.Workspace())
}

func TestSwitcher_KeysWithCtrlHeldAreTheKeys(t *testing.T) {
	hs, _ := newKeyHarness(t, true)
	hs.raw(t, ctrlTab)
	hs.raw(t, "\x1b[106;5u") // ctrl+j
	assert.Equal(t, "a", hs.selected())
	hs.raw(t, "\x1b[107;5u") // ctrl+k
	assert.Equal(t, "b", hs.selected())
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlDown}) // Bubble Tea names ctrl+↓
	assert.Equal(t, "a", hs.selected())
	hs.raw(t, "\x1b[1;5:2A") // ctrl+↑ held down
	assert.Equal(t, "b", hs.selected())
	hs.raw(t, "\x1b[51;5u") // ctrl+3
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, "a", hs.m.Workspace(), "row 3")
}

func TestSwitcher_OldEncodingsWhilePushed(t *testing.T) {
	hs, _ := newKeyHarness(t, true)
	hs.raw(t, ctrlTab)
	require.True(t, hs.m.keys.pushed)
	hs.raw(t, ctrlTab) // the flags not taken yet: modifyOtherKeys
	assert.Equal(t, "a", hs.selected())
	hs.send(tea.KeyMsg{Type: tea.KeyTab}) // a terminal that sends ctrl+tab as tab
	assert.Equal(t, "c", hs.selected())
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, "c", hs.m.Workspace())
}

func TestSwitcher_WithoutReleasesEnterSwitches(t *testing.T) {
	for name, hs := range map[string]*harness{
		"no answer": func() *harness { hs, _ := newKeyHarness(t, false); return hs }(),
		"no output": newSwitcherHarness(t, nil),
	} {
		hs.raw(t, ctrlTab)
		assert.Contains(t, ansi.Strip(hs.m.View()), "enter to switch", name)
		for range 3 {
			hs.send(FilesChangedMsg{})
		}
		assert.True(t, hs.m.switcher.open, "%s: nothing closes it but the user", name)
		hs.raw(t, "\x1b[106u") // j: in this terminal no proof of anything
		assert.Equal(t, "a", hs.selected(), name)
		hs.send(tea.KeyMsg{Type: tea.KeyEnter})
		assert.False(t, hs.m.switcher.open, name)
		assert.Equal(t, "a", hs.m.Workspace(), name)
	}
}

func TestSwitcher_QuickTapCommitsOnTheNextEvent(t *testing.T) {
	// ctrl went up before the terminal took the flags: no release comes, but
	// every event after it says ctrl is not down
	tests := []struct {
		name, seq string
		msg       tea.Msg
		want      string // the workspace on screen after it
	}{
		{name: "tab let go", seq: "\x1b[9;1:3u", want: "b"},
		{name: "j pressed", seq: "\x1b[106u", want: "b"},
		{name: "shift pressed", seq: "\x1b[57441;2u", want: "b"},
		{name: "↓ named by Bubble Tea", msg: tea.KeyMsg{Type: tea.KeyDown}, want: "b"},
		{name: "esc still goes back", seq: "\x1b[27u", want: "c"},
		{name: "a digit still picks its row", seq: "\x1b[51u", want: "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs, log := newKeyHarness(t, true)
			hs.raw(t, ctrlTab)
			if tt.msg != nil {
				hs.send(tt.msg)
			} else {
				hs.raw(t, tt.seq)
			}
			assert.False(t, hs.m.switcher.open)
			assert.Equal(t, tt.want, hs.m.Workspace())
			assertBalanced(t, hs, log, 1, tt.name)
		})
	}
}

func TestKeyReports_StrayReportsAfterItClosedAreDropped(t *testing.T) {
	hs, _ := newKeyHarness(t, true)
	hs.raw(t, ctrlTab)
	hs.raw(t, ctrlLetGo) // b, whose terminal has focus
	require.Equal(t, "b", hs.m.Workspace())
	term := hs.term(0)
	for _, seq := range []string{"\x1b[9;5:3u", ctrlLetGo, "\x1b[57441;2u", "\x1b[57358u", kittyReply} {
		hs.raw(t, seq)
	}
	assert.Empty(t, term.text, "releases and modifier keys, reported before the terminal took the flags back, type nothing")
	hs.raw(t, "\x1b[108u")
	assert.Equal(t, []string{"l"}, term.text, "a key typed in that moment arrives as itself")
}

func TestHandsOverTerminal(t *testing.T) {
	// fails when a Bubble Tea upgrade renames the message it runs an
	// external program with
	for _, msg := range []tea.Msg{tea.QuitMsg{}, tea.SuspendMsg{}, tea.InterruptMsg{}, tea.ExecProcess(exec.Command("true"), nil)()} {
		assert.True(t, handsOverTerminal(msg), "%T", msg)
	}
	for _, msg := range []tea.Msg{tea.KeyMsg{}, tea.ResumeMsg{}, nil} {
		assert.False(t, handsOverTerminal(msg), "%T", msg)
	}
}
