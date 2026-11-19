package splitview

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The status line under the whole window shows which panel has focus: the
// columns under it turn green, so the mark follows focus and every resize.
// The green is the wrapped model's own when it has one (focusColorer), such
// as the review theme's added lines; else the fixed one below.

// The green of the focused panel's columns: truecolor, and the nearest
// 256-color palette entry for a line drawn with the palette. It is light
// enough for the line's dark text.
const (
	focusGreen        = "48;2;0;215;0" // #00d700
	focusGreenPalette = "48;5;40"      // the same green
)

// focusColorer is a wrapped model that colors the focus mark: bg and fg are
// select-graphic-rendition sequences, "" for none.
type focusColorer interface {
	FocusMarkColors() (bg, fg string)
}

// innerFocuser is a wrapped model with panes of its own, which tells the
// columns of the one that has focus, within its width.
type innerFocuser interface {
	FocusedColumns() (from, to int)
}

// focusedSpan is the window columns of the focused panel, [from, to): a
// sidebar, the wrapped model's focused pane, or a terminal. ok is false
// when no panel takes the columns, as in a window too narrow to lay out.
func (m Model) focusedSpan() (from, to int, ok bool) {
	base := m.leftCols()
	switch {
	case m.focus == focusOuter:
		from, to = 0, m.outerW()
	case m.focus == focusSidebar:
		from, to = m.outerCols(), m.outerCols()+m.sideW()
	case m.focus == focusDiff:
		from, to = base, base+m.leftW
		if f, isFocuser := m.inner.(innerFocuser); isFocuser {
			lo, hi := f.FocusedColumns()
			lo, hi = max(0, min(lo, m.leftW)), max(0, min(hi, m.leftW))
			if lo < hi {
				from, to = base+lo, base+hi
			}
		}
	case m.focus >= 0 && m.focus < len(m.terms):
		rects := m.termRects()
		if m.focus >= len(rects) {
			return 0, 0, false
		}
		r := rects[m.focus]
		from = base + m.leftW + r.x
		to = from + r.w
	default:
		return 0, 0, false
	}
	from, to = max(0, from), min(to, m.winW)
	return from, to, from < to
}

// withFocusMark draws line, the status line, with the focused panel's
// columns on green. A line with no background color gets those columns in
// reverse video instead.
func (m Model) withFocusMark(line string) string {
	from, to, ok := m.focusedSpan()
	if !ok {
		return line
	}
	mid := ansi.Cut(line, from, to) // each piece carries the escapes of the whole line
	mark := "\x1b[7m"
	bg, fg := m.focusColors(line)
	if bg != "" {
		mid = sgrSeq.ReplaceAllStringFunc(mid, func(seq string) string { return withColors(seq, bg, fg) })
		mark = "\x1b[" + strings.Trim(bg+";"+fg, ";") + "m"
	}
	return ansi.Cut(line, 0, from) + ansi.ResetStyle + mark + mid + ansi.ResetStyle + ansi.Cut(line, to, m.winW)
}

// sgrSeq matches a select-graphic-rendition escape sequence.
var sgrSeq = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// focusBackground is the green for line, in the color form its own
// background takes: truecolor, the palette, or "" when it sets none.
func focusBackground(line string) string {
	for _, seq := range sgrSeq.FindAllString(line, -1) {
		params := sgrParams(seq)
		for i := 0; i < len(params); i++ {
			switch params[i] {
			case "38", "58": // a foreground or underline color: skip its arguments
				i += colorArgs(params, i)
			case "48":
				if i+1 < len(params) && params[i+1] == "5" {
					return focusGreenPalette
				}
				return focusGreen
			}
		}
	}
	return ""
}

// focusColors are the mark's background and foreground parameters for
// line: the wrapped model's colors, else the fixed green on the line's own
// text color; bg is "" when the line sets no background to replace.
func (m Model) focusColors(line string) (bg, fg string) {
	fallback := focusBackground(line)
	if fallback == "" {
		return "", ""
	}
	if c, ok := m.inner.(focusColorer); ok {
		cbg, cfg := c.FocusMarkColors()
		if p := sgrBody(cbg); p != "" {
			return p, sgrBody(cfg)
		}
	}
	return fallback, ""
}

// sgrBody is the parameters of seq, "" for none or no sequence.
func sgrBody(seq string) string {
	if !sgrSeq.MatchString(seq) {
		return ""
	}
	return strings.Join(sgrParams(seq), ";")
}

// withColors rewrites seq so that what it draws keeps the background bg,
// and the foreground fg when it is set: a color it sets or resets becomes
// them, and a full reset is followed by them.
func withColors(seq, bg, fg string) string {
	in := sgrParams(seq)
	out := make([]string, 0, len(in)+2)
	for i := 0; i < len(in); i++ {
		switch p := in[i]; p {
		case "38":
			n := colorArgs(in, i)
			if fg != "" {
				out = append(out, fg)
			} else {
				out = append(out, in[i:min(len(in), i+n+1)]...)
			}
			i += n
		case "58":
			n := colorArgs(in, i)
			out = append(out, in[i:min(len(in), i+n+1)]...)
			i += n
		case "48":
			i += colorArgs(in, i)
			out = append(out, bg)
		case "49":
			out = append(out, bg)
		case "39":
			out = append(out, cmpOr(fg, p))
		case "", "0":
			out = append(out, p, bg)
			if fg != "" {
				out = append(out, fg)
			}
		default:
			out = append(out, p)
		}
	}
	return "\x1b[" + strings.Join(out, ";") + "m"
}

// cmpOr is s, or fallback when s is empty.
func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// colorArgs is how many parameters follow the extended color at params[i]:
// 2;r;g;b or 5;n.
func colorArgs(params []string, i int) int {
	if i+1 >= len(params) {
		return 0
	}
	switch params[i+1] {
	case "2":
		return min(4, len(params)-1-i)
	case "5":
		return min(2, len(params)-1-i)
	}
	return 0
}

// sgrParams are the parameters of seq; "\x1b[m" has one, empty.
func sgrParams(seq string) []string {
	return strings.Split(strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m"), ";")
}
