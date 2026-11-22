package splitview

import (
	"hash/fnv"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	tabGap     = 1 // the plain column between two tabs
	tabMinName = 6 // a name is shortened to no fewer columns before the strip scrolls
	tabMarkW   = 2 // a scroll mark and the gap beside it: "‹ " on the left, " ›" on the right
	tabCloseW  = 2 // the close button's cells: "×" and the blank after it
)

// The tab strip's glyphs.
const (
	tabCloseGlyph = "×"
	tabLeftMark   = "‹"
	tabRightMark  = "›"
)

// Each tab is drawn on its machine's color, so the tabs of one machine read
// as a group: the color is picked from machinePalette by a hash of the
// machine's name as the tab shows it, never by the tab's place, so it stays
// the same in every frame, as tabs open and close, across reconnects and
// from one run to the next. Two machines may land on the same color.
//
// The colors are entries of the xterm 256-color cube, so a terminal that
// maps truecolor to its palette lands on them exactly. Each sets its text
// color too, never the theme's. On the Linux console, which keeps the top
// bit of each channel of a background, every one keeps a color of its own
// family (blue, green, red, magenta, cyan, brown), and its text stays white
// or black. The tab on screen is a brighter shade of its machine's color,
// raising only the channels that already set that bit, so it stays in the
// same family there too, with bold, underlined text, whose color is the
// one, white or black, that reads better on that shade.

// machineColor is a machine's tab colors, as hex.
type machineColor struct {
	bg, fg             string // the tabs not on screen
	activeBg, activeFg string // the tab on screen
}

// machinePalette are the tab colors, picked by machine (machineColorOf).
// Each pair keeps at least 4.3:1 contrast (TestMachinePaletteContrast).
var machinePalette = [...]machineColor{
	{bg: "#005faf", fg: "#ffffff", activeBg: "#005fd7", activeFg: "#ffffff"}, // blue
	{bg: "#00875f", fg: "#ffffff", activeBg: "#00af5f", activeFg: "#000000"}, // green
	{bg: "#af5f5f", fg: "#ffffff", activeBg: "#d75f5f", activeFg: "#000000"}, // rose
	{bg: "#875faf", fg: "#ffffff", activeBg: "#af5fd7", activeFg: "#000000"}, // purple
	{bg: "#008787", fg: "#ffffff", activeBg: "#00afaf", activeFg: "#000000"}, // teal
	{bg: "#878700", fg: "#000000", activeBg: "#afaf00", activeFg: "#000000"}, // olive
	{bg: "#875f00", fg: "#ffffff", activeBg: "#af5f00", activeFg: "#ffffff"}, // brown
	{bg: "#5f5f87", fg: "#ffffff", activeBg: "#5f5faf", activeFg: "#ffffff"}, // slate
}

// machineColorOf is machine's tab colors: FNV-1a of its name picks them.
func machineColorOf(machine string) machineColor {
	h := fnv.New32a()
	_, _ = h.Write([]byte(machine))
	return machinePalette[h.Sum32()%uint32(len(machinePalette))]
}

// tabSGR is the select-graphic-rendition sequence a tab on machine's color
// starts with: the tab on screen brighter, bold and underlined. The colors
// go through lipgloss's color profile, the one the program draws with, so
// a profile of 256 or 16 colors gets the nearest of its own; one sequence
// for the whole tab, where a lipgloss style with an underline would style
// it a rune at a time. Without colors a tab is plain, the tab on screen in
// reverse video; "" for no sequence.
func tabSGR(machine string, current, noColors bool) string {
	switch {
	case noColors && current:
		return "\x1b[1;7m"
	case noColors:
		return ""
	}
	c := machineColorOf(machine)
	fg, bg, params := c.fg, c.bg, []string(nil)
	if current {
		fg, bg, params = c.activeFg, c.activeBg, []string{"1", "4"}
	}
	return colorSGR(params, fg, bg)
}

