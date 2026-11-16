package termpane

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// renderedFrame is a Render result and what it was built from.
type renderedFrame struct {
	valid      bool
	gen        uint64
	drawCursor bool
	out        string
}

// Render returns exactly height lines of exactly width cells. When showCursor
// is set, the cursor cell is drawn in reverse video.
func (p *Pane) Render(showCursor bool) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	drawCursor := showCursor && !p.emu.CursorHidden() && !p.Exited()
	if f := p.frame; f.valid && f.gen == p.screenGen && f.drawCursor == drawCursor {
		return f.out
	}
	out := p.renderLocked(drawCursor)
	p.frame = renderedFrame{valid: true, gen: p.screenGen, drawCursor: drawCursor, out: out}
	return out
}

// renderLocked builds the frame from the emulator cells; mu must be held.
func (p *Pane) renderLocked(drawCursor bool) string {
	w, h := p.emu.Width(), p.emu.Height()
	cur := p.emu.CursorPosition()

	var b strings.Builder
	b.Grow(h * (w + 16))
	line := make(uv.Line, w)
	for y := range h {
		for x := range w {
			if c := p.emu.CellAt(x, y); c != nil {
				line[x] = *c
			} else {
				line[x] = uv.EmptyCell
			}
		}
		if drawCursor && y == cur.Y && cur.X >= 0 && cur.X < w {
			c := &line[cur.X]
			if c.IsZero() || c.Content == "" {
				*c = uv.EmptyCell
				c.Content = " "
			}
			c.Style.Attrs ^= uv.AttrReverse
		}
		rendered := line.Render() + ansi.ResetStyle
		if pad := w - ansi.StringWidth(rendered); pad > 0 {
			rendered += strings.Repeat(" ", pad)
		}
		b.WriteString(rendered)
		if y < h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
