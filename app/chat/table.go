package chat

import (
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// A pipe table in the agent's prose, as GitHub writes them, is drawn with
// box-drawing borders and fitted to the pane: wide columns shrink and their
// text wraps, a row growing taller, so no cell is ever cut.

// minColumn is the narrowest a shrunk column gets. A pane too narrow to
// give every column that stacks each record as "header: value" lines.
const minColumn = 8

// streamCursor is the mark a reply still being written ends with.
const streamCursor = "▍"

// align is how a column's cells sit in it.
type align int

const (
	alignLeft align = iota
	alignCenter
	alignRight
)

// table is a parsed pipe table. Its records may hold fewer or more cells
// than its header; drawing pads or drops them.
type table struct {
	header []string
	align  []align // one a header cell
	rows   [][]string
}

var (
	delimiterCell = regexp.MustCompile(`^:?-+:?$`)
	partialCell   = regexp.MustCompile(`^:?-*:?$`)
)

// tableAt is the table that starts at lines[i] and how many lines it
// takes, 0 when none does: a header row, a delimiter row with as many
// cells (|---|:---:|---:|, its outer pipes optional), and the rows under
// them up to the first line that is not one. While a reply is still being
// written (streaming), a last line that will likely become a table, a row
// opening with a pipe or a header over a delimiter row half written, is one
// already, so the table does not flicker in from raw text.
func tableAt(lines []string, i int, streaming bool) (table, int) {
	if !isRow(lines[i]) {
		return table{}, 0
	}
	header := splitRow(lines[i])
	last := i == len(lines)-1
	provisional := streaming && strings.HasPrefix(strings.TrimSpace(lines[i]), "|")
	switch {
	case !last && isDelimiter(lines[i+1], len(header)):
	case last && provisional:
		return table{header: header, align: make([]align, len(header))}, 1
	case !last && i+1 == len(lines)-1 && provisional && isPartialDelimiter(lines[i+1]):
		return table{header: header, align: alignments(splitRow(lines[i+1]), len(header))}, 2
	default:
		return table{}, 0
	}
	t := table{header: header, align: alignments(splitRow(lines[i+1]), len(header))}
	n := 2
	for i+n < len(lines) && isRow(lines[i+n]) {
		t.rows = append(t.rows, splitRow(lines[i+n]))
		n++
	}
	return t, n
}

// isRow reports a line that is a table row: one with a cell separator.
func isRow(line string) bool {
	s := strings.TrimSpace(line)
	return strings.HasPrefix(s, "|") || len(splitRow(s)) > 1
}

// isDelimiter reports the delimiter row of a header of n cells.
func isDelimiter(line string, n int) bool {
	if !strings.Contains(line, "|") {
		return false // "---" under a line is a heading's underline
	}
	cells := splitRow(line)
	return len(cells) == n && !slices.ContainsFunc(cells, func(c string) bool { return !delimiterCell.MatchString(c) })
}

// isPartialDelimiter reports the start of a delimiter row, as a streamed
// reply has it while the row is being written.
func isPartialDelimiter(line string) bool {
	s := strings.TrimSpace(line)
	return strings.HasPrefix(s, "|") && !slices.ContainsFunc(splitRow(s), func(c string) bool { return !partialCell.MatchString(c) })
}

// alignments reads the columns' alignment off the delimiter row's cells,
// left where it says nothing.
func alignments(cells []string, n int) []align {
	out := make([]align, n)
	for i := range min(n, len(cells)) {
		c := cells[i]
		switch left, right := strings.HasPrefix(c, ":"), strings.HasSuffix(c, ":") && len(c) > 1; {
		case left && right:
			out[i] = alignCenter
		case right:
			out[i] = alignRight
		}
	}
	return out
}

// splitRow is a row's cells, trimmed. Its outer pipes are optional; an
// escaped pipe (\|) and a pipe inside a `code span` belong to the cell's
// text.
func splitRow(line string) []string {
	s := strings.TrimPrefix(strings.TrimSpace(line), "|")
	var cells []string
	var cur strings.Builder
	sep := false // the last thing read was a separator
	for i := 0; i < len(s); {
		sep = false
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '|':
			cur.WriteByte('|')
			i += 2
		case s[i] == '`':
			n := len(s[i:]) - len(strings.TrimLeft(s[i:], "`"))
			if j := closingTicks(s, i+n, n); j >= 0 {
				cur.WriteString(strings.ReplaceAll(s[i:j+n], `\|`, "|"))
				i = j + n
				continue
			}
			cur.WriteString(s[i : i+n]) // never closed: plain backticks
			i += n
		case s[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			sep = true
			i++
		default:
			cur.WriteByte(s[i])
			i++
		}
	}
	if !sep {
		cells = append(cells, strings.TrimSpace(cur.String()))
	}
	return cells
}

// closingTicks is where the run of n backticks closing a code span opened
// before from starts, -1 when there is none.
func closingTicks(s string, from, n int) int {
	for k := from; k < len(s); {
		if s[k] != '`' {
			k++
			continue
		}
		m := len(s[k:]) - len(strings.TrimLeft(s[k:], "`"))
		if m == n {
			return k
		}
		k += m
	}
	return -1
}

// withCursor is t with the streaming cursor after the last cell written.
func (t table) withCursor() table {
	if len(t.rows) == 0 {
		t.header = append(slices.Clone(t.header[:len(t.header)-1]), t.header[len(t.header)-1]+streamCursor)
		return t
	}
	t.rows = slices.Clone(t.rows)
	last := slices.Clone(t.rows[len(t.rows)-1])
	last[len(last)-1] += streamCursor
	t.rows[len(t.rows)-1] = last
	return t
}

// segment is a run of a cell's text in one inline style.
type segment struct {
	text  string
	style int // segPlain, segBold or segCode
}

const (
	segPlain = iota
	segBold
	segCode
)

// inlineSegments reads a cell's inline markdown as the prose renderer
// does: [links](url) as their text, **bold**, `code`.
func inlineSegments(s string) []segment {
	s = linkSpan.ReplaceAllString(s, "$1")
	var out []segment
	for s != "" {
		loc := codeSpan.FindStringSubmatchIndex(s)
		if loc == nil {
			break
		}
		out = append(out, boldSegments(s[:loc[0]])...)
		out = append(out, segment{text: s[loc[2]:loc[3]], style: segCode})
		s = s[loc[1]:]
	}
	return append(out, boldSegments(s)...)
}

// boldSegments splits text without code spans at its **bold** runs.
func boldSegments(s string) []segment {
	var out []segment
	for s != "" {
		loc := boldSpan.FindStringSubmatchIndex(s)
		if loc == nil {
			break
		}
		if loc[0] > 0 {
			out = append(out, segment{text: s[:loc[0]]})
		}
		out = append(out, segment{text: s[loc[2]:loc[3]], style: segBold})
		s = s[loc[1]:]
	}
	if s != "" {
		out = append(out, segment{text: s})
	}
	return out
}

// visible is the text segments show, their markers gone.
func visible(segs []segment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.text)
	}
	return b.String()
}

