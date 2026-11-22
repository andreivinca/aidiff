package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	textColor          = lipgloss.Color("#e4e4e4") // the bright neutral
	composerBackground = lipgloss.Color("#262626") // a quiet band, one step off the terminal's
	panelBackground    = lipgloss.Color("#262626") // the user's messages, the same band
	mutedColor         = lipgloss.Color("#7a7a7a") // hints, and a queued message, not sent yet
)

// styles are the pane's colors, neutral greys (Omarchy plain); plain when color
// is off. selected marks the input's selected text and atom its blocks (a
// pasted image's token); both keep their own
// background on the composer's.
type styles struct {
	text, muted, rail, panel, cursor, heading, code, warn, bad, bold lipgloss.Style
	selected, atom                                                   lipgloss.Style
	badge                                                            lipgloss.Style // Config.Badge on the header
	// queued and queuedStripe draw a queued message's panel: its text muted,
	// on the panel's background and, every other cell, a shade lighter
	queued, queuedStripe lipgloss.Style
}

// newStyles is the pane's palette, and the same palette on the composer's
// background. A style's reset ends any style around it, so every part of a
// composer row is drawn with the composer palette rather than wrapped in a
// background.
func newStyles(color bool) (base, composer styles) {
	if !color {
		plain := lipgloss.NewStyle()
		base = styles{text: plain, muted: plain, rail: plain, panel: plain, cursor: plain, heading: plain, code: plain,
			warn: plain, bad: plain, bold: plain, selected: plain, atom: plain, queued: plain, queuedStripe: plain,
			badge: lipgloss.NewStyle().Reverse(true)}
		return base, base
	}
	base = styles{
		// badge: a quiet label in the purple, never a filled pill
		badge: lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")),
		text:  lipgloss.NewStyle().Foreground(textColor),
		muted: lipgloss.NewStyle().Foreground(mutedColor),
		rail:  lipgloss.NewStyle().Foreground(lipgloss.Color("#5a5a5a")),
		panel: lipgloss.NewStyle().Foreground(textColor).Background(panelBackground),
		// queued: the text muted, as its state row, on the panel and a barely lighter stripe
		queued:       lipgloss.NewStyle().Foreground(mutedColor).Background(panelBackground),
		queuedStripe: lipgloss.NewStyle().Foreground(mutedColor).Background(lipgloss.Color("#2c2c2c")),
		cursor:       lipgloss.NewStyle().Reverse(true),
		// headings read as bold text, not a color of their own
		heading: lipgloss.NewStyle().Bold(true).Foreground(textColor),
		code:    lipgloss.NewStyle().Foreground(lipgloss.Color("#8cb4d8")),
		warn:    lipgloss.NewStyle().Foreground(lipgloss.Color("#d7b46a")),
		bad:     lipgloss.NewStyle().Foreground(lipgloss.Color("#e5787f")),
		bold:    lipgloss.NewStyle().Bold(true).Foreground(textColor),
		// selected: a neutral selection, the same on any background
		selected: lipgloss.NewStyle().Foreground(textColor).Background(lipgloss.Color("#3a3a3a")),
		// atom: a chip in the purple, apart from the selection's blue
		atom: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#bb9af7")).Background(lipgloss.Color("#2a2a2a")),
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
