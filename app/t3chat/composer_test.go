package t3chat

import (
	"encoding/json"
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

	"github.com/umputun/revdiff/app/t3client"
)

func TestPane_ComposerFollowsThreadSettings(t *testing.T) {
	snap := fixture()
	snap.Thread = t3client.Thread{
		InteractionMode: "plan", RuntimeMode: "approval-required",
		ModelSelection: &t3client.ModelSelection{InstanceID: "claude", Model: "claude-sonnet-4"},
	}
	p, f := newTestPane(t, 100, 20, snap)
	v := view(p)
	assert.Contains(t, v, "│   Plan  Sonnet 4 Claude Code · approval required")
	assert.NotContains(t, v, "─", "a blank gap separates the composer from the conversation")
	assert.NotContains(t, v, "> █", "the input no longer has a shell prompt")
	assert.Contains(t, v, "ctrl+v paste")
	assert.Contains(t, v, "shift+enter newline")
	assert.Contains(t, v, "esc stop…")
	assert.Contains(t, v, "enter queue", "a busy agent receives the message as its next turn")

	p.SendText("keep this draft")
	f.live().Thread(t3client.Thread{ID: "th-1", InteractionMode: "default", RuntimeMode: "full-access",
		ModelSelection: &t3client.ModelSelection{InstanceID: "codex", Model: "gpt-5"}})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "│   Build  GPT-5 Codex · full access")
	assert.Contains(t, view(p), "│   keep this draft█")
	f.live().Thread(t3client.Thread{ID: "th-1", Title: "A new title"})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "Build  GPT-5 Codex · full access", "partial metadata keeps the settings")
	f.live().Thread(t3client.Thread{ID: "other", InteractionMode: "plan"})
	assert.Contains(t, view(p), "Build  GPT-5", "another thread cannot change the composer")

	p.SendText("\x15/clear\r")
	assert.Contains(t, view(p), "fresh context")
	p.Follow(nil, "th-2", "Other thread")
	assert.NotContains(t, view(p), "GPT-5", "switching threads clears the previous model")
	assert.NotContains(t, view(p), "full access")
}

func TestPane_ComposerKeepsCursorOnResize(t *testing.T) {
	p := New(nil, "th-1", "Resize", 80, 20, Config{NoColor: true})
	t.Cleanup(p.Close)
	for _, draft := range []string{"", "0123456789", "first\nsecond\nthird\nfourth\nfifth\nsixth\nlast", "界界 café e\u0301", "/mo"} {
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
	p, _ := withCatalog(t)
	p.Resize(60, 9)
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
	assert.Contains(t, view(p), "/model <model>", "the selected command stays visible in a short popup")
	assert.Contains(t, view(p), "↑↓ select")
	p.SendText("\x1b")
	assert.NotContains(t, view(p), "/model <model>")
	assert.Contains(t, view(p), "ctrl+p commands")
}

func TestPane_ComposerBackgroundSurvivesNestedStyles(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	p := New(nil, "th-1", "Colors", 80, 20, Config{})
	t.Cleanup(p.Close)
	for _, draft := range []string{"", "/mo", "a draft"} {
		p.SendText("\x15" + draft)
		p.mu.Lock()
		rows := p.composerRowsLocked(80, 17, true)
		p.mu.Unlock()
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

func TestPaneComposerShowsEffortAfterModel(t *testing.T) {
	snap := fixture()
	snap.Thread.ModelSelection = &t3client.ModelSelection{InstanceID: "codex", Model: "gpt-5", Options: json.RawMessage(`[{"id":"reasoningEffort","value":"high"}]`)}
	p, f := newTestPane(t, 100, 20, snap)
	assert.Contains(t, view(p), "GPT-5 · high Codex")
	f.live().Thread(t3client.Thread{ID: "th-1", ModelSelection: &t3client.ModelSelection{InstanceID: "claude", Model: "claude-sonnet-4", Options: json.RawMessage(`[{"id":"effort","value":"max"}]`)}})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "Sonnet 4 · max Claude Code")
	assert.NotContains(t, view(p), "high")
}
