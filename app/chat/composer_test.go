package chat

import (
	"fmt"
	"image/color"
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
	assert.Contains(t, v, "ctrl+v paste")
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
	assert.Contains(t, view(p), "ctrl+v paste", "closing the popup brings the hint row back")
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
