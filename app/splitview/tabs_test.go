package splitview

import (
	"math"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stripLine is the window's top row, unstyled.
func (hs *harness) stripLine() string {
	return ansi.Strip(strings.Split(hs.m.View(), "\n")[0])
}

// clickAt presses the left button at window cell (x, y).
func (hs *harness) clickAt(x, y int) tea.Cmd {
	return hs.send(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
}

// zoneOf is the strip's zone for key, its close button when close is set.
func (hs *harness) zoneOf(t *testing.T, key string, close bool) tabZone {
	t.Helper()
	for _, z := range hs.m.tabStrip().zones {
		if z.key == key && z.close == close && z.from < z.to {
			if !close && z.to-z.from <= tabMarkW && z.from == 0 {
				continue // the ‹ mark, which names the tab beyond it
			}
			return z
		}
	}
	require.Failf(t, "no zone", "tab %q close=%v", key, close)
	return tabZone{}
}

// truecolor draws lipgloss colors for the test, as aidiff does.
func truecolor(t *testing.T) {
	t.Helper()
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
}

func TestTabs_OrderOfOpeningNeverChanges(t *testing.T) {
	hs := newSwitcherHarness(t, nil) // a, b, c opened in that order
	assert.Equal(t, []string{"a", "b", "c"}, hs.m.tabKeys())
	hs.send(SwitchWorkspaceMsg{Key: "a"})
	assert.Equal(t, []string{"a", "b", "c"}, hs.m.tabKeys(), "going back to a keeps the order")
	assert.Equal(t, []string{"a", "c", "b"}, hs.m.sessionKeys(), "while the switcher's order moves it to the front")
	d, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "d", Model: d})
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: d, Replace: true})
	assert.Equal(t, []string{"a", "b", "c", "d"}, hs.m.tabKeys(), "a new one goes last; a replaced review keeps its tab")
	assert.True(t, strings.HasPrefix(hs.stripLine(), "▌1 a $ × "), "numbered from 1, a terminal open in a: %q", hs.stripLine())
	assert.Contains(t, hs.stripLine(), "▌4 d × ")
}

func TestTabs_NumberKeysSwitch(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	assert.Equal(t, "a", hs.m.Workspace(), "alt+1")
	hs.raw(t, "\x1b[27;5;50~") // ctrl+2, modifyOtherKeys
	assert.Equal(t, "b", hs.m.Workspace(), "ctrl+2")
	hs.raw(t, "\x1b[51;5u") // ctrl+3, CSI u
	assert.Equal(t, "c", hs.m.Workspace(), "ctrl+3")
	hs.raw(t, "\x1b[49;3u") // alt+1, CSI u
	assert.Equal(t, "a", hs.m.Workspace(), "alt+1 in its CSI u form")
	assert.Equal(t, []string{"a", "c", "b"}, hs.m.sessionKeys(), "a switch by number counts as a use")

	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}, Alt: true})
	assert.Equal(t, "a", hs.m.Workspace())
	assert.Equal(t, "there is no tab 7", hs.m.flash)

	before := len(hs.term(0).text)
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	assert.Equal(t, "b", hs.m.Workspace(), "a focused terminal gives alt+digits up too")
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	assert.Len(t, hs.term(0).text, before)
}

func TestTabs_StripShowsFromTwoTabsAndRelaysOut(t *testing.T) {
	hs := newWorkspaceHarness(t, "a")
	assert.Zero(t, hs.m.top, "one tab: no strip")
	assert.Equal(t, tea.WindowSizeMsg{Width: 60, Height: 30}, hs.lastInnerSize())
	assert.Equal(t, 30, hs.term(0).h)

	b, bGot := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b})
	assert.Equal(t, 1, hs.m.top, "two tabs: the strip takes the top row")
	assert.Contains(t, *bGot, tea.WindowSizeMsg{Width: 100, Height: 29}, "the review under it is a row shorter")
	lines := strings.Split(hs.m.View(), "\n")
	require.Len(t, lines, 30)
	assert.True(t, strings.HasPrefix(ansi.Strip(lines[0]), "▌1 a $ ×  ▌2 b × "), "%q", ansi.Strip(lines[0]))
	assert.Equal(t, "inner", strings.TrimSpace(ansi.Strip(lines[1])), "the review starts under it")
	for i, l := range lines {
		assert.Equal(t, 100, ansi.StringWidth(l), "row %d spans the window", i)
	}

	hs.send(OpenTerminalMsg{})
	assert.Equal(t, 29, hs.term(0).h, "a pane is a row shorter too")
	hs.send(SwitchWorkspaceMsg{Key: "a"})
	assert.Equal(t, 29, hs.term(0).h, "and a parked one once it is back")
	assert.Equal(t, tea.WindowSizeMsg{Width: 60, Height: 29}, hs.lastInnerSize())

	hs.send(closeTabMsg{key: "b"})
	assert.Zero(t, hs.m.top, "back to one tab: the strip goes")
	assert.Equal(t, tea.WindowSizeMsg{Width: 60, Height: 30}, hs.lastInnerSize(), "the review gets its row back")
	assert.Equal(t, 30, hs.term(0).h)
}

