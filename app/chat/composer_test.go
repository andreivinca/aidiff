package chat

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPane_ComposerShowsSettings(t *testing.T) {
	conv := fixture()
	conv.Settings = Settings{Mode: "Plan", Attention: true, Model: "Sonnet 4 · high", Details: []string{"Claude Code", "approval required"}}
	p, f := newTestPane(t, 100, 20, conv)
	v := view(p)
	assert.Contains(t, v, "│   Plan  Sonnet 4 · high Claude Code · approval required")
	assert.NotContains(t, v, "─", "a blank gap separates the composer from the conversation")
	assert.NotContains(t, v, "ctrl+v", "paste needs no hint")
	assert.Contains(t, v, "shift+enter newline")
	assert.Contains(t, v, "esc stop…")
	assert.Contains(t, v, "enter queue", "a busy agent receives the message as its next turn")

	p.Resize(40, 20)
	assert.Contains(t, view(p), "│   Plan  Sonnet 4 · high Claude Code", "narrow panes drop the last details first")
	assert.NotContains(t, view(p), "approval")
	p.Resize(100, 20)

	p.SendText("keep this draft")
	conv.State, conv.Running = AgentIdle, nil
	conv.Settings = Settings{Mode: "Build", Model: "GPT-5"}
	show(t, p, f, conv)
	assert.Contains(t, view(p), "│   Build  GPT-5")
	assert.Contains(t, view(p), "│   keep this draft█")
	assert.NotContains(t, view(p), "esc stop…", "nothing to stop")
	assert.Contains(t, view(p), "enter send")
	p.Follow(nil, "Other thread")
	assert.NotContains(t, view(p), "GPT-5", "following another backend clears the settings")
}

func TestPane_ComposerKeepsCursorOnResize(t *testing.T) {
	p := New(nil, "Resize", 80, 20, Config{NoColor: true})
	t.Cleanup(p.Close)
	for _, draft := range []string{"", "0123456789", "first\nsecond\nthird\nfourth\nfifth\nsixth\nlast", "界界 café é", "/he"} {
		p.SendText("\x15")
		p.Paste(draft)
		for _, w := range []int{1, 2, 5, 6, 12, 32, 60, 100} {
			for _, h := range []int{1, 2, 3, 4, 6, 10, 20} {
				t.Run(fmt.Sprintf("%q/%dx%d", draft, w, h), func(t *testing.T) {
					p.Resize(w, h)
					frame := p.Render(true)
					assert.Equal(t, ansi.Strip(frame), frame, "NoColor emits no escape sequences")
					rows := strings.Split(frame, "\n")
					require.Len(t, rows, h)
					for i, row := range rows {
						assert.Equal(t, w, ansi.StringWidth(row), "row %d", i)
					}
					assert.Contains(t, frame, "█", "the focused input cursor stays visible")
					assert.NotContains(t, p.Render(false), "█", "unfocused inputs hide their cursor")
					assert.Equal(t, draft, p.input.text(), "resizing keeps the draft intact")
				})
			}
		}
	}
}

func TestPane_ComposerScrollsDraftAndPopup(t *testing.T) {
	p, _ := newTestPane(t, 60, 9, fixture())
	p.Paste("one\ntwo\nthree\nfour\nfive\nsix\nseven")
	assert.Contains(t, view(p), "seven█")
	assert.NotContains(t, view(p), "│   one")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyHome})
	assert.Contains(t, view(p), "│   one", "moving the cursor scrolls the draft back into view")
	assert.NotContains(t, view(p), "seven█")
	p.SendText("\x15/")
	for range 7 {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	}
	assert.Contains(t, view(p), "/attach <image file>", "the selected command stays visible in a short popup")
	assert.Contains(t, view(p), "↑↓ select")
	p.SendText("\x1b")
	assert.NotContains(t, view(p), "/attach <image file>")
	assert.Contains(t, view(p), "shift+enter newline", "closing the popup brings the hint row back")
}

func TestPane_HintRowListsTheHostKeys(t *testing.T) {
	p := New(nil, "Hosted", 80, 12, Config{NoColor: true})
	assert.NotContains(t, view(p), "commands", "a pane lists no host shortcut it was not given")
	p = New(nil, "Hosted", 80, 12, Config{NoColor: true, HostKeys: []Hint{{Key: "ctrl+p", Label: "commands"}}})
	assert.Contains(t, view(p), "ctrl+p commands")
}

func TestPane_ComposerBackgroundSurvivesNestedStyles(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
	})
	f := newFakeBackend(fixture())
	p := New(f, "Colors", 80, 20, Config{})
	t.Cleanup(p.Close)
	awaitShown(t, p, f)
	for _, draft := range []string{"", "/he", "a draft"} {
		p.SendText("\x15" + draft)
		var rows []string
		locked(p, func() {
			rows = p.composerRowsLocked(80, 17, true)
		})
		emu := vt.NewEmulator(80, len(rows)+1)
		_, err := emu.WriteString(strings.Join(rows, "\r\n"))
		require.NoError(t, err)
		for y, row := range rows[:len(rows)-1] { // the shortcut row uses the terminal background
			if row == "" { // outer spacing also uses the terminal background
				continue
			}
			start := 0
			if strings.HasPrefix(ansi.Strip(row), "│") {
				start = 2 // the accent rail and its gap sit outside the panel
			}
			for x := start; x < 80; x++ {
				cell := emu.CellAt(x, y)
				require.NotNil(t, cell)
				require.NotNil(t, cell.Style.Bg, "draft %q, cell %d,%d", draft, x, y)
				assert.Equal(t, color.RGBA{R: 32, G: 32, B: 32, A: 255}, color.RGBAModel.Convert(cell.Style.Bg),
					"draft %q, cell %d,%d", draft, x, y)
			}
		}
	}
}

