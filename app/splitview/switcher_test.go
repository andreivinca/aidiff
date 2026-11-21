package splitview

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The modified-key reports of ctrl+tab and ctrl+shift+tab, in both forms.
const (
	ctrlTab           = "\x1b[27;5;9~"
	ctrlTabCSIu       = "\x1b[9;5u"
	ctrlShiftTab      = "\x1b[27;6;9~"
	ctrlShiftTabCSIu  = "\x1b[9;6u"
	switcherTestWidth = 100
)

// fakeSessions describes the workspaces and records the switcher's calls.
type fakeSessions struct {
	info     map[string]SessionInfo
	notReady map[string]bool
	done     []string // the workspaces whose Done ran
	opened   []string // the workspaces Open was asked for
}

func (f *fakeSessions) Describe(key string) SessionInfo {
	return f.info[key]
}

func (f *fakeSessions) Show(key string) (func(WorkspaceSwitch), bool) {
	return func(WorkspaceSwitch) { f.done = append(f.done, key) }, !f.notReady[key]
}

func (f *fakeSessions) Open(key string) tea.Cmd {
	f.opened = append(f.opened, key)
	return func() tea.Msg { return SwitchWorkspaceMsg{Key: key} }
}

// newSwitcherHarness is a 100x30 split where workspaces a, b and c were
// opened in that order, so c is on screen and the order of use is c, b, a.
// a and b have a terminal each; sessions, when not nil, is Config.Sessions.
func newSwitcherHarness(t *testing.T, sessions *fakeSessions) *harness {
	t.Helper()
	return newSwitcherHarnessWith(t, func(cfg *Config) {
		if sessions != nil {
			cfg.Sessions = sessions
		}
	})
}

// newSwitcherHarnessWith is newSwitcherHarness with its config changed by
// tweak.
func newSwitcherHarnessWith(t *testing.T, tweak func(*Config)) *harness {
	t.Helper()
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.OpenOnStart = true
		cfg.Workspace = "a"
		tweak(&cfg)
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: switcherTestWidth, Height: 30})
	b, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b})
	hs.send(OpenTerminalMsg{})
	c, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "c", Model: c})
	require.Equal(t, "c", hs.m.Workspace())
	require.Equal(t, []string{"c", "b", "a"}, hs.m.sessionKeys())
	return hs
}

// raw sends the modified-key report seq as Bubble Tea delivers it.
func (hs *harness) raw(t *testing.T, seq string) tea.Cmd {
	t.Helper()
	return hs.send(unknownCSIMsg(t, seq))
}

// selected is the key of the switcher's selected row.
func (hs *harness) selected() string {
	return hs.m.switcher.selectedSession()
}

func TestSwitcher_BothEncodingsOpenAndMove(t *testing.T) {
	for _, seq := range [][2]string{{ctrlTab, ctrlShiftTab}, {ctrlTabCSIu, ctrlShiftTabCSIu}} {
		hs := newSwitcherHarness(t, nil)
		hs.raw(t, seq[0])
		require.True(t, hs.m.switcher.open, "%q opens the switcher", seq[0])
		assert.Equal(t, "b", hs.selected(), "on the session used before, as alt+tab does")
		assert.Equal(t, "b", hs.m.Workspace(), "which comes on screen at once")

		hs.raw(t, seq[0])
		assert.Equal(t, "a", hs.selected(), "%q moves down", seq[0])
		hs.raw(t, seq[1])
		assert.Equal(t, "b", hs.selected(), "%q moves up", seq[1])
	}

	hs := newSwitcherHarness(t, nil)
	hs.raw(t, ctrlShiftTab)
	require.True(t, hs.m.switcher.open, "ctrl+shift+tab opens it too")
	assert.Equal(t, "a", hs.selected(), "on the least recently used session")
}