func TestTabs_SmallWindowHidesTheStrip(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.send(SwitchWorkspaceMsg{Key: "a"}) // the review the harness records
	require.Equal(t, 1, hs.m.top)
	hs.send(tea.WindowSizeMsg{Width: 100, Height: minTabsHeight - 1})
	assert.Zero(t, hs.m.top, "too short: the panes keep every row")
	assert.Equal(t, minTabsHeight-1, hs.lastInnerSize().Height)
	hs.send(tea.WindowSizeMsg{Width: 100, Height: minTabsHeight})
	assert.Equal(t, 1, hs.m.top)
	assert.Equal(t, minTabsHeight-1, hs.lastInnerSize().Height, "never below what a review's toolbar needs")
}

func TestTabs_MouseOnTheStrip(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	zb := hs.zoneOf(t, "b", false)
	before := len(*hs.inner)
	hs.clickAt(zb.from+1, 0)
	assert.Equal(t, "b", hs.m.Workspace(), "a click on a tab brings it")
	hs.clickAt(zb.to-tabCloseW-1, 0)
	assert.Equal(t, "b", hs.m.Workspace(), "a click on the tab on screen keeps it")
	assert.Len(t, *hs.inner, before, "the review under the strip gets no click")

	zc := hs.zoneOf(t, "c", true)
	assert.Equal(t, "×", string([]rune(hs.stripLine())[zc.from]), "the close zone is where × is drawn")
	hs.clickAt(zc.from, 0)
	require.NotNil(t, hs.m.confirm, "× asks first, even with nothing to lose")
	assert.Contains(t, ansi.Strip(hs.m.View()), "Opening it again starts a new tab.")
	hs.sendAll(msgsOf(hs.key("y")))
	assert.Equal(t, []string{"a", "b"}, hs.m.tabKeys(), "and closes once confirmed")
	assert.Nil(t, hs.m.confirm)

	hs.send(tea.MouseMsg{X: 3, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	assert.Equal(t, "b", hs.m.Workspace(), "the wheel does nothing there")
}

func TestTabs_CloseButtonOnEveryTab(t *testing.T) {
	for _, key := range []string{"a", "b", "c"} {
		hs := newSwitcherHarness(t, nil)
		z := hs.zoneOf(t, key, true)
		hs.clickAt(z.from, 0)
		if hs.m.confirm != nil { // a and b have a terminal each
			hs.sendAll(msgsOf(hs.key("y")))
		}
		assert.NotContains(t, hs.m.tabKeys(), key, "tab %s closes from its ×", key)
	}
}

func TestTabs_ClosingTheTabOnScreenGoesToItsNeighbor(t *testing.T) {
	fs := &fakeSessions{}
	hs := newSwitcherHarness(t, fs)
	hs.send(SwitchWorkspaceMsg{Key: "b"})
	hs.send(closeTabMsg{key: "b"})
	assert.Equal(t, "c", hs.m.Workspace(), "the one to its right")
	assert.Equal(t, []string{"a", "c"}, hs.m.tabKeys())
	assert.Equal(t, []string{"b"}, fs.closed, "the program hears of it")
	assert.Contains(t, fs.done, "c", "and of the switch, as from any of its own")
	assert.NotContains(t, hs.m.sessionKeys(), "b", "the switcher lists it no more")

	hs.send(closeTabMsg{key: "c"})
	assert.Equal(t, "a", hs.m.Workspace(), "the last tab hands over to its left")
	assert.Equal(t, []string{"b", "c"}, fs.closed)

	hs.send(closeTabMsg{key: "a"})
	assert.Equal(t, "a", hs.m.Workspace(), "the last tab stays")
	assert.Equal(t, lastTab, hs.m.flash)
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}, Alt: true})
	assert.Equal(t, lastTab, hs.m.flash, "alt+w says so too")
	assert.Equal(t, []string{"b", "c"}, fs.closed)
}

