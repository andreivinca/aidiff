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
	// Files, when set, searches files by name while the palette's query
	// does not start with '>' (the palette opens with it typed). It receives
	// the wrapped model and the query and returns the work that answers with
	// a FilesMsg for that query; nil when there is nothing to search.
	Files func(inner tea.Model, query string) tea.Cmd
	// Carry, when set, is asked whenever the wrapped model on screen is
	// replaced by another one of the same workspace (ReplaceInnerMsg, a
	// SwitchWorkspaceMsg for the workspace on screen) for the work that
	// carries over to the new model what the old one showed; its messages
	// go to the new model.
	Carry func(old, next tea.Model) tea.Cmd
	// Keys are the global keys; empty fields take the defaults.
	Keys Keys
	// Quit words the quit dialog.
	Quit QuitText
	// Colors are the split's own chrome colors; nil fields take the defaults.
	Colors Colors
	// NoColors draws the chrome that would color its text by machine (the
	// tab strip, the switcher's machine names) without colors.
	NoColors bool
	// TabBarBg is the select-graphic-rendition sequence that sets the tab
	// strip's background, such as the side pane's shade, so the strip reads
	// as its own band without a line under it; "" leaves the terminal's.
	TabBarBg string
	// Sidebar, when set, is a column left of the wrapped model that
	// Keys.Sidebar shows and hides.
	Sidebar Sidebar
	// OuterSidebar, when set, is a second column left of Sidebar, shown only
	// while it has focus: Keys.FocusLeft reaches it past the sidebar.
	OuterSidebar Sidebar
	// SidebarWidth is the sidebar's width (each sidebar's) in columns, its separator left out;
	// 30 by default.
	SidebarWidth int
	// Workspace names the workspace the split starts with; "" leaves it
	// unnamed, so it closes once another one is switched to (see
	// SwitchWorkspaceMsg).
	Workspace string
	// Tabs, when set, are the tabs the split starts with, in order:
	// Workspace among them, else appended, and each other one a workspace
	// not opened yet (unopened.go), which has a tab and a switcher row but no
	// wrapped model until it is first shown and Sessions.Open opens it.
	// Ignored without Sessions, which alone can open them.
	Tabs []string
	// Recent are workspaces of Tabs the switcher lists first after the one
	// on screen, most recently used first, as the run before used them.
	Recent []string
	// Opening starts Workspace not opened yet either, as the other Tabs: its
	// stand-in is on screen, saying how its opening goes, and Init asks
	// Sessions.Open for it, so the model New wraps is not used (it may be
	// nil). Ignored without Sessions or for the unnamed workspace.
	Opening bool
	// Sessions, when set, tells the session switcher (Keys.Switch) what each
	// workspace is and puts it on screen the way the embedding program's
	// own switches do. Without it the rows show the workspace keys and the
	// switcher switches by itself.
	Sessions Sessions
	// Output, when set, writes an escape sequence to the terminal the
	// program draws on, whole, in one write, while the program runs: the
	// switcher asks the terminal to report key releases through it
	// (keyreports.go). The program's renderer writes there too, so each
	// sequence must reach the terminal in one piece. Without it the
	// switcher never learns that a modifier was let go.
	Output func(seq string)
}

// Keys are the keys the split handles before anything reaches the wrapped
// model or a pane, in Bubble Tea's key notation.
type Keys struct {
	Focus     string // move focus right without wrapping; alt+right by default
	FocusLeft string // move focus left without wrapping; alt+left by default
	// FocusAlt and FocusLeftAlt do the same as Focus and FocusLeft; none by
	// default. The Linux console never delivers alt+left and alt+right: its
	// keymap switches virtual consoles with them.
	FocusAlt     string
	FocusLeftAlt string
	Quit         string // quit, asking first; ctrl+q by default
	Palette      string // open or close the command palette; ctrl+p by default
	Sidebar      string // show or hide Config.Sidebar; ctrl+b by default
	// PaletteAlt and SidebarAlt do the same as Palette and Sidebar; alt+p
	// and alt+b by default, for terminals that keep ctrl+p or ctrl+b
	PaletteAlt string
	SidebarAlt string
	// Switch opens the session switcher and, while it is open, selects the
	// next session; SwitchBack opens it on the last one and selects the one
	// before. ctrl+tab and ctrl+shift+tab by default, in ultraviolet's key
	// notation: they arrive only from a terminal that reports modified keys
	// (ReportModifiedKeys) and leaves the chord to the program.
	Switch     string
	SwitchBack string
	// SwitchAlt does what Switch does in every terminal, its shifted form
	// what SwitchBack does while the switcher is open; alt+s by default.
	SwitchAlt string
	// CloseTab closes the tab on screen (tabs.go), asking first when that
	// would lose something; alt+w by default.
	CloseTab string
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
	Badge  lipgloss.TerminalColor // a row's badge (Command.Badge), drawn on it; ANSI 5 by default
}

// withDefaults fills in what the embedding program left unset.
func (c Config) withDefaults() Config {
	if c.WidthPercent < minWidthPercent || c.WidthPercent > maxWidthPercent {
		c.WidthPercent = defaultWidthPercent
	}
	c.Keys = c.Keys.withDefaults()
	if c.SidebarWidth <= 0 {
		c.SidebarWidth = defaultSidebarWidth
	}
	if c.Quit.Title == "" {
		c.Quit.Title = "Quit?"
	}
	c.Colors = c.Colors.withDefaults()
	return c
}

func (k Keys) withDefaults() Keys {
	if k.Focus == "" {
		k.Focus = "alt+right"
	}
	if k.FocusLeft == "" {
		k.FocusLeft = "alt+left"
	}
	if k.Quit == "" {
		k.Quit = "ctrl+q"
	}
	if k.Palette == "" {
		k.Palette = "ctrl+p"
	}
	if k.Sidebar == "" {
		k.Sidebar = "ctrl+b"
	}
	if k.PaletteAlt == "" {
		k.PaletteAlt = "alt+p"
	}
	if k.SidebarAlt == "" {
		k.SidebarAlt = "alt+b"
	}
	if k.Switch == "" {
		k.Switch = "ctrl+tab"
	}
	if k.SwitchBack == "" {
		k.SwitchBack = "ctrl+shift+tab"
	}
	if k.SwitchAlt == "" {
		k.SwitchAlt = "alt+s"
	}
	if k.CloseTab == "" {
		k.CloseTab = "alt+w"
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
	if c.Badge == nil {
		c.Badge = lipgloss.Color("5")
	}
	return c
}

// styles are the lipgloss styles built from Colors once, in New.
type styles struct {
	muted      lipgloss.Style
	box        lipgloss.Style
	grip       lipgloss.Style
	gripActive lipgloss.Style
	badge      lipgloss.Style
}

func newStyles(c Colors) styles {
	return styles{
		muted:      lipgloss.NewStyle().Foreground(c.Muted),
		box:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c.Border),
		grip:       lipgloss.NewStyle().Foreground(c.Muted),
		gripActive: lipgloss.NewStyle().Foreground(c.Active),
		badge:      lipgloss.NewStyle().Background(c.Badge).Foreground(lipgloss.Color("15")),
	}
}
