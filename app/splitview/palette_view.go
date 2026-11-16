package splitview

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	paletteMaxRows   = 10
	paletteWidth     = 64 // the command list, and the quit dialog
	pickerWidth      = 90 // pickers and prompts carry longer labels
	paletteMinWidth  = 20
	promptTailMarker = "…"

	boxBorder = 2 // columns the rounded border takes
	boxMargin = 2 // columns kept free on each side of a popup

	rowMinDetail = 12 // a row's detail keeps at least this many columns...
	rowMinLabel  = 10 // ...unless its label would get fewer than this
	rowDetailGap = 2  // one column before the detail, one after it
)

// boxInner is the content width of a popup at most maxW columns wide.
func (m Model) boxInner(maxW int) int {
	return min(maxW, max(paletteMinWidth, m.width-2*boxMargin)) - boxBorder
}

// paletteView renders the popup box.
func (m Model) paletteView() string {
	p := m.palette
	maxW := paletteWidth
	if p.mode != modeCommands {
		maxW = pickerWidth
	}
	inner := m.boxInner(maxW)
	dim := m.styles.muted

	var lines []string
	if p.title != "" {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Render(fitLine(" "+p.title, inner)))
	}
	lines = append(lines, p.inputLine(inner, dim))
	if p.mode == modePrompt {
		lines = append(lines, dim.Render(fitLine(" enter send · esc cancel", inner)))
		return m.styles.box.Render(strings.Join(lines, "\n"))
	}
	lines = append(lines, dim.Render(strings.Repeat("─", inner)))
	if len(p.rows) == 0 {
		empty := "  no matching commands"
		if p.mode == modePick {
			empty = "  no matches"
		}
		lines = append(lines, dim.Render(fitLine(empty, inner)))
	}
	start := max(0, p.sel-paletteMaxRows+1)
	for i := start; i < len(p.rows) && i < start+paletteMaxRows; i++ {
		lines = append(lines, rowLine(p.rows[i], inner, i == p.sel, dim))
	}
	return m.styles.box.Render(strings.Join(lines, "\n"))
}

// inputLine is the query line. A long prompt shows its tail so the cursor
// stays visible; an empty prompt shows the placeholder.
func (p palette) inputLine(inner int, dim lipgloss.Style) string {
	const prefix = " > "
	if p.query == "" && p.placeholder != "" {
		return fitLine(prefix+"█"+dim.Render(p.placeholder), inner)
	}
	q := p.query
	if room := inner - ansi.StringWidth(prefix) - 1; ansi.StringWidth(q) > room && room > 1 {
		q = promptTailMarker + ansi.TruncateLeft(q, ansi.StringWidth(q)-room+1, "")
	}
	return fitLine(prefix+q+"█", inner)
}

// rowLine renders one row: label left, detail right, reversed when selected.
// The label keeps its width; a long detail is shortened to the room left.
func rowLine(r row, inner int, selected bool, dim lipgloss.Style) string {
	label := "  " + r.label
	line := fitLine(label, inner)
	if r.detail != "" {
		labelW := ansi.StringWidth(label)
		room := inner - labelW - rowDetailGap
		if room < rowMinDetail {
			room = min(rowMinDetail, max(1, inner-rowMinLabel))
			labelW = inner - room - rowDetailGap
		}
		plain := ansi.Truncate(r.detail, room, "…")
		pad := max(1, inner-labelW-ansi.StringWidth(plain)-1)
		detail := plain
		if !selected {
			detail = dim.Render(plain)
		}
		line = fitLine(fitLine(label, labelW)+strings.Repeat(" ", pad)+detail+" ", inner)
	}
	if selected {
		line = lipgloss.NewStyle().Reverse(true).Render(line)
	}
	return line
}