// colorSGR is the sequence setting params, foreground fg and background bg
// (hex) in lipgloss's color profile; "" when it sets nothing.
func colorSGR(params []string, fg, bg string) string {
	profile := lipgloss.ColorProfile()
	if seq := profile.Color(fg).Sequence(false); seq != "" {
		params = append(params, seq)
	}
	if seq := profile.Color(bg).Sequence(true); seq != "" {
		params = append(params, seq)
	}
	if len(params) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(params, ";") + "m"
}

// withSGR is text drawn with sgr, then reset; text as it is for no sgr.
func withSGR(sgr, text string) string {
	if sgr == "" {
		return text
	}
	return sgr + text + ansi.ResetStyle
}

// machineBadge is machine's name, text, on its tabs' color, as the
// switcher shows it beside a session (switcher_view.go).
func machineBadge(machine, text string) string {
	c := machineColorOf(machine)
	return withSGR(colorSGR(nil, c.fg, c.bg), text)
}

// tabSpec is what a tab shows, cleaned; the strip's cache is keyed by them.
type tabSpec struct {
	key           string
	name, machine string
	note          string // why it cannot come back as it was left, such as "disconnected"
	busy          bool   // an agent is working in it
	shells        int    // its terminals still running
	current       bool   // on screen
}

// tabZone is a cell range of the strip, [from, to) in window columns, and
// what a click there does.
type tabZone struct {
	from, to int
	key      string // the tab a click brings, or closes
	close    bool   // the tab's close button
}

// tabLine is the strip drawn for one window width: its line, and where a
// click lands.
type tabLine struct {
	line        string
	zones       []tabZone
	first, last int // the tabs shown, by index; the others are scrolled out
}

// tabCache keeps the strip's last layout, keyed by everything it is drawn
// from: the strip is drawn on every frame.
type tabCache struct {
	valid    bool
	specs    []tabSpec
	width    int
	noColors bool
	strip    tabLine
}

// hit is the zone under window column x; the close button wins over the
// tab it belongs to.
func (l tabLine) hit(x int) (tabZone, bool) {
	for _, z := range l.zones {
		if z.close && x >= z.from && x < z.to {
			return z, true
		}
	}
	for _, z := range l.zones {
		if x >= z.from && x < z.to {
			return z, true
		}
	}
	return tabZone{}, false
}

// tabSpecs are the tabs as they are now.
func (m Model) tabSpecs() []tabSpec {
	keys := m.tabKeys()
	specs := make([]tabSpec, len(keys))
	for i, k := range keys {
		info := m.sessionInfo(k, false) // every frame: nothing fetched for it
		specs[i] = tabSpec{key: k, name: info.Name, machine: info.Machine, note: info.Note, busy: info.Busy,
			shells: m.shells(k), current: k == m.wsKey}
	}
	return specs
}

// tabStrip is the strip for the window as it is. The view draws it and the
// mouse is hit-tested against it, so a click lands on what is drawn; it is
// laid out again only when a tab, its marks, the tab on screen or the width
// change.
func (m Model) tabStrip() tabLine {
	specs := m.tabSpecs()
	c := m.strip
	if c != nil && c.valid && c.width == m.winW && c.noColors == m.cfg.NoColors && slices.Equal(c.specs, specs) {
		return c.strip
	}
	strip := layoutTabs(specs, m.winW, m.cfg.NoColors, m.styles.muted)
	if c != nil {
		*c = tabCache{valid: true, specs: specs, width: m.winW, noColors: m.cfg.NoColors, strip: strip}
	}
	return strip
}

// tabText is tab s's text with its name cut to name: its number (1-9),
// name, machine, note and marks, then its close button, a blank on each
// side.
func tabText(s tabSpec, i int, name string) string {
	var b strings.Builder
	b.WriteString(" ")
	if i < 9 {
		b.WriteString(string(rune('1'+i)) + " ")
	}
	b.WriteString(name)
	for _, part := range []string{s.machine, s.note} {
		if strings.TrimSpace(part) != "" {
			b.WriteString(" · " + part)
		}
	}
	if s.busy {
		b.WriteString(" " + busyGlyph)
	}
	if s.shells > 0 {
		b.WriteString(" " + shellGlyph)
	}
	b.WriteString(" " + tabCloseGlyph + " ")
	return b.String()
}

