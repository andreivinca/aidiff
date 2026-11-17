package splitview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSidebar records what the split asks of it.
type fakeSidebar struct {
	available bool
	shown     int
	keys      []string
	clicks    []int
	selects   []int
	hideOn    string // the key that asks to hide it
}

func (f *fakeSidebar) Available() bool {
	return f.available
}

func (f *fakeSidebar) Shown() {
	f.shown++
}

func (f *fakeSidebar) View(width, height int, focused bool) string {
	line := strings.Repeat("s", width)
	if focused {
		line = "F" + line[1:]
	}
	return strings.TrimSuffix(strings.Repeat(line+"\n", height), "\n")
}

func (f *fakeSidebar) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	f.keys = append(f.keys, msg.String())
	return nil, msg.String() == f.hideOn
}

func (f *fakeSidebar) Click(y int) (tea.Cmd, bool) {
	f.clicks = append(f.clicks, y)
	return nil, true
}

func (f *fakeSidebar) Select(n int) (tea.Cmd, bool) {
	f.selects = append(f.selects, n)
	return nil, true
}

// newSidebarHarness is a split with sb as its 20-column sidebar, no
// terminal open, in a 100x30 window.
func newSidebarHarness(sb *fakeSidebar) *harness {
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Sidebar = sb
		cfg.SidebarWidth = 20
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return hs
}

func TestSidebar_ToggleShrinksTheReview(t *testing.T) {
	sb := &fakeSidebar{available: true, hideOn: "esc"}
	hs := newSidebarHarness(sb)
	assert.Equal(t, 100, hs.lastInnerSize().Width)

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlB})
	assert.Equal(t, 1, sb.shown)
	assert.Equal(t, focusSidebar, hs.m.focus, "a shown sidebar takes the keys")
	assert.Equal(t, 79, hs.lastInnerSize().Width, "the review gives up the sidebar and its separator")
	lines := strings.Split(hs.m.View(), "\n")
	assert.True(t, strings.HasPrefix(lines[0], "F"+strings.Repeat("s", 19)+"│inner"), "sidebar, separator, review: %q", lines[0])

	hs.key("j")
	assert.Equal(t, []string{"j"}, sb.keys, "keys go to the focused sidebar")
	assert.NotContains(t, *hs.inner, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}, "and not to the review")

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlB})
	assert.Equal(t, 100, hs.lastInnerSize().Width, "ctrl+b again hides it")
	assert.Equal(t, focusDiff, hs.m.focus)

	hs.send(ShowSidebarMsg{})
	hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, hs.m.sideOpen, "the sidebar can ask to hide itself")
}

func TestSidebar_UnavailableLeavesTheKey(t *testing.T) {
	sb := &fakeSidebar{}
	hs := newSidebarHarness(sb)
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlB})
	hs.send(ShowSidebarMsg{})
	assert.False(t, hs.m.sideOpen)
	assert.Contains(t, *hs.inner, tea.KeyMsg{Type: tea.KeyCtrlB}, "without a connection ctrl+b is the focused pane's")
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	assert.Empty(t, sb.selects)
}

func TestSidebar_NumberKeysSelect(t *testing.T) {
	sb := &fakeSidebar{available: true}
	hs := newSidebarHarness(sb)
	hs.send(unknownCSIMsg(t, "\x1b[27;5;50~")) // ctrl+2, modifyOtherKeys form
	hs.send(unknownCSIMsg(t, "\x1b[51;5u"))    // ctrl+3, CSI u form
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}, Alt: true})
	assert.Equal(t, []int{2, 3, 4}, sb.selects, "ctrl or alt with a number selects that row with the sidebar hidden")
	hs.send(unknownCSIMsg(t, "\x1b[27;5;48~")) // ctrl+0
	assert.Equal(t, []int{2, 3, 4}, sb.selects, "rows are numbered from 1")
}

