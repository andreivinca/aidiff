package splitview

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	switcherWidth    = 72 // the popup at its widest, border included
	switcherMinWidth = 30 // a window narrower than this plus margins gets a popup as wide as itself
	switcherMinInner = 8  // narrower than this, the popup is one line at the bottom
	switcherMaxRows  = 12 // rows shown at most; more scroll
	switcherMinRows  = 3  // rows kept before the title, rule, legend and hints give way
	switcherPrefix   = 5  // " 1 ▸ ": the number and the mark of the session the popup opened on
	switcherFlags    = 4  // " ● $": an agent working, a terminal open
	switcherGap      = 2  // columns between the name, the machine and the path
	switcherMinPath  = 6  // a path column narrower than this is left out
	switcherMinName  = 8  // the name keeps this many columns before the glyphs or the machine do
)

// The switcher's glyphs, as its legend names them.
const (
	busyGlyph  = "●"
	shellGlyph = "$"
)

// switcherRow is one session as the switcher shows it.
type switcherRow struct {
	info   SessionInfo
	shells int  // its terminals still running
	origin bool // on screen when the popup opened
}

// switcherBox is the switcher drawn for the current window, with where its
// rows are, for the mouse.
type switcherBox struct {
	lines []string // the popup, border included; nil when the window is too small for one
	line  string   // the one-line stand-in of a window too small for the popup
	w     int      // the popup's width
	top   int      // the line of the first row shown, border included
	first int      // the row shown on line top
	shown int      // how many rows are shown
}

// describeSession is workspace key's row text, cleaned: what Sessions says
// of it, else its key.
func (m Model) describeSession(key string) SessionInfo {
	var info SessionInfo
	if m.cfg.Sessions != nil {
		info = m.cfg.Sessions.Describe(key)
	}
	info.Name, info.Machine, info.Path, info.Note = cleanLine(info.Name), cleanLine(info.Machine), cleanLine(info.Path), cleanLine(info.Note)
	if strings.TrimSpace(info.Name) == "" {
		info.Name = cleanLine(key)
	}
	if strings.TrimSpace(info.Name) == "" {
		info.Name = "unnamed"
	}
	return info
}

// shells counts workspace key's running terminals: its panes that are not
// a chat, as the quit dialog counts them.
func (m Model) shells(key string) int {
	terms := m.terms
	if key != m.wsKey {
		terms = m.parked[key].terms
	}
	n := 0
	for _, t := range terms {
		if _, chat := t.(quitNoter); !chat && !t.Exited() {
			n++
		}
	}
	return n
}

// switcherRows are the rows of the open switcher.
func (m Model) switcherRows() []switcherRow {
	s := m.switcher
	rows := make([]switcherRow, 0, len(s.keys))
	for _, k := range s.keys {
		r := switcherRow{info: m.describeSession(k), shells: m.shells(k), origin: k == s.origin}
		if r.origin && s.hold && r.info.Note == "" {
			r.info.Note = "closes when left"
		}
		rows = append(rows, r)
	}
	return rows
}

// switcherView draws the switcher over view.
func (m Model) switcherView(view string) string {
	m.switcher = m.synced() // a session may have closed since the last key
	if !m.switcher.open {
		return view
	}
	b := m.switcherBox()
	if b.lines == nil {
		return replaceLastLine(view, b.line, m.winW)
	}
	return overlayCenter(view, strings.Join(b.lines, "\n"), m.winW, m.height)
}