// layoutTabs lays the tabs out across width columns. Names are shortened
// first, all to the same width, down to tabMinName (a shorter name keeps
// its width); when the tabs still do not fit, the strip shows the run of
// whole tabs around the one on screen, with ‹ and › where tabs are cut
// off. Widths are measured on the text before it is styled, so the zones
// match the cells drawn, wide characters included.
func layoutTabs(specs []tabSpec, width int, noColors bool, muted lipgloss.Style) tabLine {
	n := len(specs)
	if n == 0 || width <= 0 {
		return tabLine{line: strings.Repeat(" ", max(0, width))}
	}
	cur := max(0, slices.IndexFunc(specs, func(s tabSpec) bool { return s.current }))
	fixed, nameW := make([]int, n), make([]int, n)
	longest := 0
	for i, s := range specs {
		fixed[i] = ansi.StringWidth(tabText(s, i, ""))
		nameW[i] = ansi.StringWidth(s.name)
		longest = max(longest, nameW[i])
	}
	total := func(limit int) int {
		w := (n - 1) * tabGap
		for i := range n {
			w += fixed[i] + min(nameW[i], limit)
		}
		return w
	}
	limit := longest
	for limit > tabMinName && total(limit) > width {
		limit--
	}
	names, w := make([]string, n), make([]int, n)
	for i, s := range specs {
		names[i] = s.name
		if nameW[i] > limit {
			names[i] = ansi.Truncate(s.name, limit, "…")
		}
		w[i] = fixed[i] + ansi.StringWidth(names[i])
	}

	fits := func(first, last int) bool {
		used := (last - first) * tabGap
		for i := first; i <= last; i++ {
			used += w[i]
		}
		if first > 0 {
			used += tabMarkW
		}
		if last < n-1 {
			used += tabMarkW
		}
		return used <= width
	}
	first, last := 0, n-1
	if !fits(first, last) {
		for first < cur && !fits(first, cur) {
			first++
		}
		last = cur
		for last+1 < n && fits(first, last+1) {
			last++
		}
		if !fits(cur, cur) { // the tab on screen alone is too wide: its name gives way
			marks := 0
			if cur > 0 {
				marks += tabMarkW
			}
			if cur < n-1 {
				marks += tabMarkW
			}
			room := max(1, width-marks-fixed[cur])
			names[cur] = ansi.Truncate(specs[cur].name, room, "…")
			w[cur] = fixed[cur] + ansi.StringWidth(names[cur])
		}
	}

	gap, mark := " ", func(s string) string { return muted.Render(s) }
	if noColors {
		gap, mark = "│", func(s string) string { return s }
	}
	var b strings.Builder
	var zones []tabZone
	x := 0
	if first > 0 {
		b.WriteString(mark(tabLeftMark) + " ")
		zones = append(zones, tabZone{from: 0, to: tabMarkW, key: specs[first-1].key})
		x = tabMarkW
	}
	for i := first; i <= last; i++ {
		if i > first {
			b.WriteString(gap)
			x += tabGap
		}
		text := tabText(specs[i], i, names[i])
		b.WriteString(withSGR(tabSGR(specs[i].machine, specs[i].current, noColors), text))
		end := x + w[i]
		zones = append(zones, tabZone{from: x, to: end, key: specs[i].key},
			tabZone{from: end - tabCloseW, to: end, key: specs[i].key, close: true})
		x = end
	}
	if last < n-1 {
		b.WriteString(strings.Repeat(" ", max(0, width-1-x)) + mark(tabRightMark))
		zones = append(zones, tabZone{from: width - tabMarkW, to: width, key: specs[last+1].key})
		x = width
	}
	line := b.String()
	if x > width { // a tab too wide even with its name cut to one cell: cut the row, and its zones
		line = ansi.Truncate(line, width, "")
		for i := range zones {
			zones[i].to = min(zones[i].to, width)
		}
		zones = slices.DeleteFunc(zones, func(z tabZone) bool { return z.from >= z.to })
	}
	if pad := width - min(x, width); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return tabLine{line: line, zones: zones, first: first, last: last}
}
