package chat

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/clipperhouse/uax29/v2/graphemes"
)

// editor is the input: its text as grapheme clusters, the characters a user
// sees (an emoji sequence, a flag, a letter with its accents), and a cursor
// between them. Moving and deleting go a whole cluster at a time.
type editor struct {
	clusters []string
	cur      int // the cursor, in clusters from the start
}

func (e *editor) text() string {
	return strings.Join(e.clusters, "")
}

func (e *editor) empty() bool {
	return len(e.clusters) == 0
}

func (e *editor) atEnd() bool {
	return e.cur == len(e.clusters)
}

// before is the character before the cursor, or "" at the start.
func (e *editor) before() string {
	if e.cur == 0 {
		return ""
	}
	return e.clusters[e.cur-1]
}

func (e *editor) clear() {
	e.clusters, e.cur = e.clusters[:0], 0
}

// insert puts s at the cursor and moves the cursor past it. The text is
// split into clusters again, since s may join a neighbor, as a combining
// accent joins the letter before it.
func (e *editor) insert(s string) {
	head := strings.Join(e.clusters[:e.cur], "") + s
	e.set(head + strings.Join(e.clusters[e.cur:], ""))
	e.cur = e.cursorAt(len(head))
}

// set replaces the text, leaving the cursor where it is.
func (e *editor) set(text string) {
	e.clusters = e.clusters[:0]
	g := graphemes.FromString(text)
	for g.Next() {
		e.clusters = append(e.clusters, g.Value())
	}
}

// cursorAt is the cursor after byte n of the text: past every cluster that
// starts before it.
func (e *editor) cursorAt(n int) int {
	start := 0
	for i, c := range e.clusters {
		if start >= n {
			return i
		}
		start += len(c)
	}
	return len(e.clusters)
}

func (e *editor) backspace() {
	if e.cur > 0 {
		e.clusters = slices.Delete(e.clusters, e.cur-1, e.cur)
		e.cur--
	}
}

func (e *editor) del() {
	if e.cur < len(e.clusters) {
		e.clusters = slices.Delete(e.clusters, e.cur, e.cur+1)
	}
}

func (e *editor) left() {
	if e.cur > 0 {
		e.cur--
	}
}

func (e *editor) right() {
	if e.cur < len(e.clusters) {
		e.cur++
	}
}

func (e *editor) home() {
	e.cur = 0
}

func (e *editor) end() {
	e.cur = len(e.clusters)
}

// killWord deletes back to the start of the word before the cursor.
func (e *editor) killWord() {
	i := e.cur
	for i > 0 && e.space(i-1) {
		i--
	}
	for i > 0 && !e.space(i-1) {
		i--
	}
	e.clusters = slices.Delete(e.clusters, i, e.cur)
	e.cur = i
}

// space reports whether character i is white space.
func (e *editor) space(i int) bool {
	r, _ := utf8.DecodeRuneInString(e.clusters[i])
	return unicode.IsSpace(r)
}

// wrapped is the text broken into rows of at most w cells, at line breaks
// and where a row is full, with the cursor's row and column. One cell is kept
// free for the block cursor at the end of a full row.
type wrapped struct {
	rows           [][]string
	curRow, curCol int
}

// wrap lays the text out in rows w cells wide.
func (e *editor) wrap(w int) wrapped {
	var out wrapped
	var row []string
	width := 0
	for i, c := range e.clusters {
		if i == e.cur {
			out.curRow, out.curCol = len(out.rows), len(row)
		}
		if c == "\n" {
			out.rows = append(out.rows, row)
			row, width = nil, 0
			continue
		}
		cw := ansi.StringWidth(c)
		if width > 0 && width+cw > max(1, w-1) {
			out.rows = append(out.rows, row)
			row, width = nil, 0
			if i == e.cur {
				out.curRow, out.curCol = len(out.rows), 0
			}
		}
		row = append(row, c)
		width += cw
	}
	if e.atEnd() {
		if width >= w {
			out.rows = append(out.rows, row)
			row = nil
		}
		out.curRow, out.curCol = len(out.rows), len(row)
	}
	out.rows = append(out.rows, row)
	return out
}