func TestSwitcher_OpensWhateverHasFocus(t *testing.T) {
	t.Run("review", func(t *testing.T) {
		hs := newSwitcherHarness(t, nil)
		before := len(*hs.inner)
		hs.raw(t, ctrlTab)
		assert.True(t, hs.m.switcher.open)
		assert.Len(t, *hs.inner, before, "the review gets no tab")
	})
	t.Run("terminal", func(t *testing.T) {
		hs := newSwitcherHarness(t, nil)
		hs.send(SwitchWorkspaceMsg{Key: "a"}) // a's terminal has focus
		require.Equal(t, 0, hs.m.focus)
		term := hs.term(0)
		hs.raw(t, ctrlTab)
		assert.True(t, hs.m.switcher.open)
		assert.Empty(t, term.text, "the focused terminal gets neither the report nor a tab")
	})
	t.Run("sidebar", func(t *testing.T) {
		sb := &fakeSidebar{available: true}
		hs := newSwitcherHarness(t, nil)
		hs.m.cfg.Sidebar = sb
		hs.send(ShowSidebarMsg{})
		require.Equal(t, focusSidebar, hs.m.focus)
		hs.raw(t, ctrlTab)
		assert.True(t, hs.m.switcher.open)
		assert.Empty(t, sb.keys, "the sidebar gets no key")
		assert.True(t, hs.m.sideOpen, "and stays while the user picks")

		hs.send(tea.KeyMsg{Type: tea.KeyEnter})
		assert.Equal(t, "b", hs.m.Workspace())
		assert.False(t, hs.m.sideOpen, "left for another session, it hides, as picking from it does")
	})
	t.Run("fallback key", func(t *testing.T) {
		hs := newSwitcherHarness(t, nil)
		hs.send(SwitchWorkspaceMsg{Key: "a"})
		term := hs.term(0)
		hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true})
		require.True(t, hs.m.switcher.open, "alt+s opens it in any terminal")
		assert.Empty(t, term.text, "and is not typed into the terminal")
		hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true})
		assert.Equal(t, 2, hs.m.switcher.sel, "pressed again it moves on")
		hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S"), Alt: true})
		assert.Equal(t, 1, hs.m.switcher.sel, "alt+S moves back")
		assert.Empty(t, term.text)
	})
}

func TestSwitcher_NotUnderAnotherPopup(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	sel := hs.m.palette.sel
	hs.raw(t, ctrlTab)
	assert.False(t, hs.m.switcher.open, "not over the palette")
	assert.NotEqual(t, sel, hs.m.palette.sel, "where ctrl+tab moves the selection as tab does")
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true})
	assert.False(t, hs.m.switcher.open)
	hs.send(tea.KeyMsg{Type: tea.KeyEsc})

	hs.send(ConfirmMsg{Title: "Sure?"})
	hs.raw(t, ctrlTab)
	assert.False(t, hs.m.switcher.open, "not over a confirmation")
	hs.send(tea.KeyMsg{Type: tea.KeyEsc})

	hs.send(quitRequestMsg{})
	require.True(t, hs.m.quitDialog)
	hs.raw(t, ctrlTab)
	assert.False(t, hs.m.switcher.open, "not over the quit dialog")
}

func TestSwitcher_AnotherPopupTakesOver(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.raw(t, ctrlTab)
	hs.send(ConfirmMsg{Title: "Sure?"})
	assert.False(t, hs.m.switcher.open, "a popup arriving closes the switcher")
	assert.NotNil(t, hs.m.confirm)
	assert.Equal(t, []string{"b", "c", "a"}, hs.m.sessionKeys(), "on its selection")
}

func TestSwitcher_WrapsBothWaysShowingEachSession(t *testing.T) {
	sessions := &fakeSessions{}
	hs := newSwitcherHarness(t, sessions)
	hs.raw(t, ctrlTab)
	var onScreen []string
	for range 4 {
		onScreen = append(onScreen, hs.m.Workspace())
		hs.raw(t, ctrlTab)
	}
	assert.Equal(t, []string{"b", "a", "c", "b"}, onScreen, "down, wrapping past the last row")
	assert.Equal(t, []string{"b", "a", "c", "b", "a"}, sessions.done, "each switch carries the program's Done")

	onScreen = nil
	for range 4 {
		hs.raw(t, ctrlShiftTab)
		onScreen = append(onScreen, hs.m.Workspace())
	}
	assert.Equal(t, []string{"b", "c", "a", "b"}, onScreen, "up, wrapping past the first")
	assert.Equal(t, []string{"c", "b", "a"}, hs.m.switcher.keys, "the order stays frozen while it is open")
	assert.Equal(t, []string{"c", "b", "a"}, hs.m.mru)
}

