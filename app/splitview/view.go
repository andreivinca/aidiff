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

	cut []string // lines cut one column short for the main grip; nil until needed

	footerValid bool // footer is the wrapped model's status line at footerW columns
	footerW     int
	footerHints [][2]string // the pane hints footer shows
	footer      string
}

// paneCache keeps each column's fitted block from the frames before, keyed
// by what it was fitted from, and the status line with its focus mark: a
// frame redrawn because one pane changed then measures that pane alone.
// Like leftCache it is shared across copies of the Model.
type paneCache struct {
	slots []fitSlot // slotOuter, slotSide, then slotTerms+i for terminal i

	markValid bool
	markKey   markKey
	mark      string
}

// paneCache slots
const (
	slotOuter = iota
	slotSide
	slotTerms
)

// fitSlot is one column's fitted block and what it was fitted from.
type fitSlot struct {
	valid bool
	src   string
	w, h  int
	lines []string
}

// markKey is what the status line's focus mark is drawn from.
type markKey struct {
	line             string
	winW, from, to   int
	colorBg, colorFg string
}

// fitted is src fitted by fit to w by h, one line per row, reused from slot
// i while src and the size stay the same. The lines must not be changed.
func (m Model) fitted(i int, src string, w, h int, fit func(src string, w, h int) []string) []string {
	c := m.panes
	if c == nil {
		return fit(src, w, h)
	}
	for len(c.slots) <= i {
		c.slots = append(c.slots, fitSlot{})
	}
	if sl := c.slots[i]; sl.valid && sl.w == w && sl.h == h && sl.src == src {
		return sl.lines
	}
	lines := fit(src, w, h)
	c.slots[i] = fitSlot{valid: true, src: src, w: w, h: h, lines: lines}
	return lines
}

// dropSlotsFrom forgets the fitted blocks of slots i and above.
func (m Model) dropSlotsFrom(i int) {
	if c := m.panes; c != nil && len(c.slots) > i {
		clear(c.slots[i:])
		c.slots = c.slots[:i]
	}
}

// fitLines is fitBlock split into its lines.
func fitLines(src string, w, h int) []string {
	return strings.Split(fitBlock(src, w, h), "\n")
}

// View implements tea.Model.
func (m Model) View() string {
	if m.winW == 0 {
		return m.inner.View()
	}
	view := m.columnView(m.leftBlock())
	if m.dragging && m.dragMoved {
		view = m.dragGhost(view)
	}
	if m.palette.open {
		view = overlayCenter(view, m.paletteView(), m.winW, m.height)
	}
	if m.switcher.open {
		view = m.switcherView(view)
	}
	switch {
	case m.quitDialog:
		view = overlayCenter(view, m.quitDialogView(), m.winW, m.height)
	case m.confirm != nil:
		view = overlayCenter(view, m.confirmView(), m.winW, m.height)
	case m.flash != "":
		view = replaceLastLine(view, " "+m.flash+" ", m.winW)
	}
	return view
}

// leftBlock is the wrapped model's view fitted to its column, one line per
// row. It is cached until the wrapped model receives a message or the column
// changes size, so a terminal echo does not re-render the wrapped model.
func (m Model) leftBlock() []string {
	c, h := m.leftCache, m.paneH()
	if !c.valid || c.w != m.leftW || c.h != h {
		c.lines = strings.Split(fitBlock(m.inner.View(), m.leftW, h), "\n")
		c.valid, c.w, c.h, c.cut = true, m.leftW, h, nil
	}
	return c.lines
}

// leftCut is leftBlock with each line one column short, for the rows the
// main grip is drawn over; cached with it.
func (m Model) leftCut() []string {
	lines, c := m.leftBlock(), m.leftCache
	if c.cut == nil {
		c.cut = make([]string, len(lines))
		for y, l := range lines {
			c.cut[y] = ansi.Cut(l, 0, max(0, m.leftW-1))
		}
	}
	return c.cut
}

// dropLeftCache forgets the fitted view once the wrapped model has changed.
func (m Model) dropLeftCache() {
	m.leftCache.valid = false
	m.leftCache.footerValid = false
}