// switcherBox lays the switcher out for the window: the box narrows to the
// window, drops its legend, rule, title and hints, in that order, before
// showing fewer than switcherMinRows rows, and a window too small for any
// box gets one line at the bottom instead.
func (m Model) switcherBox() switcherBox {
	rows := m.switcherRows()
	s := m.switcher
	margin := boxMargin
	if m.winW-2*margin < switcherMinWidth {
		margin = 0
	}
	w := min(switcherWidth, m.winW-2*margin)
	inner, avail := w-boxBorder, m.height-2
	if inner < switcherMinInner || avail < 1 || len(rows) == 0 {
		return switcherBox{line: switcherLine(rows, s.sel), w: m.winW}
	}

	legend := switcherLegend(rows)
	showTitle, showRule, showLegend, showHint := true, true, legend != "", true
	extras := func() int {
		n := 0
		for _, on := range []bool{showTitle, showRule, showLegend, showHint} {
			if on {
				n++
			}
		}
		return n
	}
	want := min(len(rows), switcherMaxRows)
	for _, drop := range []*bool{&showLegend, &showRule, &showTitle, &showHint} {
		if min(want, switcherMinRows)+extras() <= avail {
			break
		}
		*drop = false
	}
	shown := min(want, max(1, avail-extras()))
	first := max(0, s.sel-shown+1)

	dim := m.styles.muted
	var lines []string
	if showTitle {
		title := " Switch session"
		if shown < len(rows) {
			title += "  " + strconv.Itoa(s.sel+1) + "/" + strconv.Itoa(len(rows))
		}
		lines = append(lines, lipgloss.NewStyle().Bold(true).Render(fitLine(title, inner)))
	}
	if showRule {
		lines = append(lines, dim.Render(strings.Repeat("─", inner)))
	}
	top := 1 + len(lines) // under the border
	cols := switcherLayout(rows, inner)
	for i := first; i < first+shown; i++ {
		lines = append(lines, cols.line(rows[i], i, i == s.sel, dim))
	}
	if showLegend {
		lines = append(lines, dim.Render(fitLine(" "+legend, inner)))
	}
	if showHint {
		lines = append(lines, dim.Render(fitLine(m.switcherHint(), inner)))
	}
	box := strings.Split(m.styles.box.Render(strings.Join(lines, "\n")), "\n")
	return switcherBox{lines: box, w: w, top: top, first: first, shown: shown}
}

// switcherHint says how to switch: by letting go of the modifier that
// opened the switcher where the terminal reports that, else with enter.
func (m Model) switcherHint() string {
	how := "enter to switch"
	if s := m.switcher; !s.hold && s.held != 0 && m.keys.active() {
		how = "release " + heldName(s.held) + " to switch"
	}
	return " ↓↑ move · " + how + " · esc stay"
}

// hit tells which row the window cell (x, y) is on, -1 for none, and
// whether it is inside the popup.
func (b switcherBox) hit(m Model, x, y int) (row int, inside bool) {
	if b.lines == nil {
		return -1, false
	}
	x0, y0 := boxOrigin(b.w, len(b.lines), m.winW, m.height)
	if x < x0 || x >= x0+b.w || y < y0 || y >= y0+len(b.lines) {
		return -1, false
	}
	if l := y - y0; l >= b.top && l < b.top+b.shown && x > x0 && x < x0+b.w-1 {
		return b.first + l - b.top, true
	}
	return -1, true
}

// switcherLine is the switcher in one line, for a window too small for the
// popup: the selection's number, name and machine.
func switcherLine(rows []switcherRow, sel int) string {
	if sel < 0 || sel >= len(rows) {
		return ""
	}
	r := rows[sel].info
	line := " " + strconv.Itoa(sel+1) + "/" + strconv.Itoa(len(rows)) + " " + r.Name
	if r.Machine != "" {
		line += " · " + r.Machine
	}
	return line + " "
}

// switcherLegend names the glyphs the rows show, "" when none does.
func switcherLegend(rows []switcherRow) string {
	busy, shell := false, false
	for _, r := range rows {
		busy = busy || r.info.Busy
		shell = shell || r.shells > 0
	}
	var parts []string
	if busy {
		parts = append(parts, busyGlyph+" agent working")
	}
	if shell {
		parts = append(parts, shellGlyph+" terminal open")
	}
	return strings.Join(parts, " · ")
}

// switcherCols are the widths of a row's columns, the same for every row.
type switcherCols struct {
	inner, name, machine, path int
	flags                      bool // the glyph columns are drawn
}