func TestTabs_ClosingPrefersANeighborThatCanCome(t *testing.T) {
	fs := &fakeSessions{notReady: map[string]bool{"c": true}}
	hs := newSwitcherHarness(t, fs)
	hs.send(SwitchWorkspaceMsg{Key: "b"})
	hs.send(closeTabMsg{key: "b"})
	assert.Equal(t, "a", hs.m.Workspace(), "c's connection dropped: a, on the left, comes instead")

	fs = &fakeSessions{notReady: map[string]bool{"a": true, "b": true}}
	hs = newSwitcherHarness(t, fs)
	hs.send(closeTabMsg{key: "c"})
	assert.Equal(t, "b", hs.m.Workspace(), "with none that can come, the nearest anyway: the program rebuilds it")
}

func TestTabs_ClosingAsksWhenItWouldLoseSomething(t *testing.T) {
	fs := &fakeSessions{closeNote: map[string]string{"c": "Its 2 annotations go to history."}}
	hs := newSwitcherHarness(t, fs)
	aTerm := hs.started[0]

	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}, Alt: true}) // c, on screen: its annotations
	require.NotNil(t, hs.m.confirm, "alt+w asks first")
	assert.Contains(t, ansi.Strip(hs.m.View()), "Its 2 annotations go to history.", "in the program's words")
	hs.key("n")
	assert.Nil(t, hs.m.confirm)
	assert.Equal(t, []string{"a", "b", "c"}, hs.m.tabKeys(), "no closes nothing")
	assert.Empty(t, fs.closed)

	hs.clickAt(hs.zoneOf(t, "a", true).from, 0)
	require.NotNil(t, hs.m.confirm, "a parked tab with a terminal asks too")
	assert.Contains(t, ansi.Strip(hs.m.View()), "Close a?")
	assert.Contains(t, ansi.Strip(hs.m.View()), "Closes its terminal and everything running in it.")
	cmd := hs.key("y")
	hs.sendAll(msgsOf(cmd))
	assert.Equal(t, []string{"b", "c"}, hs.m.tabKeys(), "yes closes it")
	assert.Equal(t, []string{"a"}, fs.closed)
	hs.m.closing.Wait()
	assert.True(t, aTerm.isClosed(), "with its terminal")
	assert.Equal(t, "c", hs.m.Workspace(), "the screen stays where it was")
}

func TestTabs_ClosingAskForABusyChat(t *testing.T) {
	fs := &fakeSessions{info: map[string]SessionInfo{"c": {Name: "gamma", Busy: true}}}
	hs := newSwitcherHarness(t, fs)
	hs.send(OpenPaneMsg{Start: func(w, h int) (Terminal, error) { return chatTerm{newFakeTerm(w, h)}, nil }})
	hs.send(closeTabMsg{key: "c"}) // a confirmed close goes through
	assert.NotContains(t, hs.m.tabKeys(), "c")

	hs = newSwitcherHarness(t, fs)
	hs.send(OpenPaneMsg{Start: func(w, h int) (Terminal, error) { return chatTerm{newFakeTerm(w, h)}, nil }})
	hs.m, _ = asModel(hs.m.requestCloseTab("c"))
	require.NotNil(t, hs.m.confirm, "a chat whose agent works asks first")
	assert.Contains(t, ansi.Strip(hs.m.View()), "Its agent is working")
}

// asModel unwraps a Model's update result.
func asModel(model tea.Model, cmd tea.Cmd) (Model, tea.Cmd) {
	return model.(Model), cmd
}

func TestTabs_PaletteCloseTab(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.runPalette("close tab")
	require.NotNil(t, hs.m.confirm, "the palette's Close tab asks first")
	hs.sendAll(msgsOf(hs.key("y")))
	assert.Equal(t, []string{"a", "b"}, hs.m.tabKeys(), "the palette's Close tab closes the tab on screen")
	assert.Equal(t, "b", hs.m.Workspace(), "its neighbor on the left takes the screen")

	one := newWorkspaceHarness(t, "a")
	one.m = one.m.openPalette()
	for _, r := range one.m.palette.rows {
		assert.NotEqual(t, "Close tab", r.label, "a single tab cannot be closed: no row for it")
	}
}

