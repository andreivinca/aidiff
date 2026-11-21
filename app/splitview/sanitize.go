package splitview

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Text the split draws for its callers (palette rows, popups, the flash, the
// sidebars) may come from the remote side: thread and project titles, folder
// names, file-search results, error strings. Written to the host terminal
// as it is, an escape sequence in it acts there: OSC 52 writes the
// clipboard, OSC 0 sets the window title. So such text is cleaned before it
// is drawn, and the split adds its own styling after that.

// cleanLine is s with escape sequences and control characters taken out;
// line breaks and tabs become spaces, so it stays on its one line.
func cleanLine(s string) string {
	return cleanText(s, false)
}

// cleanParagraph is cleanLine keeping line breaks, for text wrapped into
// lines.
func cleanParagraph(s string) string {
	return cleanText(s, true)
}

// cleanText drops every escape sequence, C0 and C1 control character and
// invalid byte from s; tabs and line breaks become spaces unless keepLines
// keeps the line breaks.
func cleanText(s string, keepLines bool) string {
	if isPlain(s) {
		return s
	}
	s = ansi.Strip(s) // whole sequences, so an OSC's payload goes with it
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		switch {
		case r == utf8.RuneError && size == 1: // an invalid byte, such as a raw C1 control
		case r == '\n' && keepLines:
			b.WriteByte('\n')
		case r == '\n', r == '\r', r == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(r): // C0, DEL and C1
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isPlain reports whether s is printable ASCII, which needs no cleaning.
func isPlain(s string) bool {
	for i := range len(s) {
		if c := s[i]; c < 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}

// sgrOnly matches a select-graphic-rendition sequence and nothing else.
var sgrOnly = regexp.MustCompile(`^\x1b\[[0-9;:]*m$`)

// cleanView is a view a caller styled itself, such as a sidebar's, made safe
// to draw: its colors (SGR sequences) and line breaks stay, every other
// escape sequence and control character goes.
func cleanView(s string) string {
	if isPlain(strings.ReplaceAll(s, "\n", "")) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	var state byte
	for len(s) > 0 {
		seq, _, n, next := ansi.DecodeSequence(s, state, nil)
		if n <= 0 { // never expected; drop what is left rather than loop
			break
		}
		state = next
		s = s[n:]
		if sgrOnly.MatchString(seq) {
			b.WriteString(seq)
			continue
		}
		b.WriteString(cleanParagraph(seq))
	}
	return b.String()
}
