package splitview

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTerm records what the split sends to the terminal.
type fakeTerm struct {
	mu        sync.Mutex
	text      []string
	keys      []uv.KeyPressEvent
	pastes    []string
	mice      []vt.Mouse
	w, h      int
	exited    bool
	closed    bool
	updates   chan struct{}
	closeOnce sync.Once
}

func newFakeTerm(w, h int) *fakeTerm {
	return &fakeTerm{w: w, h: h, updates: make(chan struct{}, 1)}
}

func (f *fakeTerm) Updates() <-chan struct{} {
	return f.updates
}

func (f *fakeTerm) Exited() bool {
	return f.exited
}

func (f *fakeTerm) SendText(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.text = append(f.text, s)
}

func (f *fakeTerm) SendKey(k uv.KeyPressEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, k)
}

func (f *fakeTerm) Paste(s string) {
	f.pastes = append(f.pastes, s)
}

func (f *fakeTerm) SendMouse(m vt.Mouse) {
	f.mice = append(f.mice, m)
}

func (f *fakeTerm) Resize(w, h int) {
	f.w, f.h = w, h
}

// Close marks the fake closed and, as the Terminal contract asks, closes its
// updates channel.
func (f *fakeTerm) Close() {
	f.closeOnce.Do(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.closed = true
		close(f.updates)
	})
}

func (f *fakeTerm) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeTerm) Render(showCursor bool) string {
	line := strings.Repeat("t", f.w)
	if showCursor {
		line = "C" + line[1:]
	}
	return strings.TrimSuffix(strings.Repeat(line+"\n", f.h), "\n")
}

// innerModel is a stand-in for the diff view: it records what it receives.
type innerModel struct {
	got     *[]tea.Msg
	quitKey string
}

func (i innerModel) Init() tea.Cmd {
	return nil
}

func (i innerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	*i.got = append(*i.got, msg)
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == i.quitKey {
		return i, tea.Quit
	}
	return i, nil
}

func (i innerModel) View() string {
	return "inner"
}

type harness struct {
	m       Model
	started []*fakeTerm
	inner   *[]tea.Msg
}

// config is a split whose Start opens fake terminals the harness records.
func (hs *harness) config() Config {
	return Config{
		WidthPercent: 40,
		Start: func(cw, ch int) (Terminal, error) {
			ft := newFakeTerm(cw, ch)
			hs.started = append(hs.started, ft)
			return ft, nil
		},
		Quit: QuitText{Title: "Quit aidiff?", Idle: "Leaves the review."},
	}
}

// newSplit wraps a recording inner model, quitting on "q", in a split with
// cfg (see harness.config), without sending a window size.
func newSplit(cfg func(hs *harness) Config) *harness {
	got := []tea.Msg{}
	hs := &harness{inner: &got}
	hs.m = New(innerModel{got: &got, quitKey: "q"}, cfg(hs))
	return hs
}

func newHarnessOpts(t *testing.T, w, h int, openOnStart bool) *harness {
	t.Helper()
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.OpenOnStart = openOnStart
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: w, Height: h})
	return hs
}

// newHarness starts with one terminal open, like --terminal.
func newHarness(t *testing.T, w, h int) *harness {
	t.Helper()
	hs := newHarnessOpts(t, w, h, true)
	require.Len(t, hs.m.terms, 1, "first window size opens the terminal")
	return hs
}

func (hs *harness) send(msg tea.Msg) tea.Cmd {
	model, cmd := hs.m.Update(msg)
	hs.m = model.(Model)
	return cmd
}