func TestTabs_UnnamedWorkspaceHasNoTab(t *testing.T) {
	hs := newWorkspaceHarness(t, "")
	assert.Empty(t, hs.m.tabKeys())
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}, Alt: true})
	assert.Equal(t, "this review has no tab to close", hs.m.flash)
	b, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b})
	assert.Equal(t, []string{"b"}, hs.m.tabKeys(), "the unnamed one closed when left: b is the only tab")
	assert.Zero(t, hs.m.top)
}

func TestTabs_NotReadyTabOpensAsTheSwitcherDoes(t *testing.T) {
	fs := &fakeSessions{notReady: map[string]bool{"a": true}, info: map[string]SessionInfo{"a": {Name: "a", Note: "disconnected"}}}
	hs := newSwitcherHarness(t, fs)
	assert.Contains(t, hs.stripLine(), "▌1 a · disconnected $ × ", "the tab stays, marked as the switcher marks it")
	cmd := hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	assert.Equal(t, "c", hs.m.Workspace(), "not shown as it was parked")
	assert.Equal(t, []string{"a"}, fs.opened, "Open brings it the program's way")
	hs.sendAll(msgsOf(cmd))
	assert.Equal(t, "a", hs.m.Workspace())
	assert.Equal(t, []string{"a", "b", "c"}, hs.m.tabKeys(), "in its place")
}