// styledPiece is a row of wrapped styled text: drawn, and its width.
type styledPiece struct {
	row   string
	width int
	cont  bool
	joint string
}

// wrapSegments wraps styled text on what it shows, its first row taking
// first cells and the rest rest; each row is drawn in its runs' styles,
// plain runs in base.
func wrapSegments(segs []segment, first, rest int, base lipgloss.Style, st styles) []styledPiece {
	text := visible(segs)
	pieces := wrapLine(text, first, rest, false)
	out := make([]styledPiece, 0, len(pieces))
	pos := 0
	for _, pc := range pieces {
		pos += len(pc.joint)
		out = append(out, styledPiece{row: drawRange(segs, pos, pos+len(pc.text), base, st), width: ansi.StringWidth(pc.text),
			cont: pc.cont, joint: pc.joint})
		pos += len(pc.text)
	}
	return out
}

// drawRange draws the bytes from to to of what segs show, each run in its
// style.
func drawRange(segs []segment, from, to int, base lipgloss.Style, st styles) string {
	var b strings.Builder
	at := 0
	for _, s := range segs {
		lo, hi := max(from, at), min(to, at+len(s.text))
		if lo < hi {
			style := base
			switch s.style {
			case segBold:
				style = st.bold
			case segCode:
				style = st.code
			}
			b.WriteString(style.Render(s.text[lo-at : hi-at]))
		}
		at += len(s.text)
	}
	return b.String()
}

// tableRows draws t in w cells: a grid when every column can keep
// minColumn cells, or its natural width when narrower, else the records
// stacked.
func tableRows(t table, w int, st styles) block {
	n := len(t.header)
	header := make([][]segment, n)
	for c := range n {
		header[c] = inlineSegments(t.header[c])
	}
	records := make([][][]segment, len(t.rows))
	for r, row := range t.rows {
		records[r] = make([][]segment, n)
		for c := range min(n, len(row)) { // a ragged row is padded with empty cells, or loses its extra ones
			records[r][c] = inlineSegments(row[c])
		}
	}
	natural := make([]int, n)
	for c := range n {
		natural[c] = max(1, ansi.StringWidth(visible(header[c])))
		for _, rec := range records {
			natural[c] = max(natural[c], ansi.StringWidth(visible(rec[c])))
		}
	}
	widths, ok := fitColumns(natural, w-len(inset)-(3*n+1))
	if !ok {
		return stackedRows(header, records, w, st)
	}
	g := grid{widths: widths, align: t.align, st: st}
	var b block
	g.border(&b, "╭", "┬", "╮")
	g.record(&b, header, st.bold, 0)
	if len(records) > 0 {
		g.border(&b, "├", "┼", "┤")
	}
	for r, rec := range records {
		g.record(&b, rec, st.text, r+1)
	}
	g.border(&b, "╰", "┴", "╯")
	return b
}

