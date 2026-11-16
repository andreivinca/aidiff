package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	textColor          = lipgloss.Color("#eeeeee")
	composerBackground = lipgloss.Color("#202020")
)

// styles are the pane's colors, OpenCode's default palette; plain when color
// is off. selected marks the input's selected text and atom its blocks (a
// pasted image's token, a /resume a stop offers); both keep their own
// background on the composer's.
type styles struct {
	text, muted, rail, panel, cursor, heading, code, warn, bad, bold lipgloss.Style
	selected, atom                                                   lipgloss.Style
}

// newStyles is the pane's palette, and the same palette on the composer's
// background. A style's reset ends any style around it, so every part of a
// composer row is drawn with the composer palette rather than wrapped in a
// background.
func newStyles(color bool) (base, composer styles) {
	if !color {
		plain := lipgloss.NewStyle()
		base = styles{text: plain, muted: plain, rail: plain, panel: plain, cursor: plain, heading: plain, code: plain,
			warn: plain, bad: plain, bold: plain, selected: plain, atom: plain}
		return base, base
	}
	base = styles{
		text:    lipgloss.NewStyle().Foreground(textColor),
		muted:   lipgloss.NewStyle().Foreground(lipgloss.Color("#808080")),
		rail:    lipgloss.NewStyle().Foreground(lipgloss.Color("#5c9cf5")),
		panel:   lipgloss.NewStyle().Foreground(textColor).Background(lipgloss.Color("#141414")),
		cursor:  lipgloss.NewStyle().Reverse(true),
		heading: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#9d7cd8")),
		code:    lipgloss.NewStyle().Foreground(lipgloss.Color("#7fd88f")),
		warn:    lipgloss.NewStyle().Foreground(lipgloss.Color("#f5a742")),
		bad:     lipgloss.NewStyle().Foreground(lipgloss.Color("#e06c75")),
		bold:    lipgloss.NewStyle().Bold(true),
		// selected: OpenCode's selection color, the same on any background
		selected: lipgloss.NewStyle().Foreground(textColor).Background(lipgloss.Color("#3a4a6b")),
		// atom: a chip in the heading's purple, apart from the selection's blue
		atom: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#d4c2ff")).Background(lipgloss.Color("#3d3160")),
	}
	return base, base.on(composerBackground)
}

// on is the palette on the background bg. The cursor and bold, which have
// no color of their own, take the text's.
func (s styles) on(bg lipgloss.Color) styles {
	s.cursor = s.cursor.Foreground(textColor)
	s.bold = s.bold.Foreground(textColor)
	for _, st := range []*lipgloss.Style{&s.text, &s.muted, &s.rail, &s.panel, &s.cursor, &s.heading, &s.code, &s.warn, &s.bad, &s.bold} {
		*st = st.Background(bg)
	}
	return s
}

// fill fits a row made of the palette's parts to exactly w cells, padding it
// on the palette's background.
func (s styles) fill(row string, w int) string {
	if ansi.StringWidth(row) > w {
		row = ansi.Truncate(row, w, "")
	}
	if pad := w - ansi.StringWidth(row); pad > 0 {
		row += s.text.Render(strings.Repeat(" ", pad))
	}
	return row
}