func TestSidebar_Mouse(t *testing.T) {
	sb := &fakeSidebar{available: true}
	hs := newSidebarHarness(sb)
	hs.send(ShowSidebarMsg{})
	hs.send(tea.MouseMsg{X: 30, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	assert.Equal(t, 9, lastMouse(t, *hs.inner).X, "the review gets its own columns")
	assert.False(t, hs.m.sideOpen, "focus left the sidebar for the review: it closes")
	assert.Equal(t, 100, hs.lastInnerSize().Width, "and the review gets its columns back")

	hs.send(ShowSidebarMsg{})
	hs.send(tea.MouseMsg{X: 5, Y: 7, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	assert.Equal(t, []int{7}, sb.clicks)
	assert.False(t, hs.m.sideOpen, "a click that picks a row hides the sidebar")
}

func TestSidebar_FocusLeftOfTheReviewShowsIt(t *testing.T) {
	sb := &fakeSidebar{available: true}
	hs := newSidebarHarness(sb)
	altLeft := tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
	hs.send(altLeft)
	assert.True(t, hs.m.sideOpen, "alt+← on the leftmost panel shows the hidden sidebar")
	assert.Equal(t, focusSidebar, hs.m.focus, "and focuses it")
	hs.send(altLeft)
	assert.True(t, hs.m.sideOpen, "pressed again it stays")
	assert.Equal(t, focusSidebar, hs.m.focus)

	off := newSidebarHarness(&fakeSidebar{})
	off.send(altLeft)
	assert.False(t, off.m.sideOpen, "without a T3 connection nothing shows")
	assert.Equal(t, focusDiff, off.m.focus)
}

// newOuterHarness is a split with a 20-column sidebar and outer sidebar,
// no terminal open, in a 120x30 window.
func newOuterHarness(sb, outer *fakeSidebar) *harness {
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Sidebar = sb
		cfg.OuterSidebar = outer
		cfg.SidebarWidth = 20
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	return hs
}

var (
	altLeft  = tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
	altRight = tea.KeyMsg{Type: tea.KeyRight, Alt: true}
)

func TestOuter_ReachedPastTheSidebar(t *testing.T) {
	sb, outer := &fakeSidebar{available: true}, &fakeSidebar{available: true, hideOn: "enter"}
	hs := newOuterHarness(sb, outer)

	hs.send(altLeft)
	assert.Equal(t, focusSidebar, hs.m.focus, "the first alt+← shows the sidebar")
	assert.False(t, hs.m.outerOpen)
	hs.send(altLeft)
	assert.Equal(t, focusOuter, hs.m.focus, "the next one the outer sidebar")
	assert.Equal(t, 1, outer.shown)
	assert.Equal(t, 120-21-21, hs.lastInnerSize().Width, "both sidebars take their columns")
	lines := strings.Split(hs.m.View(), "\n")
	assert.True(t, strings.HasPrefix(lines[0], "F"+strings.Repeat("s", 19)+"│"+strings.Repeat("s", 20)+"│inner"),
		"outer sidebar, then sidebar, then the review: %q", lines[0])
	hs.send(altLeft)
	assert.Equal(t, focusOuter, hs.m.focus, "it is the leftmost")

	hs.key("j")
	assert.Equal(t, []string{"j"}, outer.keys, "keys go to it")
	hs.send(altRight)
	assert.False(t, hs.m.outerOpen, "leaving it hides it")
	assert.Equal(t, focusSidebar, hs.m.focus)
	assert.Equal(t, 120-21, hs.lastInnerSize().Width)

	hs.send(altLeft)
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, hs.m.outerOpen, "it can ask to hide itself")
	assert.Equal(t, focusSidebar, hs.m.focus, "focus goes back where it came from")

	hs.send(altLeft)
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlB})
	assert.False(t, hs.m.outerOpen, "hiding the sidebar hides it too")
	assert.Equal(t, focusDiff, hs.m.focus)
}

func TestOuter_WithoutTheSidebar(t *testing.T) {
	outer := &fakeSidebar{available: true}
	hs := newOuterHarness(&fakeSidebar{}, outer)
	hs.send(altLeft)
	assert.Equal(t, focusOuter, hs.m.focus, "with no sidebar to show, alt+← goes straight to the outer one")
	hs.send(altRight)
	assert.Equal(t, focusDiff, hs.m.focus)
	assert.False(t, hs.m.outerOpen)
}

func TestOuter_ClickElsewhereHidesIt(t *testing.T) {
	outer := &fakeSidebar{available: true}
	hs := newOuterHarness(&fakeSidebar{available: true}, outer)
	hs.send(altLeft)
	hs.send(altLeft)
	hs.send(tea.MouseMsg{X: 3, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	assert.Equal(t, []int{5}, outer.clicks, "a click on it acts on the row")
	assert.False(t, hs.m.outerOpen, "the fake hides on a click")

	hs.send(altLeft)
	require.True(t, hs.m.outerOpen)
	hs.send(tea.MouseMsg{X: 80, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	assert.False(t, hs.m.outerOpen, "a click on the review moves focus there and hides it")
	assert.False(t, hs.m.sideOpen, "and the sidebar beside it")
	assert.Equal(t, focusDiff, hs.m.focus)
	assert.Equal(t, 80-42, lastMouse(t, *hs.inner).X, "the click landed where it was drawn")
}

// lastMouse is the last mouse event the wrapped model received.
func lastMouse(t *testing.T, got []tea.Msg) tea.MouseMsg {
	t.Helper()
	for i := len(got) - 1; i >= 0; i-- {
		if m, ok := got[i].(tea.MouseMsg); ok {
			return m
		}
	}
	require.Fail(t, "no mouse event reached the wrapped model")
	return tea.MouseMsg{}
}

func TestSidebar_ClosesWhenFocusLeaves(t *testing.T) {
	sb, outer := &fakeSidebar{available: true}, &fakeSidebar{available: true}
	hs := newOuterHarness(sb, outer)

	hs.send(altLeft)
	require.True(t, hs.m.sideOpen)
	hs.send(altRight)
	assert.False(t, hs.m.sideOpen, "alt+→ out of the sidebar closes it")
	assert.Equal(t, focusDiff, hs.m.focus)
	assert.Equal(t, 120, hs.lastInnerSize().Width, "the review gets its columns back")

	hs.send(altLeft)
	hs.send(altLeft)
	require.True(t, hs.m.outerOpen)
	assert.True(t, hs.m.sideOpen, "moving on into the outer sidebar keeps the sidebar")
	hs.send(altRight)
	assert.False(t, hs.m.outerOpen, "leaving the outer sidebar closes it")
	assert.True(t, hs.m.sideOpen, "and comes back to the sidebar")
	hs.send(altRight)
	assert.False(t, hs.m.sideOpen, "then out of the sidebar closes it too")

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlB})
	require.True(t, hs.m.sideOpen, "ctrl+b shows it, focused")
	hs.send(OpenPaneMsg{Start: hs.config().Start})
	assert.False(t, hs.m.sideOpen, "a pane that opens takes focus, and the sidebar closes")
}
