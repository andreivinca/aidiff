package splitview

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	defaultWidthPercent = 45
	minWidthPercent     = 10
	maxWidthPercent     = 90
)

// Config configures the split.
type Config struct {
	// Start launches a shell pane (Open Terminal, OpenOnStart).
	Start StartFunc
	// WidthPercent is the right column's share of the window (10-90).
	WidthPercent int
	// OpenOnStart opens one terminal as soon as the window size is known.
	OpenOnStart bool
	// RefreshMsg is forwarded to the wrapped model when files change (see
	// FilesChangedMsg). Nil disables refreshing.
	RefreshMsg tea.Msg
	// Commands adds entries to the palette. It is asked again whenever the
	// palette opens, its filter changes, or the wrapped model handles a
	// message while it is open, so the list follows the program's state.
	Commands func() []Command
	// Keys are the global keys; empty fields take the defaults.
	Keys Keys
	// Quit words the quit dialog.
	Quit QuitText
	// Colors are the split's own chrome colors; nil fields take the defaults.
	Colors Colors
}

// Keys are the keys the split handles before anything reaches the wrapped
// model or a pane, in Bubble Tea's key notation.
type Keys struct {
	Focus   string // cycle focus: wrapped model → pane 1 → … → wrapped model; ctrl+] by default
	Quit    string // quit, asking first; ctrl+q by default
	Palette string // open or close the command palette; ctrl+p by default
}

// QuitText is what the quit dialog says.
type QuitText struct {
	Title string // the question; "Quit?" by default
	Idle  string // what quitting means when no pane is open; nothing by default
}

// Colors are the colors of the split's own chrome.
type Colors struct {
	Muted  lipgloss.TerminalColor // hints, row details, idle dividers; ANSI 8 by default
	Border lipgloss.TerminalColor // popup borders; ANSI 6 by default
	Active lipgloss.TerminalColor // the divider being dragged; ANSI 12 by default
}

// withDefaults fills in what the embedding program left unset.
func (c Config) withDefaults() Config {
	if c.WidthPercent < minWidthPercent || c.WidthPercent > maxWidthPercent {
		c.WidthPercent = defaultWidthPercent
	}
	c.Keys = c.Keys.withDefaults()
	if c.Quit.Title == "" {
		c.Quit.Title = "Quit?"
	}
	c.Colors = c.Colors.withDefaults()
	return c
}

func (k Keys) withDefaults() Keys {
	if k.Focus == "" {
		k.Focus = "ctrl+]"
	}
	if k.Quit == "" {
		k.Quit = "ctrl+q"
	}
	if k.Palette == "" {
		k.Palette = "ctrl+p"
	}
	return k
}

func (c Colors) withDefaults() Colors {
	if c.Muted == nil {
		c.Muted = lipgloss.Color("8")
	}
	if c.Border == nil {
		c.Border = lipgloss.Color("6")
	}
	if c.Active == nil {
		c.Active = lipgloss.Color("12")
	}
	return c
}

// styles are the lipgloss styles built from Colors once, in New.
type styles struct {
	muted      lipgloss.Style
	box        lipgloss.Style
	grip       lipgloss.Style
	gripActive lipgloss.Style
}

func newStyles(c Colors) styles {
	return styles{
		muted:      lipgloss.NewStyle().Foreground(c.Muted),
		box:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c.Border),
		grip:       lipgloss.NewStyle().Foreground(c.Muted),
		gripActive: lipgloss.NewStyle().Foreground(c.Active),
	}
}