func (hs *harness) key(s string) tea.Cmd {
	return hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func (hs *harness) term(i int) *fakeTerm {
	return hs.m.terms[i].(*fakeTerm)
}

func (hs *harness) lastInnerSize() tea.WindowSizeMsg {
	var size tea.WindowSizeMsg
	for _, msg := range *hs.inner {
		if s, ok := msg.(tea.WindowSizeMsg); ok {
			size = s
		}
	}
	return size
}

// runPalette opens ctrl+p, types query and presses enter.
func (hs *harness) runPalette(query string) {
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	for _, r := range query {
		hs.key(string(r))
	}
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
}

func TestSplit_LayoutSizes(t *testing.T) {
	hs := newHarness(t, 100, 30)

	assert.Equal(t, 60, hs.m.leftW)
	assert.Equal(t, tea.WindowSizeMsg{Width: 60, Height: 30}, hs.lastInnerSize(), "inner model sees only the left side")
	assert.Equal(t, 40, hs.term(0).w, "terminal gets the whole right side, no border")
	assert.Equal(t, 30, hs.term(0).h)

	hs.send(tea.WindowSizeMsg{Width: 200, Height: 50})
	assert.Equal(t, 80, hs.term(0).w, "resize reaches the running terminal")
	assert.Len(t, hs.started, 1, "resize does not start another terminal")

	lines := strings.Split(hs.m.View(), "\n")
	require.Len(t, lines, 50)
	for i, l := range lines {
		assert.Equal(t, 200, ansi.StringWidth(l), "row %d spans the full window", i)
	}
}

func TestSplit_StartsWithoutTerminal(t *testing.T) {
	hs := newHarnessOpts(t, 100, 30, false)
	assert.Empty(t, hs.m.terms)
	assert.Equal(t, 100, hs.m.leftW, "diff gets the full width")
	assert.Equal(t, tea.WindowSizeMsg{Width: 100, Height: 30}, hs.lastInnerSize())

	hs.runPalette("open")
	require.Len(t, hs.m.terms, 1, "Open Terminal opens one")
	assert.Equal(t, 60, hs.m.leftW)
	assert.Equal(t, 0, hs.m.focus, "the new terminal gets focus")
}

func TestSplit_ExitClosesPane(t *testing.T) {
	hs := newHarness(t, 100, 30)
	first := hs.term(0)

	first.exited = true
	assert.Nil(t, hs.send(termUpdateMsg{term: first}))
	assert.Empty(t, hs.m.terms, "the exited terminal's pane is removed")
	assert.Equal(t, focusDiff, hs.m.focus)
	assert.Equal(t, 100, hs.m.leftW)
	assert.Equal(t, tea.WindowSizeMsg{Width: 100, Height: 30}, hs.lastInnerSize(), "diff takes the full width again")
	assert.Eventually(t, first.isClosed, testTimeout, testTick)

	lines := strings.Split(hs.m.View(), "\n")
	require.Len(t, lines, 30)
	assert.Equal(t, 100, ansi.StringWidth(lines[0]))
	assert.NotContains(t, hs.m.View(), "t", "no terminal drawn")

	assert.Nil(t, hs.send(termUpdateMsg{term: first}), "late updates from a closed terminal are ignored")
}

func TestSplit_OpenTwiceSideBySide(t *testing.T) {
	hs := newHarness(t, 100, 30)
	hs.runPalette("open terminal")
	require.Len(t, hs.m.terms, 2, "a second Open Terminal adds another")
	assert.Equal(t, 1, hs.m.focus)
	assert.Equal(t, 60, hs.m.leftW, "the diff keeps its width")

	// 40 terminal columns: 1 separator + 39 split as 20 | 19, both full height
	assert.Equal(t, 20, hs.term(0).w)
	assert.Equal(t, 19, hs.term(1).w)
	assert.Equal(t, 30, hs.term(0).h)
	assert.Equal(t, 30, hs.term(1).h)

	for i, l := range strings.Split(ansi.Strip(hs.m.View()), "\n") {
		require.Len(t, []rune(l), 100, "row %d", i)
		separator := "│"
		if i == hs.m.height/2 {
			separator = "↔"
		}
		assert.Equal(t, separator, string([]rune(l)[80]), "row %d has the separator after the first terminal", i)
	}

	// typing goes to the focused (rightmost) one
	hs.key("x")
	assert.Equal(t, []string{"x"}, hs.term(1).text)
	assert.Empty(t, hs.term(0).text)

	// exiting the left one leaves the right one with the whole area and focus
	left, right := hs.term(0), hs.term(1)
	left.exited = true
	hs.send(termUpdateMsg{term: left})
	require.Len(t, hs.m.terms, 1)
	assert.Same(t, right, hs.term(0))
	assert.Equal(t, 0, hs.m.focus, "focus follows the remaining terminal")
	assert.Equal(t, 40, right.w)
}

func TestSplit_FocusCycles(t *testing.T) {
	hs := newHarness(t, 100, 30)
	hs.runPalette("open")
	require.Len(t, hs.m.terms, 2)

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	assert.Equal(t, focusDiff, hs.m.focus, "from the last terminal back to the diff")
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	assert.Equal(t, 0, hs.m.focus)
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	assert.Equal(t, 1, hs.m.focus)
}

func TestSplit_FocusRoutesKeys(t *testing.T) {
	hs := newHarness(t, 100, 30)
	require.Equal(t, 0, hs.m.focus, "an opened terminal starts focused")

	before := len(*hs.inner)
	hs.key("j")
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlC})
	assert.Equal(t, []string{"j", "\x03"}, hs.term(0).text)
	assert.Len(t, *hs.inner, before, "keys meant for the terminal never reach the diff")

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	assert.Equal(t, focusDiff, hs.m.focus)
	hs.key("j")
	assert.Len(t, hs.term(0).text, 2, "diff has focus now")
}