func TestSwitcher_EnterCommits(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.raw(t, ctrlTab)
	hs.raw(t, ctrlTab)
	require.Equal(t, "a", hs.m.Workspace())
	inner, ok := hs.m.inner.(innerModel)
	require.True(t, ok)
	before := len(*inner.got)
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, "a", hs.m.Workspace())
	assert.Len(t, *inner.got, before, "enter is the switcher's")
	assert.Equal(t, []string{"a", "c", "b"}, hs.m.sessionKeys(), "the chosen session comes first")

	hs.raw(t, ctrlTab)
	assert.Equal(t, "c", hs.m.Workspace(), "so the next press goes back")
}

func TestSwitcher_AnyOtherKeyCommitsAndGoesNoFurther(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.raw(t, ctrlTab) // b, whose terminal has focus
	require.Equal(t, 0, hs.m.focus)
	term := hs.term(0)
	hs.key("x")
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, "b", hs.m.Workspace())
	assert.Empty(t, term.text, "closing the switcher does not also type")
	assert.Equal(t, []string{"b", "c", "a"}, hs.m.sessionKeys())

	hs.raw(t, ctrlTab)
	hs.raw(t, "\x1b[27;5;46~") // ctrl+., which has no legacy form
	assert.False(t, hs.m.switcher.open, "a key with no name commits too")
	assert.Equal(t, "c", hs.m.Workspace())
	assert.Empty(t, term.text)
}

func TestSwitcher_DigitCommitsItsRow(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.raw(t, ctrlTab)
	hs.key("9")
	assert.True(t, hs.m.switcher.open, "no row 9: still choosing")
	hs.key("3")
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, "a", hs.m.Workspace(), "row 3")
	assert.Equal(t, []string{"a", "c", "b"}, hs.m.sessionKeys())
}

func TestSwitcher_NothingClosesItOnATimer(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	cmd := hs.raw(t, ctrlTab)
	assert.Empty(t, flatten(cmd), "opening schedules nothing that could close it later")
	assert.Empty(t, flatten(hs.raw(t, ctrlTab)), "nor does moving")
	for _, msg := range []tea.Msg{tea.WindowSizeMsg{Width: switcherTestWidth, Height: 30}, FlashMsg{Text: "hi"}, FilesChangedMsg{}} {
		hs.send(msg)
		assert.True(t, hs.m.switcher.open, "%T leaves it open", msg)
	}
	time.Sleep(20 * time.Millisecond)
	assert.True(t, hs.m.switcher.open, "it stays until a key, a click or a release closes it")
	assert.Equal(t, "a", hs.selected())
}

func TestSwitcher_EscGoesBack(t *testing.T) {
	sessions := &fakeSessions{}
	hs := newSwitcherHarness(t, sessions)
	hs.raw(t, ctrlTab)
	hs.raw(t, ctrlTab)
	require.Equal(t, "a", hs.m.Workspace())
	hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, "c", hs.m.Workspace(), "back to the session it opened on")
	assert.Equal(t, "c", sessions.done[len(sessions.done)-1], "with the program's Done")
	assert.Equal(t, []string{"c", "b", "a"}, hs.m.sessionKeys(), "the order as it was")
}

