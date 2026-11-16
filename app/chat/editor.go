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
// between them. Moving and deleting go a whole cluster at a time. A selection
// runs from the anchor to the cursor; typing replaces it and deleting removes
// it. An atom, such as a pasted image's "[Image #1]", is one cluster too: the
// cursor never stops inside it and one delete removes it whole.
type editor struct {
	clusters  []string
	cur       int  // the cursor, in clusters from the start
	selecting bool // a selection runs from anchor to cur
	anchor    int
	atoms     []string // texts kept whole as one character
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
	e.clusters, e.cur, e.selecting, e.atoms = e.clusters[:0], 0, false, nil
}

// remove deletes characters [lo, hi) and forgets the atoms no longer in the
// text, so typing one again later gives plain characters.
func (e *editor) remove(lo, hi int) {
	e.clusters = slices.Delete(e.clusters, lo, hi)
	e.atoms = slices.DeleteFunc(e.atoms, func(a string) bool {
		return !slices.Contains(e.clusters, a)
	})
}

// isAtom reports whether the character c is an atom.
func (e *editor) isAtom(c string) bool {
	return slices.Contains(e.atoms, c)
}

// insertAtom puts text at the cursor as one character, in place of the
// selection.
func (e *editor) insertAtom(text string) {
	e.deleteSelection()
	e.atoms = append(e.atoms, text)
	e.insert(text)
}

// addAtom keeps text whole as one character from now on, in the text there
// already too.
func (e *editor) addAtom(text string) {
	e.atoms = append(e.atoms, text)
	cur := len(strings.Join(e.clusters[:e.cur], ""))
	e.set(e.text())
	e.cur = e.cursorAt(cur)
}

// selection is the selected range of clusters, [lo, hi), when there is one.
func (e *editor) selection() (lo, hi int, ok bool) {
	if !e.selecting || e.anchor == e.cur {
		return 0, 0, false
	}
	return min(e.anchor, e.cur), max(e.anchor, e.cur), true
}

// selected is the selected text, "" when nothing is selected.
func (e *editor) selected() string {
	lo, hi, ok := e.selection()
	if !ok {
		return ""
	}
	return strings.Join(e.clusters[lo:hi], "")
}

// deselect drops the selection, leaving the text and cursor as they are.
func (e *editor) deselect() {
	e.selecting = false
}

// moveTo puts the cursor at i. With extend the selection grows from where
// the cursor was, or from the selection's anchor; without, it is dropped.
func (e *editor) moveTo(i int, extend bool) {
	if extend && !e.selecting {
		e.selecting, e.anchor = true, e.cur
	}
	if !extend {
		e.selecting = false
	}
	e.cur = max(0, min(i, len(e.clusters)))
}

// deleteSelection removes the selected text and reports whether there was
// any.
func (e *editor) deleteSelection() bool {
	lo, hi, ok := e.selection()
	e.selecting = false
	if !ok {
		return false
	}
	e.remove(lo, hi)
	e.cur = lo
	return true
}

// insert puts s at the cursor, in place of the selection, and moves the
// cursor past it. The text is split into clusters again, since s may join a
// neighbor, as a combining accent joins the letter before it.
func (e *editor) insert(s string) {
	e.deleteSelection()
	head := strings.Join(e.clusters[:e.cur], "") + s
	e.set(head + strings.Join(e.clusters[e.cur:], ""))
	e.cur = e.cursorAt(len(head))
}

// set replaces the text, leaving the cursor where it is. Each atom in it
// becomes one cluster; the rest splits into grapheme clusters.
func (e *editor) set(text string) {
	e.clusters = e.clusters[:0]
	for text != "" {
		at, atom := e.nextAtom(text)
		g := graphemes.FromString(text[:at])
		for g.Next() {
			e.clusters = append(e.clusters, g.Value())
		}
		if atom == "" {
			return
		}
		e.clusters = append(e.clusters, atom)
		text = text[at+len(atom):]
	}
}

// nextAtom is the first atom in text and where it starts; ("", len(text))
// when there is none. Of atoms starting at the same place, the longest wins.
func (e *editor) nextAtom(text string) (at int, atom string) {
	at = len(text)
	for _, a := range e.atoms {
		if a == "" {
			continue
		}
		i := strings.Index(text, a)
		if i >= 0 && (i < at || i == at && len(a) > len(atom)) {
			at, atom = i, a
		}
	}
	return at, atom
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
	if e.deleteSelection() {
		return
	}
	if e.cur > 0 {
		e.remove(e.cur-1, e.cur)
		e.cur--
	}
}

func (e *editor) del() {
	if e.deleteSelection() {
		return
	}
	if e.cur < len(e.clusters) {
		e.remove(e.cur, e.cur+1)
	}
}

// left moves the cursor back one character, or to the start of the
// selection, which it drops.
func (e *editor) left() {
	if lo, _, ok := e.selection(); ok {
		e.moveTo(lo, false)
		return
	}
	e.moveTo(e.cur-1, false)
}