func TestEditor_VerticalMovesBetweenRows(t *testing.T) {
	var e editor
	e.set("first line\nab\nthird line")
	e.moveTo(len(e.clusters), false) // the end of "third line"
	require.True(t, e.vertical(-1, 40, false))
	assert.Equal(t, len("first line\nab"), e.cur, "a short row takes the cursor to its end")
	require.True(t, e.vertical(-1, 40, false))
	assert.Equal(t, 2, e.cur, "the column comes from the row the cursor was on")
	assert.False(t, e.vertical(-1, 40, false), "no row above the first")
	require.True(t, e.vertical(1, 40, true))
	lo, hi, ok := e.selection()
	assert.True(t, ok)
	assert.Equal(t, []int{2, len("first line\nab")}, []int{lo, hi}, "shift selects")
	require.True(t, e.vertical(1, 40, false))
	assert.Equal(t, len("first line\nab\nth"), e.cur)
	assert.False(t, e.vertical(1, 40, false), "no row below the last")

	e.set("abcdefghijkl")
	e.moveTo(12, false) // 5 cells wide: "abcd" "efgh" "ijkl", the cursor after "l"
	require.True(t, e.vertical(-1, 5, false))
	assert.Equal(t, 7, e.cur, "a row the text wraps out of ends before the next row's start")
}

func TestPane_UpAndDownMoveTheCursorBeforeScrolling(t *testing.T) {
	p, _ := newTestPane(t, 40, 8, longConversation())
	view(p)
	p.Paste("one\ntwo")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	locked(p, func() {
		assert.Equal(t, 3, p.input.cur, "up moves to the line above")
		assert.Zero(t, p.scroll, "the transcript stays")
	})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	locked(p, func() {
		assert.Equal(t, 1, p.scroll, "on the first line up scrolls")
	})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	locked(p, func() {
		assert.Equal(t, len("one\ntwo"), p.input.cur, "down moves to the line below")
		assert.Equal(t, 1, p.scroll)
	})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	locked(p, func() {
		assert.Zero(t, p.scroll, "on the last line down scrolls")
	})
}

func TestPane_TallDraftScrollsLikeATextarea(t *testing.T) {
	p, _ := newTestPane(t, 60, 30, longConversation())
	p.Paste("line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8")
	shown := func() string {
		var out []string
		for _, l := range []string{"line1", "line2", "line3", "line4", "line5", "line6", "line7", "line8"} {
			if strings.Contains(view(p), l) {
				out = append(out, l[4:])
			}
		}
		return strings.Join(out, "")
	}
	assert.Equal(t, "45678", shown(), "the box shows the draft's end, where the cursor is")
	for i := range 4 {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
		assert.Equal(t, "45678", shown(), "the box stays put while the cursor moves within it (press %d)", i+1)
	}
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	assert.Equal(t, "34567", shown(), "past its top edge the box scrolls one row")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	assert.Equal(t, "34567", shown(), "moving back down within it does not scroll")
	locked(p, func() {
		assert.Zero(t, p.scroll, "the transcript never moved")
	})
	for range 3 {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	}
	assert.Equal(t, "12345", shown())
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	locked(p, func() {
		assert.Equal(t, 1, p.scroll, "on the draft's first line up scrolls the transcript")
	})
}

func TestPane_BlocksAreDrawnAsChips(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
	})
	f := newFakeBackend(fixture())
	p := New(f, "Colors", 80, 20, Config{})
	t.Cleanup(p.Close)
	awaitShown(t, p, f)
	locked(p, func() {
		p.input.insert("see ")
		p.input.insertAtom("[Image #1]")
		p.input.insert(" now")
	})
	var rows []string
	locked(p, func() {
		rows = p.composerRowsLocked(80, 17, true)
	})
	emu := vt.NewEmulator(80, len(rows)+1)
	_, err := emu.WriteString(strings.Join(rows, "\r\n"))
	require.NoError(t, err)
	y := slices.IndexFunc(rows, func(r string) bool {
		return strings.Contains(ansi.Strip(r), "[Image #1]")
	})
	require.GreaterOrEqual(t, y, 0)
	x := strings.Index(ansi.Strip(rows[y]), "[Image #1]")
	x = ansi.StringWidth(ansi.Strip(rows[y])[:x])
	bg := func(x int) color.RGBA {
		cell := emu.CellAt(x, y)
		require.NotNil(t, cell)
		require.NotNil(t, cell.Style.Bg)
		return color.RGBAModel.Convert(cell.Style.Bg).(color.RGBA)
	}
	near := func(a, b color.RGBA) bool { // the emulator rounds a channel by one
		d := func(x, y uint8) bool { return max(x, y)-min(x, y) <= 1 }
		return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B)
	}
	chip, panel := color.RGBA{R: 0x3d, G: 0x31, B: 0x60, A: 255}, color.RGBA{R: 32, G: 32, B: 32, A: 255}
	for i := range len("[Image #1]") {
		assert.True(t, near(chip, bg(x+i)), "the block's cell %d is a chip: %v", i, bg(x+i))
	}
	assert.Equal(t, panel, bg(x-1), "the text around it is not")
	assert.Equal(t, panel, bg(x+len("[Image #1]")))
}