// columnView renders the tab strip, when shown, then the sidebars, the
// wrapped model's lines and the terminals side by side, with a vertical
// line between neighbors, then the status line.
func (m Model) columnView(left []string) string {
	var outer, side []string
	if m.outerW() > 0 {
		outer = m.outerLines()
	}
	if m.sideW() > 0 {
		side = m.sidebarLines()
	}
	rects := m.termRects()
	blocks := make([][]string, len(rects))
	for i, r := range rects {
		showCursor := m.focus == i && !m.palette.open
		blocks[i] = m.fitted(slotTerms+i, m.terms[i].Render(showCursor), r.w, r.h, fitLines)
	}
	m.dropSlotsFrom(slotTerms + len(rects)) // closed panes' frames
	var cut []string
	if len(m.terms) > 0 {
		cut = m.leftCut()
	}
	grips := m.gripGlyphs()
	body := m.bodyH()
	rows := make([]string, m.top, m.top+body+1)
	if m.top > 0 {
		rows[0] = m.tabStrip().line
	}
	for y := range body {
		var b strings.Builder
		b.Grow(2 * m.winW)
		if outer != nil {
			b.WriteString(outer[y])
		}
		if side != nil {
			b.WriteString(side[y])
		}
		if m.mainGripAt(y) {
			// our own divider over the wrapped model's last column; its
			// content has no border of its own
			b.WriteString(cut[y])
			b.WriteString(grips.at(m, y, dragMain))
		} else {
			b.WriteString(left[y])
		}
		for i := range blocks {
			if i > 0 {
				b.WriteString(grips.at(m, y, i-1))
			}
			b.WriteString(blocks[i][y])
		}
		rows = append(rows, b.String())
	}
	if m.footerRows() > 0 {
		rows = append(rows, m.withFocusMark(m.footerLine())) // footer_focus.go
	}
	return strings.Join(rows, "\n")
}

// sidebarBlock is a sidebar's view, drawn by the embedding program and so
// cleaned of escapes other than its colors (cleanView), fitted to w by h
// with the separator after each row; reused while the view is the same.
func (m Model) sidebarBlock(slot int, view string, w, h int) []string {
	return m.fitted(slot, view, w, h, func(src string, w, h int) []string {
		lines := fitLines(cleanView(src), w, h)
		sep := m.styles.grip.Render("│")
		for i := range lines {
			lines[i] += sep
		}
		return lines
	})
}

// gripGlyphs are a divider's cells, rendered once a frame rather than once
// a row.
type gripGlyphs struct {
	line, mid, activeLine, activeMid string
	midRow                           int // the row under the strip that shows the grip
}

// gripGlyphs renders the divider cells for this frame.
func (m Model) gripGlyphs() gripGlyphs {
	return gripGlyphs{
		line:       m.styles.grip.Render("│"),
		mid:        m.styles.grip.Render("↔"),
		activeLine: m.styles.gripActive.Render("│"),
		activeMid:  m.styles.gripActive.Render("↔"),
		midRow:     m.paneH() / 2,
	}
}

// at marks each wrapper-owned divider on row y and highlights a grabbed one.
func (g gripGlyphs) at(m Model, y, divider int) string {
	active := m.dragging && m.dragSep == divider && !m.dragMoved // once moved, the highlight is where it goes
	switch {
	case y == g.midRow && active:
		return g.activeMid
	case y == g.midRow:
		return g.mid
	case active:
		return g.activeLine
	}
	return g.line
}

// dragGhost draws the dragged divider, highlighted, where the pointer took
// it, over the panes as they still are: they resize on release.
func (m Model) dragGhost(view string) string {
	pos, ok := m.dragTarget(m.dragX)
	if !ok {
		return view
	}
	col := m.leftCols() + pos
	g := m.gripGlyphs()
	lines := strings.Split(view, "\n")
	for y := range min(len(lines)-m.top, m.bodyH()) {
		if m.dragSep == dragMain && !m.mainGripAt(y) {
			continue
		}
		glyph := g.activeLine
		if y == g.midRow {
			glyph = g.activeMid
		}
		l := lines[m.top+y]
		lines[m.top+y] = ansi.Cut(l, 0, col) + glyph + ansi.Cut(l, col+1, m.winW)
	}
	return strings.Join(lines, "\n")
}

// replaceLastLine draws text, cleaned (it may come from the remote side),
// over the last line of view. Text wider than the window is cut, not
// wrapped onto lines the window does not have.
func replaceLastLine(view, text string, width int) string {
	lines := strings.Split(view, "\n")
	text = ansi.Truncate(cleanLine(text), width, "")
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
	x0, y0 := boxOrigin(bw, len(boxLines), width, height)
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

// boxOrigin is where overlayCenter draws a box w by h: its top left cell.
func boxOrigin(w, h, width, height int) (x0, y0 int) {
	return max(0, (width-w)/2), max(0, (height-h)/3)
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
