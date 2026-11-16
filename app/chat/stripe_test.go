package chat

import (
	"image/color"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPendingCard_StripedColumns(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
	})
	base, _ := newStyles(true)
	p := &Pane{styles: base}
	rows := p.pendingCard(queuedMessage{text: "queued 界 text", queued: true}, 30)
	emu := vt.NewEmulator(30, 2)
	_, err := emu.WriteString(rows[0])
	require.NoError(t, err)
	bg := func(x int) color.RGBA {
		cell := emu.CellAt(x, 0)
		require.NotNil(t, cell)
		require.NotNil(t, cell.Style.Bg, "cell %d", x)
		return color.RGBAModel.Convert(cell.Style.Bg).(color.RGBA)
	}
	near := func(a, b color.RGBA) bool { // the emulator rounds a channel by one
		d := func(x, y uint8) bool { return max(x, y)-min(x, y) <= 1 }
		return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B)
	}
	panel, stripe := color.RGBA{R: 0x14, G: 0x14, B: 0x14, A: 255}, color.RGBA{R: 0x16, G: 0x16, B: 0x16, A: 255}
	// cell 0 is the rail; the panel starts at cell 1: "  queued 界 text"
	wide := 1 + len("  queued ")
	for x := 1; x < 30; x++ {
		if x == wide+1 {
			continue // the wide character's second cell
		}
		want := panel
		if (x-1)%2 == 1 {
			want = stripe
		}
		assert.True(t, near(want, bg(x)), "cell %d: want %v, got %v", x, want, bg(x))
	}
	fg := color.RGBAModel.Convert(emu.CellAt(3, 0).Style.Fg).(color.RGBA)
	assert.True(t, near(color.RGBA{R: 0x80, G: 0x80, B: 0x80, A: 255}, fg), "the text is muted, as its state row: %v", fg)
	assert.Equal(t, "Queued", ansiStripTrim(rows[1]), "the state row keeps the pane's background")
}

func ansiStripTrim(s string) string {
	return strings.TrimSpace(ansi.Strip(s))
}

func TestPendingRows_BlankBetweenCards(t *testing.T) {
	base, _ := newStyles(false)
	p := &Pane{styles: base}
	rows := p.pendingRowsLocked([]queuedMessage{{text: "first", queued: true}, {text: "second", queued: true}}, 40, maxPendingRows)
	var got []string
	for _, r := range rows {
		got = append(got, strings.TrimSpace(strings.TrimPrefix(ansi.Strip(r), rail)))
	}
	assert.Equal(t, []string{"", "first", "Queued", "", "second", "Queued", ""}, got,
		"a blank row above each message and below each state, one between two cards")
}

func TestPane_BlankRowAboveTheStatus(t *testing.T) {
	conv := fixture()
	conv.State, conv.Running = AgentIdle, nil
	p, _ := newTestPane(t, 60, 20, conv)
	rows := strings.Split(view(p), "\n")
	i := slices.IndexFunc(rows, func(r string) bool { return strings.TrimSpace(r) == "Idle" })
	if assert.Positive(t, i) {
		assert.Empty(t, strings.TrimSpace(rows[i-1]), "a blank row between the transcript and the status")
		assert.NotEmpty(t, strings.TrimSpace(rows[i-2]), "right under the transcript's last line")
	}
}