func TestSwitcher_OtherSwitchesCountAsUse(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.send(SwitchWorkspaceMsg{Key: "a"}) // the sidebar, alt+1…9, a thread opening
	assert.Equal(t, []string{"a", "c", "b"}, hs.m.sessionKeys())
	d, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "d", Model: d})
	assert.Equal(t, []string{"d", "a", "c", "b"}, hs.m.sessionKeys(), "a new session comes first")

	hs.raw(t, ctrlTab)
	require.Equal(t, "a", hs.m.Workspace())
	hs.send(SwitchWorkspaceMsg{Key: "b"}) // a project opening that was asked for before
	assert.False(t, hs.m.switcher.open, "a switch from elsewhere takes over")
	assert.Equal(t, "b", hs.m.Workspace())
	assert.Equal(t, []string{"b", "d", "a", "c"}, hs.m.sessionKeys(), "and the preview it replaced was no use")
}

func TestSwitcher_SessionClosingWhileOpen(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.raw(t, ctrlTab)
	hs.raw(t, ctrlTab)
	require.Equal(t, "a", hs.selected())

	hs.m.parked = maps.Clone(hs.m.parked)
	delete(hs.m.parked, "b") // b closed meanwhile
	view := hs.m.View()
	assert.NotRegexp(t, `\d   b\s`, ansi.Strip(view), "the list follows at once")
	hs.raw(t, ctrlTab)
	assert.Equal(t, []string{"c", "a"}, hs.m.switcher.keys)
	assert.Equal(t, "c", hs.selected(), "the selection stays valid, moving on from a")

	hs.m.parked = maps.Clone(hs.m.parked)
	delete(hs.m.parked, "a")
	hs.raw(t, ctrlTab)
	assert.False(t, hs.m.switcher.open, "nothing left to switch to: it closes")
	assert.Equal(t, "c", hs.m.Workspace())
	assert.Equal(t, []string{"c"}, hs.m.sessionKeys())
}

func TestSwitcher_SelectedSessionClosingWhileOpen(t *testing.T) {
	sessions := &fakeSessions{notReady: map[string]bool{"a": true}}
	hs := newSwitcherHarness(t, sessions)
	hs.raw(t, ctrlTab)
	hs.raw(t, ctrlTab)
	require.Equal(t, "a", hs.selected())
	require.Equal(t, "b", hs.m.Workspace(), "a is not ready: b stays on screen")

	hs.m.parked = maps.Clone(hs.m.parked)
	delete(hs.m.parked, "a")
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, "b", hs.m.Workspace(), "the nearest session left is chosen")
	assert.Empty(t, sessions.opened)
}

func TestSwitcher_OneSessionFlashes(t *testing.T) {
	hs := newWorkspaceHarness(t, "a")
	cmd := hs.raw(t, ctrlTab)
	assert.Nil(t, cmd)
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, noOtherSession, hs.m.flash)
	assert.Contains(t, ansi.Strip(hs.m.View()), noOtherSession)
	assert.Empty(t, hs.term(0).text, "and nothing reaches the terminal")
}

func TestSwitcher_UnnamedSessionIsLeftOnlyByChoice(t *testing.T) {
	hs := newWorkspaceHarness(t, "a")
	unnamed, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "", Model: unnamed}) // never by aidiff: only the start is unnamed
	hs.send(OpenTerminalMsg{})
	term := hs.term(0)

	hs.raw(t, ctrlTab)
	require.True(t, hs.m.switcher.open)
	assert.True(t, hs.m.switcher.hold)
	assert.Equal(t, "", hs.m.Workspace(), "nothing is shown before a choice: leaving closes the unnamed workspace")
	assert.Contains(t, ansi.Strip(hs.m.View()), "closes when left")
	assert.Contains(t, ansi.Strip(hs.m.View()), "enter to switch")

	hs.raw(t, "\x1b[57442;1:3u")
	assert.True(t, hs.m.switcher.open, "letting go of ctrl does not leave it")
	hs.key("x")
	assert.False(t, hs.m.switcher.open, "a stray key closes it")
	assert.Equal(t, "", hs.m.Workspace(), "staying")
	assert.False(t, term.isClosed())

	hs.raw(t, ctrlTab)
	hs.send(tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	assert.Equal(t, "", hs.m.Workspace(), "so does a click outside it")

	hs.raw(t, ctrlTab)
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "a", hs.m.Workspace(), "enter leaves it")
	hs.m.Close()
	assert.True(t, term.isClosed(), "closing it, as any switch away does")
	assert.Equal(t, []string{"a"}, hs.m.sessionKeys())
}