func TestTabs_MouseRowsUnderTheStripShiftByOne(t *testing.T) {
	sb := &fakeSidebar{available: true}
	hs := newSwitcherHarnessWith(t, func(cfg *Config) { cfg.Sidebar, cfg.SidebarWidth = sb, 20 })
	hs.send(SwitchWorkspaceMsg{Key: "a"}) // a has a terminal: review | terminal
	require.Equal(t, 1, hs.m.top)
	hs.clickAt(5, 6)
	assert.Equal(t, 5, lastMouse(t, *hs.inner).Y, "the review gets its own rows")
	hs.clickAt(hs.m.leftW+3, 6)
	mice := hs.term(0).mice
	require.NotEmpty(t, mice)
	assert.Equal(t, 5, mice[len(mice)-1].Mouse().Y, "and a pane its own")

	hs.send(ShowSidebarMsg{})
	hs.clickAt(3, 8)
	assert.Equal(t, []int{7}, sb.clicks, "and the sidebar")

	hs.send(ShowSidebarMsg{})
	hs.send(HideSidebarMsg{})
	hs.clickAt(hs.m.leftW-1, 3) // the divider's grip
	require.True(t, hs.m.dragging, "the divider is taken a row under the strip")
	hs.send(tea.MouseMsg{X: 49, Y: 0, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	hs.send(tea.MouseMsg{X: 49, Y: 0, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	assert.False(t, hs.m.dragging, "a release over the strip ends the drag")
	assert.Equal(t, 50, hs.m.leftW, "where it was let go")
	rows := strings.Split(ansi.Strip(hs.m.View()), "\n")
	assert.Equal(t, '↔', []rune(rows[1+hs.m.paneH()/2])[49], "its grip in the middle of the rows under the strip")
}

func TestTabs_SwitcherListsWhatTheTabsDoMostRecentFirst(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	hs.send(closeTabMsg{key: "b"})
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}, Alt: true})
	require.True(t, hs.m.switcher.open)
	assert.Equal(t, []string{"c", "a"}, hs.m.switcher.keys, "a closed tab is gone from the switcher")
}

func TestTabs_OverflowShortensThenScrolls(t *testing.T) {
	specs := func(cur int) []tabSpec {
		var out []tabSpec
		for i := range 12 {
			out = append(out, tabSpec{key: strconv.Itoa(i), name: "project-" + strconv.Itoa(i), machine: "local", current: i == cur})
		}
		return out
	}
	wide := layoutTabs(specs(0)[:3], 200, true, lipgloss.NewStyle())
	assert.Contains(t, ansi.Strip(wide.line), "project-1 · local", "room enough: names whole")

	l := layoutTabs(specs(0)[:4], 90, true, lipgloss.NewStyle())
	assert.Contains(t, ansi.Strip(l.line), "… · local", "too wide: names are shortened first")
	assert.NotContains(t, ansi.Strip(l.line), "project-1 ·")
	assert.Equal(t, 0, l.first)
	assert.Equal(t, 3, l.last, "and then all four fit")

	for cur := range 12 {
		l := layoutTabs(specs(cur), 80, true, lipgloss.NewStyle())
		line := ansi.Strip(l.line)
		require.Equal(t, 80, ansi.StringWidth(line), "cur %d", cur)
		assert.True(t, cur >= l.first && cur <= l.last, "the tab on screen is shown: cur %d in %d..%d", cur, l.first, l.last)
		assert.Equal(t, l.first > 0, strings.HasPrefix(line, tabLeftMark), "‹ where tabs are cut on the left (cur %d)", cur)
		assert.Equal(t, l.last < 11, strings.HasSuffix(line, tabRightMark), "› where they are cut on the right (cur %d)", cur)
		assertZonesMatch(t, line, l)
	}
}

// assertZonesMatch checks each zone against the drawn line: a tab's zone
// starts on the blank before its number, its close zone on its ×.
func assertZonesMatch(t *testing.T, line string, l tabLine) {
	t.Helper()
	cells := []rune(line) // the test names are one cell a rune
	for _, z := range l.zones {
		switch {
		case z.close:
			assert.Equal(t, "×", string(cells[z.from]), "close zone of %s at %d: %q", z.key, z.from, line)
		case z.to-z.from == tabMarkW && (z.from == 0 || z.to == len(cells)):
			assert.Contains(t, []string{tabLeftMark, tabRightMark}, strings.TrimSpace(string(cells[z.from:z.to])))
		default:
			assert.Equal(t, " ", string(cells[z.from]), "tab %s starts with its blank", z.key)
			assert.Equal(t, "×", string(cells[z.to-tabCloseW]), "and ends with its ×")
		}
	}
}

func TestTabs_ScrolledStripHitTests(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e", "f"}
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Workspace = "a"
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 40, Height: 20})
	for _, k := range keys[1:] {
		m, _ := otherInner()
		hs.send(SwitchWorkspaceMsg{Key: k, Model: m})
	}
	strip := hs.m.tabStrip()
	require.Positive(t, strip.first, "six tabs do not fit in 40 columns: scrolled to f")
	assert.Equal(t, 5, strip.last)
	assert.True(t, strings.HasPrefix(hs.stripLine(), tabLeftMark))
	hs.clickAt(0, 0)
	assert.Equal(t, keys[strip.first-1], hs.m.Workspace(), "‹ brings the tab hidden beyond it")
	cur := hs.m.Workspace()
	z := hs.zoneOf(t, cur, true)
	assert.Equal(t, "×", string([]rune(hs.stripLine())[z.from]))
	hs.clickAt(z.from, 0)
	hs.sendAll(msgsOf(hs.key("y")))
	assert.NotContains(t, hs.m.tabKeys(), cur, "× hit-tests right after a scroll")
}

func TestTabs_WideCharacters(t *testing.T) {
	fs := &fakeSessions{info: map[string]SessionInfo{
		"a": {Name: "日本語のプロジェクト", Machine: "local"},
		"b": {Name: "emoji 🚀 app", Machine: "devbox T3"},
		"c": {Name: "plain", Machine: "local"},
	}}
	for _, width := range []int{120, 50, 31} {
		hs := newSwitcherHarness(t, fs)
		hs.send(tea.WindowSizeMsg{Width: width, Height: 30})
		line := strings.Split(hs.m.View(), "\n")[0]
		assert.Equal(t, width, ansi.StringWidth(line), "the strip fills the row exactly at %d", width)
		for _, z := range hs.m.tabStrip().zones {
			if z.close {
				assert.Equal(t, "×", ansi.Strip(ansi.Cut(line, z.from, z.from+1)), "× at its zone at %d", width)
			}
		}
	}
}

