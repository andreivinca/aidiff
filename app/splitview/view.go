package splitview

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// leftCache holds the wrapped model's fitted view. It is dropped whenever the
// wrapped model receives a message, so a terminal echo reuses it.
type leftCache struct {
	valid bool
	w, h  int
	lines []string

	footerValid bool // footer is the wrapped model's status line at footerW columns
	footerW     int
	footerHints [][2]string // the pane hints footer shows
	footer      string
}

// View implements tea.Model.
func (m Model) View() string {
	if m.winW == 0 {
		return m.inner.View()
	}
	view := m.columnView(m.leftBlock())
	if m.palette.open {
		view = overlayCenter(view, m.paletteView(), m.winW, m.height)
	}
	switch {
	case m.quitDialog:
		view = overlayCenter(view, m.quitDialogView(), m.winW, m.height)
	case m.flash != "":
		view = replaceLastLine(view, " "+m.flash+" ", m.winW)
	}
	return view
}

// leftBlock is the wrapped model's view fitted to its column, one line per
// row. It is cached until the wrapped model receives a message or the column
// changes size, so a terminal echo does not re-render the wrapped model.
func (m Model) leftBlock() []string {
	c := m.leftCache
	if !c.valid || c.w != m.leftW || c.h != m.height {
		c.lines = strings.Split(fitBlock(m.inner.View(), m.leftW, m.height), "\n")
		c.valid, c.w, c.h = true, m.leftW, m.height
	}
	return c.lines
}

// dropLeftCache forgets the fitted view once the wrapped model has changed.
func (m Model) dropLeftCache() {
	m.leftCache.valid = false
	m.leftCache.footerValid = false
}

// columnView renders the sidebar, when shown, the wrapped model's lines and
// the terminals side by side, with a vertical line between neighbors.
func (m Model) columnView(left []string) string {
	var side []string
	if m.outerW() > 0 {
		side = m.outerLines()
	}
	if m.sideW() > 0 {
		lines := m.sidebarLines()
		if side == nil {
			side = lines
		} else {
			for y := range side {
				side[y] += lines[y]
			}
		}
	}
	rects := m.termRects()
	blocks := make([][]string, len(rects))
	for i, r := range rects {
		showCursor := m.focus == i && !m.palette.open
		blocks[i] = strings.Split(fitBlock(m.terms[i].Render(showCursor), r.w, r.h), "\n")
	}
	rows := make([]string, m.bodyH())
	for y := range rows {
		var b strings.Builder
		if side != nil {
			b.WriteString(side[y])
		}
		if m.mainGripAt(y) {
			// our own divider over the wrapped model's last column; its
			// content has no border of its own
			b.WriteString(ansi.Cut(left[y], 0, max(0, m.leftW-1)))
			b.WriteString(m.resizeGrip(y, dragMain))
		} else {
			b.WriteString(left[y])
		}
		for i := range blocks {
			if i > 0 {
				b.WriteString(m.resizeGrip(y, i-1))
			}
			b.WriteString(blocks[i][y])
		}
		rows[y] = b.String()
	}
	if m.footerRows() > 0 {
		rows = append(rows, m.withFocusMark(m.footerLine())) // footer_focus.go
	}
	return strings.Join(rows, "\n")
}

// resizeGrip marks each wrapper-owned divider and highlights a grabbed one.
func (m Model) resizeGrip(y, divider int) string {
	glyph := "│"
	if y == m.height/2 {
		glyph = "↔"
	}
	if m.dragging && m.dragSep == divider {
		return m.styles.gripActive.Render(glyph)
	}
	return m.styles.grip.Render(glyph)
}

func replaceLastLine(view, text string, width int) string {
	lines := strings.Split(view, "\n")
	lines[len(lines)-1] = lipgloss.NewStyle().Reverse(true).Width(width).MaxWidth(width).Render(text)
	return strings.Join(lines, "\n")
}

// overlayCenter draws box over base, horizontally centered, a third of the
// way down.
func overlayCenter(base, box string, width, height int) string {
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")
	bw := 0
	for _, l := range boxLines {
		bw = max(bw, ansi.StringWidth(l))
	}
	x0 := max(0, (width-bw)/2)
	y0 := max(0, (height-len(boxLines))/3)
	for i, bl := range boxLines {
		y := y0 + i
		if y >= len(baseLines) {
			break
		}
		line := baseLines[y]
		left := fitLine(ansi.Cut(line, 0, x0), x0)
		right := ansi.Cut(line, x0+bw, width)
		baseLines[y] = left + ansi.ResetStyle + fitLine(bl, bw) + ansi.ResetStyle + right
	}
	return strings.Join(baseLines, "\n")
}

// fitBlock forces s to exactly w columns by h rows, so panes line up even if
// a pane renders a ragged view.
func fitBlock(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = fitLine(l, w)
	}
	return strings.Join(lines, "\n")
}

// fitLine truncates or pads l to exactly w columns.
func fitLine(l string, w int) string {
	if ansi.StringWidth(l) > w {
		l = ansi.Truncate(l, w, "")
	}
	if pad := w - ansi.StringWidth(l); pad > 0 {
		l += strings.Repeat(" ", pad)
	}
	return l
}