func TestSplit_Palette(t *testing.T) {
	hs := newHarness(t, 100, 30)

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	require.True(t, hs.m.palette.open)
	view := ansi.Strip(hs.m.View())
	assert.Contains(t, view, "Open Terminal")
	assert.Contains(t, view, "Close Terminal")
	assert.Contains(t, view, "Quit")

	hs.key("q")
	assert.Empty(t, hs.term(0).text, "typing goes to the palette, not the terminal")
	assert.Equal(t, []string{"Quit"}, names(hs.m.palette.rows))

	hs.send(tea.KeyMsg{Type: tea.KeyBackspace})
	hs.key("zzz")
	assert.Contains(t, ansi.Strip(hs.m.View()), "no matching commands")
	assert.Nil(t, hs.send(tea.KeyMsg{Type: tea.KeyEnter}), "enter with no match does nothing")

	hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, hs.m.palette.open, "esc closes")

	// arrows wrap around and enter runs the selection
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	hs.send(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 2, hs.m.palette.sel, "up from the top wraps to the bottom")
	hs.send(tea.KeyMsg{Type: tea.KeyDown})
	hs.send(tea.KeyMsg{Type: tea.KeyDown})
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Empty(t, hs.m.terms, "second entry, Close Terminal, closed it")
	assert.False(t, hs.m.palette.open)

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	assert.Equal(t, []string{"Open Terminal", "Quit"}, names(hs.m.palette.rows),
		"Close Terminal only appears when there is one")
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	assert.False(t, hs.m.palette.open, "ctrl+p again closes")

	lines := strings.Split(hs.m.View(), "\n")
	for i, l := range lines {
		assert.Equal(t, 100, ansi.StringWidth(l), "row %d keeps full width", i)
	}
}

func names(cmds []row) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.label
	}
	return out
}

func TestFuzzyMatch(t *testing.T) {
	assert.True(t, fuzzyMatch("", "Open Terminal"))
	assert.True(t, fuzzyMatch("opt", "Open Terminal"))
	assert.True(t, fuzzyMatch("OPEN term", "Open Terminal"))
	assert.False(t, fuzzyMatch("tpo", "Open Terminal"), "order matters")
	assert.False(t, fuzzyMatch("x", "Open Terminal"))
}

func TestSplit_NoRoomForAnotherTerminal(t *testing.T) {
	hs := newHarness(t, 60, 20) // 24 terminal columns
	hs.runPalette("open")
	require.Len(t, hs.m.terms, 2, "12 | 11 still fits")
	hs.runPalette("open")
	require.Len(t, hs.m.terms, 2, "a third would be narrower than 10 columns")
	assert.Equal(t, 12, hs.term(0).w, "the refused open leaves the layout alone")
	assert.Contains(t, hs.m.View(), "No room for another terminal")
	hs.key("x")
	assert.NotContains(t, hs.m.View(), "No room", "the message clears on the next key")
}