// right moves the cursor on one character, or to the end of the selection,
// which it drops.
func (e *editor) right() {
	if _, hi, ok := e.selection(); ok {
		e.moveTo(hi, false)
		return
	}
	e.moveTo(e.cur+1, false)
}

func (e *editor) home() {
	e.moveTo(0, false)
}

func (e *editor) end() {
	e.moveTo(len(e.clusters), false)
}

// killWord deletes back to the start of the word before the cursor, or
// deletes the selection.
func (e *editor) killWord() {
	if e.deleteSelection() {
		return
	}
	i := e.cur
	for i > 0 && e.space(i-1) {
		i--
	}
	for i > 0 && !e.space(i-1) {
		i--
	}
	e.remove(i, e.cur)
	e.cur = i
}

// deleteWordLeft deletes back to the start of the word before the cursor,
// the way ctrl+← moves, or deletes the selection.
func (e *editor) deleteWordLeft() {
	if e.deleteSelection() {
		return
	}
	i := e.wordLeft()
	e.remove(i, e.cur)
	e.cur = i
}

// deleteWordRight deletes on to the end of the word after the cursor, or
// deletes the selection.
func (e *editor) deleteWordRight() {
	if e.deleteSelection() {
		return
	}
	e.remove(e.cur, e.wordRight())
}

// charClass sorts characters for word moves: a word is a run of one class
// other than space.
type charClass int

const (
	classSpace charClass = iota
	classWord            // letters, digits and _
	classPunct           // anything else: punctuation, symbols, emoji
)

// class is the class of character i.
func (e *editor) class(i int) charClass {
	r, _ := utf8.DecodeRuneInString(e.clusters[i])
	switch {
	case unicode.IsSpace(r):
		return classSpace
	case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r):
		return classWord
	default:
		return classPunct
	}
}

// wordLeft is the start of the word before the cursor: back over spaces and
// line breaks, then over one run of word characters or of punctuation.
func (e *editor) wordLeft() int {
	i := e.cur
	for i > 0 && e.class(i-1) == classSpace {
		i--
	}
	if i == 0 {
		return 0
	}
	c := e.class(i - 1)
	for i > 0 && e.class(i-1) == c {
		i--
	}
	return i
}

// wordRight is the end of the word after the cursor: on over spaces and line
// breaks, then over one run of word characters or of punctuation.
func (e *editor) wordRight() int {
	i, n := e.cur, len(e.clusters)
	for i < n && e.class(i) == classSpace {
		i++
	}
	if i == n {
		return n
	}
	c := e.class(i)
	for i < n && e.class(i) == c {
		i++
	}
	return i
}

// space reports whether character i is white space.
func (e *editor) space(i int) bool {
	r, _ := utf8.DecodeRuneInString(e.clusters[i])
	return unicode.IsSpace(r)
}

// wrapped is the text broken into rows of at most w cells, at line breaks
// and where a row is full, with the cursor's row and column. One cell is kept
// free for the block cursor at the end of a full row. starts holds the index
// of each row's first character in the text, so a row's characters can be
// matched to the selection.
type wrapped struct {
	rows           [][]string
	starts         []int
	curRow, curCol int
}

// wrap lays the text out in rows w cells wide.
func (e *editor) wrap(w int) wrapped {
	var out wrapped
	var row []string
	width, start := 0, 0
	breakRow := func(next int) {
		out.rows, out.starts = append(out.rows, row), append(out.starts, start)
		row, width, start = nil, 0, next
	}
	for i, c := range e.clusters {
		if i == e.cur {
			out.curRow, out.curCol = len(out.rows), len(row)
		}
		if c == "\n" {
			breakRow(i + 1)
			continue
		}
		cw := ansi.StringWidth(c)
		if width > 0 && width+cw > max(1, w-1) {
			breakRow(i)
			if i == e.cur {
				out.curRow, out.curCol = len(out.rows), 0
			}
		}
		row = append(row, c)
		width += cw
	}
	if e.atEnd() {
		if width >= w {
			breakRow(len(e.clusters))
		}
		out.curRow, out.curCol = len(out.rows), len(row)
	}
	breakRow(0)
	return out
}

// vertical moves the cursor dir rows (-1 up, 1 down) through the text laid
// out w cells wide, keeping its column on screen as far as the row reaches,
// and reports whether there was a row to move to. With extend the selection
// grows, as with shift.
func (e *editor) vertical(dir, w int, extend bool) bool {
	text := e.wrap(w)
	target := text.curRow + dir
	if target < 0 || target >= len(text.rows) {
		return false
	}
	cell := ansi.StringWidth(strings.Join(text.rows[text.curRow][:min(text.curCol, len(text.rows[text.curRow]))], ""))
	row := text.rows[target]
	last := len(row)
	if target+1 < len(text.rows) && text.starts[target+1] == text.starts[target]+len(row) {
		last = max(0, last-1) // the end of a row the text wraps out of is the next row's start
	}
	col, width := 0, 0
	for col < last {
		cw := ansi.StringWidth(row[col])
		if width+cw > cell {
			break
		}
		width += cw
		col++
	}
	e.moveTo(text.starts[target]+col, extend)
	return true
}
