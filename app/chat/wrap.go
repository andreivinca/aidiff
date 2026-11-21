package chat

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/clipperhouse/uax29/v2/graphemes"
)

// rowMeta is how a copy of the selection reads one transcript row. The
// agent's text is wrapped, never cut, so one line of it may take several
// rows; a copy puts the line back together. The zero value reads a row as
// a line of its own from the inset on, its trailing blanks dropped.
type rowMeta struct {
	// start and end are the columns of the row's own text; what lies outside
	// them is the pane's: the inset, a rail, a hanging indent, the padding.
	// end is 0 for a row read the default way.
	start, end int
	cont       bool   // the row goes on with the line above it, which it was wrapped from
	joint      string // what the wrap took out between that row and this one, which a copy puts back: the spaces, or nothing where a word broke
	// table marks a row of a drawn table: a copy yields its record's cells,
	// once however many rows the record takes, and nothing of a border. A
	// pointer, as few rows are a table's and every row carries a rowMeta.
	table *tableRow
}

// tableRow is what a copy reads of a drawn table's row.
type tableRow struct {
	record int    // the record the row draws, so its rows copy once
	cells  string // the record's cells, tab separated; "" for a border row
}

// block is rendered rows and, row for row, how a copy reads them.
type block struct {
	rows []string
	meta []rowMeta
}

// add appends a row and how a copy reads it.
func (b *block) add(row string, m rowMeta) {
	b.rows = append(b.rows, row)
	b.meta = append(b.meta, m)
}

// plain appends rows a copy reads the default way.
func (b *block) plain(rows ...string) {
	for _, r := range rows {
		b.add(r, rowMeta{})
	}
}

// join appends the rows of o.
func (b *block) join(o block) {
	b.rows = append(b.rows, o.rows...)
	b.meta = append(b.meta, o.meta...)
}

// styled is the block with style applied to each row; styling adds no
// cells, so the meta holds.
func (b block) styled(style func(string) string) block {
	out := block{rows: make([]string, len(b.rows)), meta: b.meta}
	for i, r := range b.rows {
		out.rows[i] = style(r)
	}
	return out
}

// piece is one row of a wrapped line.
type piece struct {
	text  string
	cont  bool   // a row after the line's first
	joint string // what the wrap took out before it: the spaces it broke at, or "" inside a word
}

// wrapLine wraps one line of plain text, without line breaks: its first row
// takes first cells, the rows after it rest. A word wrap breaks at spaces,
// which it takes out of the rows, and inside a word only when the word is
// wider than a row; a hard wrap (code) breaks anywhere and keeps every
// space where it is. Wide characters count as two cells, and a character is
// never split.
func wrapLine(line string, first, rest int, hard bool) []piece {
	first, rest = max(1, first), max(1, rest)
	if ansi.StringWidth(line) <= first {
		return []piece{{text: line}}
	}
	w := wrapper{limit: first, rest: rest}
	if hard {
		for g := graphemes.FromString(line); g.Next(); {
			w.char(g.Value())
		}
		return w.done()
	}
	for _, tok := range tokens(line) {
		if tok[0] == ' ' && w.row.Len() > 0 {
			w.spaces = tok // kept for the next word, or for the joint where the row breaks
			continue
		}
		w.word(tok) // a word, or the line's leading indentation
	}
	return w.done()
}

// wrapper is the state of wrapLine.
type wrapper struct {
	out         []piece
	row         strings.Builder
	width       int    // cells on the row so far
	limit, rest int    // the current row's cells, and the next rows'
	spaces      string // the spaces before the next word, not placed yet
	joint       string // the joint of the row being built
}

// word places a word after the spaces before it, on a new row when it does
// not fit, breaking it when it is wider than a row.
func (w *wrapper) word(word string) {
	ww, sw := ansi.StringWidth(word), ansi.StringWidth(w.spaces)
	switch {
	case w.width+sw+ww <= w.limit:
		w.place(w.spaces+word, sw+ww)
		w.spaces = ""
		return
	case w.width > 0:
		w.breakRow(w.spaces)
		w.spaces = ""
		if ww <= w.limit {
			w.place(word, ww)
			return
		}
	}
	for g := graphemes.FromString(word); g.Next(); {
		w.char(g.Value())
	}
}

// char places one character, on a new row when the row is full.
func (w *wrapper) char(c string) {
	cw := ansi.StringWidth(c)
	if w.width > 0 && w.width+cw > w.limit {
		w.breakRow("")
	}
	w.place(c, cw)
}

// place puts text of n cells on the row.
func (w *wrapper) place(text string, n int) {
	w.row.WriteString(text)
	w.width += n
}

// breakRow ends the row; the next one starts after joint.
func (w *wrapper) breakRow(joint string) {
	w.out = append(w.out, piece{text: w.row.String(), cont: len(w.out) > 0, joint: w.joint})
	w.row.Reset()
	w.width, w.limit, w.joint = 0, w.rest, joint
}

// done is the rows, the last one included.
func (w *wrapper) done() []piece {
	if w.spaces != "" && w.width+ansi.StringWidth(w.spaces) <= w.limit {
		w.row.WriteString(w.spaces) // trailing spaces stay when they fit; a copy trims them anyway
	}
	if w.row.Len() > 0 || len(w.out) == 0 {
		w.out = append(w.out, piece{text: w.row.String(), cont: len(w.out) > 0, joint: w.joint})
	}
	return w.out
}

// tokens splits a line into runs of spaces and runs of anything else.
func tokens(line string) []string {
	var out []string
	start := 0
	for i := 1; i <= len(line); i++ {
		if i == len(line) || (line[i] == ' ') != (line[start] == ' ') {
			out = append(out, line[start:i])
			start = i
		}
	}
	return out
}

// wrapText wraps text to rows that start with the inset, which the caller
// puts in front: each of its lines on rows of their own, the very first
// row after lead and every other row after as many blanks, so the text
// hangs under itself. A copy reads the first row with lead, less its
// leading blanks, and joins each line's rows back into the line.
func wrapText(lead, text string, w int, hard bool) block {
	leadW := ansi.StringWidth(lead)
	pad := strings.Repeat(" ", leadW)
	limit := max(1, w-len(inset)-leadW)
	firstStart := len(inset) + leadW - ansi.StringWidth(strings.TrimLeft(lead, " "))
	var b block
	for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		for _, pc := range wrapLine(line, limit, limit, hard) {
			head, start := pad, len(inset)+leadW
			if len(b.rows) == 0 {
				head, start = lead, firstStart
			}
			row := head + pc.text
			b.add(row, rowMeta{start: start, end: len(inset) + ansi.StringWidth(row), cont: pc.cont, joint: pc.joint})
		}
	}
	return b
}