func TestSplit_KeyTranslation(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyMsg
		text string
		key  *uv.KeyPressEvent
	}{
		{"rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ä")}, "ä", nil},
		{"alt rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true}, "\x1bb", nil},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, "\r", nil},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}, "\t", nil},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}, "\x1b", nil},
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f", nil},
		{"ctrl+a", tea.KeyMsg{Type: tea.KeyCtrlA}, "\x01", nil},
		{"space", tea.KeyMsg{Type: tea.KeySpace}, " ", nil},
		{"shift+tab", tea.KeyMsg{Type: tea.KeyShiftTab}, "\x1b[Z", nil},
		{"ctrl+left", tea.KeyMsg{Type: tea.KeyCtrlLeft}, "\x1b[1;5D", nil},
		{"shift+up", tea.KeyMsg{Type: tea.KeyShiftUp}, "\x1b[1;2A", nil},
		{"alt+ctrl+right", tea.KeyMsg{Type: tea.KeyCtrlRight, Alt: true}, "\x1b[1;7C", nil},
		{"up uses emulator encoder", tea.KeyMsg{Type: tea.KeyUp}, "", &uv.KeyPressEvent{Code: uv.KeyUp}},
		{"f5", tea.KeyMsg{Type: tea.KeyF5}, "", &uv.KeyPressEvent{Code: uv.KeyF5}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := newFakeTerm(10, 2)
			sendKey(ft, tc.msg)
			if tc.key != nil {
				assert.Equal(t, []uv.KeyPressEvent{*tc.key}, ft.keys)
				assert.Empty(t, ft.text)
				return
			}
			assert.Equal(t, []string{tc.text}, ft.text)
		})
	}

	ft := newFakeTerm(10, 2)
	sendKey(ft, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("multi\nline"), Paste: true})
	assert.Equal(t, []string{"multi\nline"}, ft.pastes, "pastes keep bracketed-paste handling")
}