// fitColumns is the columns' widths for avail cells of text: their natural
// widths when those fit, else a fair share. A column narrower than its
// share keeps its natural width, and the others split what is left. ok is
// false when a column would get less than minColumn.
func fitColumns(natural []int, avail int) (widths []int, ok bool) {
	widths = slices.Clone(natural)
	total := 0
	for _, n := range natural {
		total += n
	}
	if total <= avail {
		return widths, true
	}
	fixed := make([]bool, len(natural))
	remaining, open := avail, len(natural)
	for changed := true; changed && open > 0; {
		changed = false
		share := remaining / open
		for c, n := range natural {
			if !fixed[c] && n <= share {
				fixed[c], changed = true, true
				remaining -= n
				open--
			}
		}
	}
	if open == 0 {
		return widths, true
	}
	share, extra := remaining/open, remaining%open
	if share < minColumn {
		return nil, false
	}
	for c := range widths {
		if !fixed[c] {
			widths[c] = share
			if extra > 0 {
				widths[c]++
				extra--
			}
		}
	}
	return widths, true
}

// grid draws a table's rows in fitted columns.
type grid struct {
	widths []int
	align  []align
	st     styles
}

// border is a horizontal border, drawn with left, mid between the columns
// and right; a copy leaves it out.
func (g grid) border(b *block, left, mid, right string) {
	var s strings.Builder
	s.WriteString(left)
	for c, w := range g.widths {
		if c > 0 {
			s.WriteString(mid)
		}
		s.WriteString(strings.Repeat("─", w+2))
	}
	s.WriteString(right)
	b.add(inset+g.st.muted.Render(s.String()), rowMeta{table: &tableRow{record: -1}})
}

// record draws one record, each cell wrapped in its column, as many rows
// as its tallest cell takes; a copy of any of them yields the record's
// cells, tab separated.
func (g grid) record(b *block, cells [][]segment, base lipgloss.Style, id int) {
	lines := make([][]styledPiece, len(g.widths))
	text := make([]string, len(g.widths))
	height := 1
	for c, w := range g.widths {
		lines[c] = wrapSegments(cells[c], w, w, base, g.st)
		text[c] = visible(cells[c])
		height = max(height, len(lines[c]))
	}
	bar := g.st.muted.Render("│")
	copied := &tableRow{record: id, cells: strings.Join(text, "\t")}
	for r := range height {
		var row strings.Builder
		row.WriteString(inset + bar)
		for c, w := range g.widths {
			var pc styledPiece
			if r < len(lines[c]) {
				pc = lines[c][r]
			}
			row.WriteString(" " + aligned(pc.row, pc.width, w, g.align[c]) + " " + bar)
		}
		b.add(row.String(), rowMeta{table: copied})
	}
}

// aligned pads a drawn cell row of width cells to w, as its column aligns.
func aligned(row string, width, w int, a align) string {
	pad := max(0, w-width)
	switch a {
	case alignRight:
		return strings.Repeat(" ", pad) + row
	case alignCenter:
		return strings.Repeat(" ", pad/2) + row + strings.Repeat(" ", pad-pad/2)
	}
	return row + strings.Repeat(" ", pad)
}

// stackedRows draws a table too wide for the pane as its records one under
// another, each cell a "header: value" line wrapped under itself, a blank
// row between records. A table without records yet shows its header.
func stackedRows(header [][]segment, records [][][]segment, w int, st styles) block {
	var b block
	limit := max(1, w-len(inset))
	line := func(segs []segment, base lipgloss.Style) {
		for _, pc := range wrapSegments(segs, limit, max(1, limit-2), base, st) {
			lead := inset
			if pc.cont {
				lead += "  "
			}
			b.add(lead+pc.row, rowMeta{start: len(lead), end: len(lead) + pc.width, cont: pc.cont, joint: pc.joint})
		}
	}
	if len(records) == 0 {
		names := make([]string, len(header))
		for c, h := range header {
			names[c] = visible(h)
		}
		line([]segment{{text: strings.Join(names, " · ")}}, st.bold)
		return b
	}
	for r, rec := range records {
		if r > 0 {
			b.plain("")
		}
		for c, h := range header {
			label := segment{text: visible(h) + ": ", style: segBold}
			line(append([]segment{label}, rec[c]...), st.text)
		}
	}
	return b
}