// sgrAt is the styling in effect at cell col of line: the SGR sequences
// since the last reset.
func sgrAt(line string, col int) string {
	active, x := "", 0
	var state byte
	for line != "" {
		seq, w, n, next := ansi.DecodeSequence(line, state, nil)
		state, line = next, line[n:]
		switch {
		case strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m"):
			if seq == ansi.ResetStyle || seq == "\x1b[m" {
				active = ""
			} else {
				active += seq
			}
		case w > 0:
			if col >= x && col < x+w {
				return active
			}
			x += w
		}
	}
	return ""
}

// fgOf is the truecolor foreground parameters of hex.
func fgOf(hex string) string {
	r, g, b := rgbOf(hex)
	return "38;2;" + strconv.Itoa(r) + ";" + strconv.Itoa(g) + ";" + strconv.Itoa(b)
}

// bgOf is the truecolor background parameters of hex.
func bgOf(hex string) string {
	r, g, b := rgbOf(hex)
	return "48;2;" + strconv.Itoa(r) + ";" + strconv.Itoa(g) + ";" + strconv.Itoa(b)
}

// rgbOf reads #rrggbb.
func rgbOf(hex string) (r, g, b int) {
	v, _ := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)
}

func TestTabs_MachineColors(t *testing.T) {
	truecolor(t)
	fs := &fakeSessions{info: map[string]SessionInfo{
		"a": {Name: "alpha", Machine: "local"},
		"b": {Name: "beta", Machine: "devbox T3"},
		"c": {Name: "gamma", Machine: "local"},
	}}
	hs := newSwitcherHarness(t, fs)
	local, devbox := machineColorOf("local"), machineColorOf("devbox T3")
	require.NotEqual(t, local, devbox, "the test's two machines land on two colors")
	assert.Equal(t, local, machineColorOf("local"), "the same machine, the same color, from a hash of its name")
	bar := func(key string) string { // the styling of the tab's first cell
		return sgrAt(hs.m.tabStrip().line, hs.zoneOf(t, key, false).from)
	}
	style := func(key string) string { // the styling of the tab's name, and of its ×
		line, z, x := hs.m.tabStrip().line, hs.zoneOf(t, key, false), hs.zoneOf(t, key, true)
		require.Equal(t, sgrAt(line, z.from+3), sgrAt(line, x.from), "%s: the × styled as its tab", key)
		return sgrAt(line, z.from+3)
	}
	assert.Contains(t, ansi.Strip(hs.m.tabStrip().line), "▌1 alpha", "every tab starts with a bar")
	assert.Contains(t, bar("a"), fgOf(local.bg), "a local tab's bar in local's color")
	assert.Contains(t, bar("b"), fgOf(devbox.bg), "devbox's in its own")
	assert.Contains(t, bar("c"), fgOf(local.bg))
	assert.Contains(t, style("a"), fgOf(tabIdleFg), "a tab not on screen is quiet")
	assert.NotContains(t, style("a"), "48;", "on the row's background")
	assert.Contains(t, style("b"), fgOf(tabIdleFg))
	assert.Contains(t, style("c"), bgOf(local.bg), "the tab on screen filled with its machine's color")
	assert.Contains(t, style("c"), "[1;", "bold")
	assert.Contains(t, hs.m.tabStrip().line, "$ ×", "marks and × inside the tab's own sequence")

	hs.send(SwitchWorkspaceMsg{Key: "a"})
	hs.send(closeTabMsg{key: "b"})
	assert.Contains(t, style("c"), fgOf(tabIdleFg), "the emphasis follows the tab on screen")
	assert.Contains(t, style("a"), bgOf(local.bg))
	assert.Contains(t, bar("c"), fgOf(local.bg), "the bars follow the machine as tabs move and close")
	line, za, zc := hs.m.tabStrip().line, hs.zoneOf(t, "a", false), hs.zoneOf(t, "c", false)
	assert.Equal(t, 100, ansi.StringWidth(line), "the escapes take no cells")
	assert.Empty(t, sgrAt(line, za.to), "the gap is plain")
	assert.Empty(t, sgrAt(line, zc.to+1), "and so is the rest of the row")

	hs.send(SwitchWorkspaceMsg{Key: "c"})
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}, Alt: true})
	require.True(t, hs.m.switcher.open)
	assert.Contains(t, hs.m.View(), machineBadge("local", "local"), "the switcher shows a machine on its tabs' color")
}