func TestSplit_MouseFocusAndRouting(t *testing.T) {
	hs := newHarness(t, 100, 30)
	hs.runPalette("open")
	require.Len(t, hs.m.terms, 2) // columns 60-79 | separator 80 | 81-99

	hs.send(tea.MouseMsg{X: 10, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	assert.Equal(t, focusDiff, hs.m.focus, "click on the left focuses the diff")

	hs.send(tea.MouseMsg{X: 90, Y: 7, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	assert.Equal(t, 1, hs.m.focus, "click in the right terminal focuses it")
	require.Len(t, hs.term(1).mice, 1)
	assert.Equal(t, uv.Mouse{X: 9, Y: 7, Button: uv.MouseLeft}, hs.term(1).mice[0].Mouse(),
		"coordinates are relative to that terminal (it starts at column 81)")
	assert.Empty(t, hs.term(0).mice)

	hs.send(tea.MouseMsg{X: 80, Y: 7, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	assert.Equal(t, 1, hs.m.focus, "pressing the separator does not change focus")
	assert.True(t, hs.m.dragging, "it grabs the separator instead")
	hs.send(tea.MouseMsg{X: 80, Y: 7, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	assert.False(t, hs.m.dragging)

	hs.send(tea.MouseMsg{X: 65, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	assert.Equal(t, 0, hs.m.focus)
	assert.Equal(t, uv.Mouse{X: 5, Y: 2, Button: uv.MouseLeft}, hs.term(0).mice[0].Mouse())
}

func TestSplit_DragDividerResizes(t *testing.T) {
	hs := newHarness(t, 100, 30)
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket}) // focus the diff
	require.Equal(t, 60, hs.m.leftW)
	innerBefore := len(*hs.inner)

	hs.send(tea.MouseMsg{X: 59, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	assert.True(t, hs.m.dragging)
	assert.Equal(t, focusDiff, hs.m.focus, "grabbing the divider does not change focus")

	hs.send(tea.MouseMsg{X: 39, Y: 3, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	assert.Equal(t, 40, hs.m.leftW, "border follows the mouse")
	assert.Equal(t, 60, hs.term(0).w, "terminal grows")
	assert.Equal(t, tea.WindowSizeMsg{Width: 40, Height: 30}, hs.lastInnerSize())

	hs.send(tea.MouseMsg{X: 2, Y: 3, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	assert.Equal(t, minPaneWidth, hs.m.leftW)
	hs.send(tea.MouseMsg{X: 99, Y: 3, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	assert.Equal(t, 100-minPaneWidth, hs.m.leftW)

	hs.send(tea.MouseMsg{X: 49, Y: 3, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	hs.send(tea.MouseMsg{X: 49, Y: 3, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	assert.False(t, hs.m.dragging)
	assert.Equal(t, 50, hs.m.leftW)
	assert.Empty(t, hs.term(0).mice, "no drag event reached the terminal")
	for _, msg := range (*hs.inner)[innerBefore:] {
		_, isMouse := msg.(tea.MouseMsg)
		assert.False(t, isMouse, "no drag event reached the diff")
	}

	hs.send(tea.WindowSizeMsg{Width: 200, Height: 30})
	assert.Equal(t, 100, hs.m.leftW, "window resize keeps the dragged proportion")

	// close and reopen: the dragged proportion is remembered
	hs.runPalette("close")
	assert.Equal(t, 200, hs.m.leftW)
	hs.runPalette("open")
	assert.Equal(t, 100, hs.m.leftW)
}

func TestSplit_DragSeparatorBetweenTerminals(t *testing.T) {
	hs := newHarness(t, 100, 30)
	hs.runPalette("open")
	hs.runPalette("open")
	require.Len(t, hs.m.terms, 3)
	// 40 columns: 2 separators + 38 = 13 | 13 | 12 at x 0, 14, 28 (screen 60, 74, 88)
	require.Equal(t, []int{13, 13, 12}, []int{hs.term(0).w, hs.term(1).w, hs.term(2).w})
	innerBefore := len(*hs.inner)

	// grab the separator between terminals 1 and 2 (screen column 87)
	hs.send(tea.MouseMsg{X: 87, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	require.True(t, hs.m.dragging)
	hs.send(tea.MouseMsg{X: 80, Y: 4, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	assert.Equal(t, []int{13, 6 + 4, 12 + 3}, []int{hs.term(0).w, hs.term(1).w, hs.term(2).w},
		"only the two neighbors change, and the left one stops at the 10-column minimum")
	hs.send(tea.MouseMsg{X: 84, Y: 4, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	assert.Equal(t, []int{13, 10, 15}, []int{hs.term(0).w, hs.term(1).w, hs.term(2).w})
	hs.send(tea.MouseMsg{X: 90, Y: 4, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	assert.Equal(t, []int{13, 15, 10}, []int{hs.term(0).w, hs.term(1).w, hs.term(2).w},
		"moving right grows the left neighbor until the right one hits the minimum")
	hs.send(tea.MouseMsg{X: 90, Y: 4, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	assert.False(t, hs.m.dragging)

	assert.Equal(t, 60, hs.m.leftW, "the diff is untouched")
	assert.Len(t, *hs.inner, innerBefore, "the diff got no events at all, not even a same-size resize")
	for i := range 3 {
		assert.Empty(t, hs.term(i).mice, "no drag event reached terminal %d", i)
	}

	// dragging the main divider keeps the terminals' proportions
	hs.send(tea.MouseMsg{X: 59, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	hs.send(tea.MouseMsg{X: 19, Y: 4, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	hs.send(tea.MouseMsg{X: 19, Y: 4, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	require.Equal(t, 20, hs.m.leftW)
	w := []int{hs.term(0).w, hs.term(1).w, hs.term(2).w}
	assert.Equal(t, 78, w[0]+w[1]+w[2], "80 columns minus 2 separators")
	assert.Greater(t, w[1], w[0], "the dragged-wider middle terminal stays the widest")

	// closing one gives its space to the others; opening one keeps the others' proportions
	hs.runPalette("close") // closes the focused (last opened) one
	require.Len(t, hs.m.terms, 2)
	assert.Equal(t, 79, hs.term(0).w+hs.term(1).w)
	assert.Greater(t, hs.term(1).w, hs.term(0).w, "proportions kept after closing")
}

func TestSplit_QuitAsksWhileTerminalRuns(t *testing.T) {
	hs := newHarness(t, 100, 30)
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket}) // diff focus

	cmd := hs.key("q")
	require.NotNil(t, cmd)
	msg := hs.run(cmd)
	assert.IsType(t, quitRequestMsg{}, msg, "the inner quit is intercepted")

	hs.send(msg)
	assert.True(t, hs.m.quitDialog)
	view := ansi.Strip(hs.m.View())
	assert.Contains(t, view, "Quit aidiff?", "shown as a popup dialog")
	assert.Contains(t, view, "Closes the terminal and everything running in it.")
	assert.Contains(t, view, "Cancel")

	assert.Nil(t, hs.key("n"), "n cancels")
	assert.False(t, hs.m.quitDialog)

	hs.send(quitRequestMsg{})
	cmd = hs.key("y")
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, hs.run(cmd), "y quits")

	hs.runPalette("close")
	cmd = hs.send(quitRequestMsg{})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, hs.run(cmd), "no dialog without running terminals")
}

func TestSplit_QuitDialogKeys(t *testing.T) {
	hs := newHarness(t, 100, 30)
	hs.runPalette("open")
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlQ})
	require.True(t, hs.m.quitDialog)
	assert.Equal(t, cancelButton, hs.m.quitSel, "Cancel is preselected")
	assert.Contains(t, ansi.Strip(hs.m.View()), "Closes 2 terminals and everything running in them.")

	assert.Nil(t, hs.key("x"), "other keys are ignored")
	assert.True(t, hs.m.quitDialog, "and keep the dialog open")
	assert.Empty(t, hs.term(1).text, "nothing typed into the terminal")

	assert.Nil(t, hs.send(tea.KeyMsg{Type: tea.KeyEnter}), "enter on Cancel cancels")
	assert.False(t, hs.m.quitDialog)

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlQ})
	hs.send(tea.KeyMsg{Type: tea.KeyLeft})
	assert.Equal(t, quitButton, hs.m.quitSel)
	cmd := hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, hs.run(cmd), "enter on Quit quits")

	hs = newHarness(t, 100, 30)
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlQ})
	cmd = hs.send(tea.KeyMsg{Type: tea.KeyCtrlQ})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, hs.run(cmd), "ctrl+q twice quits")

	hs = newHarness(t, 100, 30)
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlQ})
	hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, hs.m.quitDialog, "esc cancels")
	hs.send(tea.MouseMsg{X: 70, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlQ})
	before := len(hs.term(0).mice)
	hs.send(tea.MouseMsg{X: 70, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	assert.Len(t, hs.term(0).mice, before, "mouse is swallowed while the dialog is open")

	lines := strings.Split(hs.m.View(), "\n")
	for i, l := range lines {
		assert.Equal(t, 100, ansi.StringWidth(l), "row %d keeps full width with the dialog", i)
	}
}

func TestSplit_CtrlQFromEitherPane(t *testing.T) {
	for _, termFocused := range []bool{false, true} {
		hs := newHarness(t, 100, 30)
		if !termFocused {
			hs.send(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
		}
		before := len(*hs.inner)

		assert.Nil(t, hs.send(tea.KeyMsg{Type: tea.KeyCtrlQ}), "running terminal: ask, don't quit yet")
		assert.True(t, hs.m.quitDialog, "termFocused=%v", termFocused)
		assert.Empty(t, hs.term(0).text, "ctrl+q is not typed into the terminal")
		assert.Len(t, *hs.inner, before, "ctrl+q is not passed to the diff")

		cmd := hs.key("y")
		require.NotNil(t, cmd)
		assert.IsType(t, tea.QuitMsg{}, hs.run(cmd))
	}
}

// run runs cmd the way the program does with QuitGuard installed.
func (hs *harness) run(cmd tea.Cmd) tea.Msg {
	return filterQuit(hs.m, cmd())
}

func TestSplit_QuitGuardCatchesEveryQuit(t *testing.T) {
	hs := newHarness(t, 100, 30)
	assert.Equal(t, quitRequestMsg{}, filterQuit(hs.m, tea.QuitMsg{}),
		"an unconfirmed quit becomes a request, whatever command sent it")
	other := FlashMsg{Text: "x"}
	assert.Equal(t, other, filterQuit(hs.m, other), "other messages pass untouched")
	assert.Equal(t, tea.QuitMsg{}, filterQuit(innerModel{}, tea.QuitMsg{}), "another root model is left alone")
}

// sequenceQuitter quits through tea.Sequence, whose message Bubble Tea
// unpacks itself, out of reach of anything wrapping commands.
type sequenceQuitter struct {
	innerModel
}

func (s sequenceQuitter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == s.quitKey {
		noop := func() tea.Msg {
			return nil
		}
		return s, tea.Sequence(noop, tea.Quit)
	}
	return s.innerModel.Update(msg)
}

func TestSplit_QuitGuardInAProgram(t *testing.T) {
	got := []tea.Msg{}
	m := New(sequenceQuitter{innerModel{got: &got, quitKey: "q"}}, Config{})
	p := tea.NewProgram(m, QuitGuard(), tea.WithInput(strings.NewReader("q")), tea.WithOutput(io.Discard),
		tea.WithoutRenderer(), tea.WithoutSignalHandler())
	final := runProgram(t, p)
	assert.True(t, final.(Model).quitConfirmed, "the sequence's quit went through the guard")
}

// runProgram runs p to its end, failing the test if it does not end in time.
func runProgram(t *testing.T, p *tea.Program) tea.Model {
	t.Helper()
	type result struct {
		model tea.Model
		err   error
	}
	done := make(chan result, 1)
	go func() {
		model, err := p.Run()
		done <- result{model, err}
	}()
	select {
	case r := <-done:
		require.NoError(t, r.err)
		return r.model
	case <-time.After(testTimeout):
		p.Kill()
		t.Fatal("the program did not end")
		return nil
	}
}

func TestSplit_QuitDialogListsEveryPane(t *testing.T) {
	hs := newHarness(t, 200, 30) // room for four panes
	for _, note := range []string{"Closes the chat.", "Closes the log.", "Closes the chat."} {
		hs.send(OpenPaneMsg{Start: func(w, h int) (Terminal, error) {
			return notedTerm{newFakeTerm(w, h), note}, nil
		}})
	}
	require.Len(t, hs.m.terms, 4)
	hs.send(quitRequestMsg{})
	require.True(t, hs.m.quitDialog)
	view := ansi.Strip(hs.m.View())
	assert.Contains(t, view, "Closes the terminal and everything running in it.", "the shell")
	assert.Contains(t, view, "Closes the chat.", "and every other pane in its own words")
	assert.Contains(t, view, "Closes the log.")
	assert.Equal(t, 1, strings.Count(view, "Closes the chat."), "a repeated note is said once")
	assert.NotContains(t, view, "Leaves the review.", "the idle text is only for nothing open")
}

// notedTerm is a pane with its own quit note.
type notedTerm struct {
	*fakeTerm
	note string
}

func (n notedTerm) QuitNote() string {
	return n.note
}

// blockingTerm's Close waits until release is closed.
type blockingTerm struct {
	*fakeTerm
	started chan<- struct{}
	release <-chan struct{}
}

func (b blockingTerm) Close() {
	b.started <- struct{}{}
	<-b.release
	b.fakeTerm.Close()
}

func TestSplit_CloseClosesInParallelAndWaits(t *testing.T) {
	hs := newHarnessOpts(t, 100, 30, false)
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	var terms []blockingTerm
	for range 3 {
		hs.send(OpenPaneMsg{Start: func(w, h int) (Terminal, error) {
			bt := blockingTerm{fakeTerm: newFakeTerm(w, h), started: started, release: release}
			terms = append(terms, bt)
			return bt, nil
		}})
	}
	require.Len(t, hs.m.terms, 3)
	terms[0].exited = true
	hs.send(termUpdateMsg{term: terms[0]}) // closed in the background
	require.Len(t, hs.m.terms, 2)

	closed := make(chan struct{})
	go func() {
		hs.m.Close()
		close(closed)
	}()
	for range 3 {
		select {
		case <-started:
		case <-time.After(testTimeout):
			t.Fatal("the panes are not closed in parallel")
		}
	}
	select {
	case <-closed:
		t.Fatal("Close returned before the panes finished closing")
	case <-time.After(3 * testTick):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(testTimeout):
		t.Fatal("Close did not return")
	}
	for i, bt := range terms {
		assert.True(t, bt.isClosed(), "pane %d closed, the one closed earlier included", i)
	}
}

func TestSplit_ClosedPaneEndsItsWait(t *testing.T) {
	ft := newFakeTerm(10, 2)
	wait := waitForUpdate(ft)
	ft.Close()
	assert.Nil(t, wait(), "a closed pane's wait ends without a message")
}

func TestSplit_ConfiguredKeys(t *testing.T) {
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.OpenOnStart = true
		cfg.Keys = Keys{Focus: "alt+o", Quit: "alt+q", Palette: "alt+p"}
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	require.Len(t, hs.m.terms, 1)

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	assert.False(t, hs.m.palette.open, "the default palette key is just a key now")
	assert.Equal(t, []string{"\x10"}, hs.term(0).text, "and reaches the terminal")
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p"), Alt: true})
	assert.True(t, hs.m.palette.open, "the configured palette key opens it")
	assert.Contains(t, ansi.Strip(hs.m.View()), "alt+q", "Quit shows the configured key")
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p"), Alt: true})
	assert.False(t, hs.m.palette.open, "and closes it")

	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o"), Alt: true})
	assert.Equal(t, focusDiff, hs.m.focus, "the configured focus key cycles")
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q"), Alt: true})
	assert.True(t, hs.m.quitDialog, "the configured quit key asks")
}

func TestSplit_CursorOnlyWhenTerminalFocused(t *testing.T) {
	hs := newHarness(t, 100, 30)
	assert.Contains(t, hs.m.columnView(hs.m.leftBlock()), "C")
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	assert.NotContains(t, hs.m.columnView(hs.m.leftBlock()), "C")
}

const (
	testTimeout = 2 * time.Second
	testTick    = 10 * time.Millisecond
)

// busyInner is an inner model that can report itself busy.
type busyInner struct {
	innerModel
	busy *bool
}

func (b busyInner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	*b.got = append(*b.got, msg)
	return b, nil
}
func (b busyInner) CanRefresh() bool {
	return !*b.busy
}

type refreshMsg struct{}

func TestSplit_RefreshHeldWhileInnerBusy(t *testing.T) {
	got := []tea.Msg{}
	busy := true
	m := New(busyInner{innerModel: innerModel{got: &got}, busy: &busy}, Config{RefreshMsg: refreshMsg{}})
	send := func(msg tea.Msg) {
		model, _ := m.Update(msg)
		m = model.(Model)
	}
	count := func() int {
		n := 0
		for _, msg := range got {
			if _, ok := msg.(refreshMsg); ok {
				n++
			}
		}
		return n
	}

	send(FilesChangedMsg{})
	send(FilesChangedMsg{})
	assert.Zero(t, count(), "held while the diff is busy (typing an annotation, popup open...)")

	busy = false
	send(tea.KeyMsg{Type: tea.KeyEsc}) // the key that ends the busy state
	assert.Equal(t, 1, count(), "sent once when free, however many changes were held")

	send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	assert.Equal(t, 1, count(), "not repeated")

	send(FilesChangedMsg{})
	assert.Equal(t, 2, count(), "a free diff refreshes immediately")

	off := New(innerModel{got: &got}, Config{})
	model, cmd := off.Update(FilesChangedMsg{})
	assert.Nil(t, cmd)
	assert.False(t, model.(Model).refreshPending, "no RefreshMsg configured: nothing to do")
}

func TestSplit_ZeroSizeStillReachesInner(t *testing.T) {
	got := []tea.Msg{}
	m := New(innerModel{got: &got}, Config{})
	model, _ := m.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	_ = model
	require.Len(t, got, 1, "a 0x0 terminal is still a size: the diff must leave its loading state")
	assert.Equal(t, tea.WindowSizeMsg{}, got[0])
}