// switcherLayout fits the columns to inner. The name and the machine keep
// their width up to a share of the row; the path, which only tells two
// sessions of the same name apart, takes what is left, and is left out
// when that is too little. In a narrow popup the glyphs give way to the
// name.
func switcherLayout(rows []switcherRow, inner int) switcherCols {
	c := switcherCols{inner: inner}
	nameN, machN, pathN := 0, 0, 0
	for _, r := range rows {
		nameN = max(nameN, ansi.StringWidth(r.info.Name))
		machN = max(machN, ansi.StringWidth(r.info.Machine))
		pathN = max(pathN, pathCellWidth(r.info))
		c.flags = c.flags || r.info.Busy || r.shells > 0
	}
	rest := inner - switcherPrefix - 1 // a blank column at the end
	if c.flags && rest-switcherFlags < switcherMinName {
		c.flags = false
	}
	if c.flags {
		rest -= switcherFlags
	}
	if rest <= 0 {
		return c
	}
	c.name = min(nameN, rest, max(rest*45/100, switcherMinName))
	machine := 0 // the machine's columns, its gap included
	if c.machine = min(machN, max(rest*25/100, 6)); c.machine > 0 && c.name+switcherGap+c.machine <= rest {
		machine = switcherGap + c.machine
	} else {
		c.machine = 0
	}
	if room := rest - c.name - machine - switcherGap; pathN > 0 && room >= switcherMinPath {
		c.path = room
	}
	switch {
	case c.path > pathN: // room to spare: the name gets it, the path what is left
		c.name = min(nameN, c.name+c.path-pathN)
		c.path = rest - c.name - machine - switcherGap
	case c.path == 0:
		c.name = min(nameN, rest-machine)
	}
	return c
}

// pathCellWidth is the width a row's path cell asks for: its note, then its
// path.
func pathCellWidth(info SessionInfo) int {
	w := ansi.StringWidth(info.Note) + ansi.StringWidth(info.Path)
	if info.Note != "" && info.Path != "" {
		w += switcherGap
	}
	return w
}

// line draws row r, the list's row i: its number (1-9), the mark of the
// session the popup opened on, the name, the machine, the note and the dim
// path, cut from the left to fit, then its glyphs. The selected row is
// reversed, unstyled inside so that no reset ends the reverse early.
func (c switcherCols) line(r switcherRow, i int, selected bool, dim lipgloss.Style) string {
	num := " "
	if i < 9 {
		num = strconv.Itoa(i + 1)
	}
	mark := " "
	if r.origin {
		mark = "▸"
	}
	style := func(s string, st lipgloss.Style) string {
		if selected || s == "" {
			return s
		}
		return st.Render(s)
	}
	var b strings.Builder
	b.WriteString(" " + num + " " + mark + " ")
	b.WriteString(fitLine(ansi.Truncate(r.info.Name, c.name, "…"), c.name))
	used := switcherPrefix + c.name
	if c.machine > 0 {
		b.WriteString(strings.Repeat(" ", switcherGap) + fitLine(ansi.Truncate(r.info.Machine, c.machine, "…"), c.machine))
		used += switcherGap + c.machine
	}
	if c.path > 0 {
		note, path := pathCell(r.info, c.path)
		b.WriteString(strings.Repeat(" ", switcherGap) + note + style(path, dim))
		used += switcherGap + ansi.StringWidth(note) + ansi.StringWidth(path)
	}
	if c.flags {
		flagsAt := c.inner - switcherFlags - 1
		b.WriteString(strings.Repeat(" ", max(0, flagsAt-used)))
		busy, shell := " ", " "
		if r.info.Busy {
			busy = busyGlyph
		}
		if r.shells > 0 {
			shell = shellGlyph
		}
		b.WriteString(" " + busy + " " + shell)
	}
	line := fitLine(b.String(), c.inner)
	if selected {
		line = lipgloss.NewStyle().Reverse(true).Render(line)
	}
	return line
}

// pathCell splits w columns between a row's note, which is kept whole when
// it fits, and its path, cut from the left so the folders nearest to the
// session stay.
func pathCell(info SessionInfo, w int) (note, path string) {
	if info.Note != "" {
		note = ansi.Truncate(info.Note, w, "…")
		w -= ansi.StringWidth(note)
		if info.Path == "" || w <= switcherGap+1 {
			return note, ""
		}
		note += strings.Repeat(" ", switcherGap)
		w -= switcherGap
	}
	if w <= 1 {
		return note, ""
	}
	path = info.Path
	if over := ansi.StringWidth(path) - w; over > 0 {
		path = "…" + ansi.TruncateLeft(path, over+1, "")
	}
	return note, path
}