func TestTabs_NoColors(t *testing.T) {
	truecolor(t)
	hs := newSwitcherHarnessWith(t, func(cfg *Config) { cfg.NoColors = true })
	line := hs.m.tabStrip().line
	assert.NotContains(t, line, "48;", "no backgrounds")
	assert.NotContains(t, line, "38;", "nor colored text")
	assert.Contains(t, line, "\x1b[1;7m", "the tab on screen reversed and bold")
	assert.Contains(t, ansi.Strip(line), "│", "tabs apart by a line")
}

// TestMachinePaletteContrast keeps every tab's text readable on its
// background: WCAG contrast of at least 4.3:1.
func TestMachinePaletteContrast(t *testing.T) {
	lum := func(hex string) float64 {
		ch := func(v int) float64 {
			c := float64(v) / 255
			if c <= 0.03928 {
				return c / 12.92
			}
			return math.Pow((c+0.055)/1.055, 2.4)
		}
		r, g, b := rgbOf(hex)
		return 0.2126*ch(r) + 0.7152*ch(g) + 0.0722*ch(b)
	}
	contrast := func(a, b string) float64 {
		la, lb := lum(a), lum(b)
		return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
	}
	// the Linux console keeps the top bit of each channel of a background
	consoleBits := func(hex string) int {
		r, g, b := rgbOf(hex)
		return r>>7<<2 | g>>7<<1 | b>>7
	}
	cube := map[int]bool{0x00: true, 0x5f: true, 0x87: true, 0xaf: true, 0xd7: true, 0xff: true}
	for _, c := range machinePalette {
		assert.GreaterOrEqual(t, contrast(c.fg, c.bg), 4.3, "%s on %s", c.fg, c.bg)
		assert.NotZero(t, consoleBits(c.bg), "%s is not the console's black", c.bg)
		assert.NotEqual(t, 7, consoleBits(c.bg), "%s is not the console's grey", c.bg)
		r, g, b := rgbOf(c.bg)
		assert.True(t, cube[r] && cube[g] && cube[b], "%s is an entry of the 256-color cube", c.bg)
	}
}

func TestTabs_StripFetchesNothing(t *testing.T) {
	fs := &fakeSessions{info: map[string]SessionInfo{"a": {Name: "alpha", Busy: true}}}
	hs := newSwitcherHarness(t, fs)
	before := fs.describes
	for range 5 {
		_ = hs.m.View()
	}
	assert.Equal(t, before, fs.describes, "drawn on every frame, the strip asks Peek, never Describe")
	assert.Contains(t, hs.stripLine(), "▌1 alpha ● $ × ", "with the marks Peek has at hand")
}

func TestTabs_StripIsCached(t *testing.T) {
	hs := newSwitcherHarness(t, nil)
	first := hs.m.tabStrip()
	hs.m.strip.strip.line = "cached"
	assert.Equal(t, "cached", hs.m.tabStrip().line, "the same tabs, width and marks: the line is reused")
	hs.send(SwitchWorkspaceMsg{Key: "a"})
	assert.NotEqual(t, "cached", hs.m.tabStrip().line, "another tab on screen: laid out again")
	assert.NotEqual(t, first.line, hs.m.tabStrip().line)
}

func TestTabs_AltWClosesTheTabOnScreenEitherCase(t *testing.T) {
	for _, r := range []rune{'w', 'W'} {
		hs := newSwitcherHarness(t, nil)
		require.Equal(t, []string{"a", "b", "c"}, hs.m.tabKeys())
		require.Equal(t, "c", hs.m.Workspace())
		hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true})
		require.NotNil(t, hs.m.confirm, "alt+%c asks first", r)
		assert.Contains(t, ansi.Strip(hs.m.View()), "Close ")
		hs.key("n")
		assert.Equal(t, []string{"a", "b", "c"}, hs.m.tabKeys(), "no keeps it")
		hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true})
		hs.sendAll(msgsOf(hs.key("y")))
		assert.Equal(t, []string{"a", "b"}, hs.m.tabKeys(), "alt+%c closes the tab on screen once confirmed", r)
		assert.Equal(t, "b", hs.m.Workspace())
	}
	assert.Equal(t, "alt+W", shifted("alt+w"))
	assert.Equal(t, "ctrl+q", shifted("ctrl+q"))
	assert.Equal(t, "alt+left", shifted("alt+left"), "a named key has no upper case")
	assert.Equal(t, "alt+1", shifted("alt+1"))
}