func TestSwitcher_NotReadySessionOpensOnceChosen(t *testing.T) {
	sessions := &fakeSessions{notReady: map[string]bool{"a": true}, info: map[string]SessionInfo{"a": {Name: "alpha", Note: "disconnected"}}}
	hs := newSwitcherHarness(t, sessions)
	hs.raw(t, ctrlTab)
	hs.raw(t, ctrlTab)
	require.Equal(t, "a", hs.selected())
	assert.Equal(t, "b", hs.m.Workspace(), "a session whose connection dropped is not shown in passing")
	assert.Contains(t, ansi.Strip(hs.m.View()), "disconnected", "its row says why")

	cmd := hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "c", hs.m.Workspace(), "chosen, the screen goes back to where it opened")
	assert.Equal(t, []string{"a"}, sessions.opened, "while the program opens it its own way")
	assert.Equal(t, []string{"c", "b", "a"}, hs.m.sessionKeys())
	for _, msg := range flatten(cmd) {
		if sw, ok := msg.(SwitchWorkspaceMsg); ok {
			hs.send(sw)
		}
	}
	assert.Equal(t, "a", hs.m.Workspace())
	assert.Equal(t, []string{"a", "c", "b"}, hs.m.sessionKeys(), "it counts as used once on screen")
}

func TestSwitcher_PaletteCommand(t *testing.T) {
	hs := newWorkspaceHarness(t, "a")
	for _, r := range hs.m.commands() {
		assert.NotEqual(t, "Switch session…", r.label, "nothing to switch to: not listed")
	}

	hs = newSwitcherHarness(t, nil)
	hs.runPalette("switch session")
	require.True(t, hs.m.switcher.open, "the palette opens the switcher")
	assert.Zero(t, hs.m.switcher.held, "with no modifier held, so it waits for enter")
	assert.Equal(t, "b", hs.m.Workspace())
	hs.raw(t, "\x1b[57442;1:3u")
	assert.True(t, hs.m.switcher.open, "a ctrl let go is not its to act on")
	assert.Contains(t, ansi.Strip(hs.m.View()), "enter to switch")
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, "b", hs.m.Workspace())
}

