package t3palette

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// The pickers' tones (neutral greys, Omarchy plain): plain text on the
// terminal's own background, brightness for focus, and a selection drawn as
// a quiet band with a light edge instead of a reversed block.
const (
	lookFg     = "#e4e4e4" // the selected row's text
	lookDim    = "#7a7a7a" // titles, hints, details
	lookSel    = "#2a2a2a" // the focused list's selection band
	lookAccent = "#c6c6c6" // the focused title, the selection edge, hint keys
)

// lookEdge is the glyph marking the focused list's selected row, in the
// accent, where the row's leading blank was.
const lookEdge = "▍"

// lookColors reports whether the terminal draws colors: without them the
// lists fall back to reverse video and underline.
func lookColors() bool {
	return lipgloss.ColorProfile() != termenv.Ascii
}

// rowStyle is a list row's style: the focused list's selected row on the
// selection band in the bright text, another list's selected row in the
// bright text alone; without colors, reverse video and underline.
func rowStyle(selected, focused bool) lipgloss.Style {
	s := lipgloss.NewStyle()
	switch {
	case !selected:
		return s
	case !lookColors() && focused:
		return s.Reverse(true)
	case !lookColors():
		return s.Underline(true)
	case focused:
		return s.Background(lipgloss.Color(lookSel)).Foreground(lipgloss.Color(lookFg))
	}
	return s.Foreground(lipgloss.Color(lookFg))
}

// edged draws label, which starts with a blank, in style: the focused list's
// selected row swaps that blank for the accent edge.
func edged(style lipgloss.Style, label string, selected, focused bool) string {
	if !selected || !focused || !lookColors() || !strings.HasPrefix(label, " ") {
		return style.Render(label)
	}
	edge := lipgloss.NewStyle().Background(lipgloss.Color(lookSel)).Foreground(lipgloss.Color(lookAccent)).Render(lookEdge)
	return edge + style.Render(label[1:])
}

// paneTitle is a list's title: lowercase and quiet, in the accent while the
// list has focus; bold without colors.
func paneTitle(title string, focused bool) string {
	if !lookColors() {
		return lipgloss.NewStyle().Bold(true).Render(" " + title)
	}
	tone := lookDim
	if focused {
		tone = lookAccent
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(tone)).Render(" " + strings.ToLower(title))
}

// hintRow draws one row of key help, " keys   what": the keys in the
// accent, the rest quiet; faint without colors. A blank row stays blank.
func hintRow(h string, width int) string {
	h = ansi.Truncate(h, width, "…")
	if strings.TrimSpace(h) == "" {
		return ""
	}
	if !lookColors() {
		return lipgloss.NewStyle().Faint(true).Render(h)
	}
	keys, what := h, ""
	if i := strings.Index(strings.TrimLeft(h, " "), "  "); i >= 0 {
		i += len(h) - len(strings.TrimLeft(h, " "))
		keys, what = h[:i], h[i:]
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(lookAccent)).Render(keys) +
		lipgloss.NewStyle().Foreground(lipgloss.Color(lookDim)).Render(what)
}

// detailStyle is a row's quiet trailing detail in style: dimmed by color,
// or faint without colors.
func detailStyle(style lipgloss.Style) lipgloss.Style {
	if !lookColors() {
		return style.Faint(true)
	}
	return style.Foreground(lipgloss.Color(lookDim))
}