func TestSwitcher_Mouse(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.raw(t, ctrlTab)
	b := hs.m.switcherBox()
	require.NotNil(t, b.lines)
	x0, y0 := boxOrigin(b.w, len(b.lines), hs.m.winW, hs.m.height)

	hs.send(tea.MouseMsg{X: x0 + 4, Y: y0 + b.top + 2, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	assert.Equal(t, "a", hs.selected(), "the wheel moves the selection")
	hs.send(tea.MouseMsg{X: x0 + 4, Y: y0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	assert.True(t, hs.m.switcher.open, "a click on the border does nothing")
	hs.send(tea.MouseMsg{X: x0 + 4, Y: y0 + b.top, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, "c", hs.m.Workspace(), "a click on a row commits that row")

	hs.raw(t, ctrlTab)
	hs.send(tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	assert.False(t, hs.m.switcher.open, "a click outside commits the selection")
	assert.Equal(t, "b", hs.m.Workspace())
}

func TestSwitcher_RowsDescribeTheSessions(t *testing.T) {
	sessions := &fakeSessions{info: map[string]SessionInfo{
		"a": {Name: "aidiff", Machine: "local", Path: "~/Projects"},
		"b": {Name: "aidiff", Machine: "devbox T3", Path: "/srv/work", Busy: true},
		"c": {Name: "no\x1b]52;c;aGk=\x07tes\x1b[31m", Machine: "local", Path: "~/Documents"},
	}}
	hs := newSwitcherHarness(t, sessions)
	hs.raw(t, ctrlTab)
	view := hs.m.View()
	assert.NotContains(t, view, "\x1b]52", "caller text is cleaned of escapes")
	plain := ansi.Strip(view)
	assert.Contains(t, plain, "Switch session")
	assert.Regexp(t, `1 ▸ notes\s+local\s+~/Documents\s+│`, plain, "the session it opened on is marked")
	assert.Regexp(t, `2   aidiff\s+devbox T3\s+/srv/work\s+● \$`, plain, "an agent working is marked")
	assert.Regexp(t, `3   aidiff\s+local\s+~/Projects\s+\$`, plain, "two of a name tell apart by machine and path")
	assert.Contains(t, plain, "● agent working · $ terminal open", "the legend names the glyphs")

	hs = newSwitcherHarness(t, nil)
	hs.raw(t, ctrlTab)
	plain = ansi.Strip(hs.m.View())
	assert.Regexp(t, `1 ▸ c\b`, plain, "without a description the row shows the key")
}

func TestSwitcher_FitsSmallWindows(t *testing.T) {
	long := strings.Repeat("very-long-project-name-", 4)
	sessions := &fakeSessions{info: map[string]SessionInfo{}}
	for _, k := range []string{"a", "b", "c"} {
		sessions.info[k] = SessionInfo{Name: long + k, Machine: "a-machine-with-a-long-name", Path: "/" + strings.Repeat("deep/", 20) + k, Busy: true, Note: "disconnected"}
	}
	hs := newSwitcherHarness(t, sessions)
	for i := range 20 { // more sessions than any window here shows
		m, _ := otherInner()
		hs.send(SwitchWorkspaceMsg{Key: "w" + strconv.Itoa(i), Model: m})
	}
	for _, size := range [][2]int{{100, 30}, {72, 20}, {40, 12}, {31, 10}, {24, 8}, {16, 6}, {12, 4}, {9, 3}, {30, 2}, {5, 1}} {
		hs.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		hs.raw(t, ctrlTab)
		for range 15 {
			hs.raw(t, ctrlTab) // scrolled down the list
		}
		require.True(t, hs.m.switcher.open, "%v", size)
		lines := strings.Split(hs.m.View(), "\n")
		assert.Len(t, lines, size[1], "%v: as tall as the window", size)
		for i, l := range lines {
			assert.Equal(t, size[0], ansi.StringWidth(l), "%v: line %d within the window", size, i)
		}
		plain := ansi.Strip(strings.Join(lines, "\n"))
		if size[1] >= 3 && size[0] >= 12 {
			assert.Contains(t, plain, "w3", "%v: the selected row stays in view", size)
		}
		hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	}
}

func TestSwitcher_PlainTabWithoutModifiedKeys(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.send(SwitchWorkspaceMsg{Key: "a"})
	term := hs.term(0)
	hs.send(tea.KeyMsg{Type: tea.KeyTab})
	assert.False(t, hs.m.switcher.open, "a terminal that cannot report ctrl+tab sends tab, which stays tab")
	assert.Equal(t, []string{"\t"}, term.text)

	hs.send(tea.KeyMsg{Type: tea.KeyShiftTab})
	assert.False(t, hs.m.switcher.open)
	assert.Equal(t, []string{"\t", "\x1b[Z"}, term.text)
}

func TestShiftedKey(t *testing.T) {
	assert.Equal(t, "alt+S", shiftedKey("alt+s"))
	assert.Equal(t, "ctrl+alt+X", shiftedKey("ctrl+alt+x"))
	assert.Empty(t, shiftedKey("f9"))
	assert.Empty(t, shiftedKey("alt+1"))
	assert.Empty(t, shiftedKey("alt+S"))
}

// flatten runs cmd and the batches and sequences it returns, and lists the
// messages they produce.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	var out []tea.Msg
	if _, ok := mapBatch(msg, func(c tea.Cmd) tea.Cmd {
		out = append(out, flatten(c)...)
		return c
	}); ok {
		return out
	}
	if s, ok := msg.(scopedMsg); ok {
		msg = s.msg
	}
	return slices.DeleteFunc([]tea.Msg{msg}, func(m tea.Msg) bool { return m == nil })
}
