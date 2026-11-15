package splitview

import (
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const paletteMaxRows = 10

// palette is the ctrl+p command popup.
type palette struct {
	open  bool
	query string
	sel   int
}

func newPalette() palette { return palette{open: true} }

// command is one palette entry.
type command struct {
	name string
	key  string // shortcut shown on the right, if any
	run  func(Model) (tea.Model, tea.Cmd)
}

// commands lists what the palette offers in the current state.
func (m Model) commands() []command {
	cmds := []command{{name: "Open Terminal", run: Model.openTerminal}}
	if len(m.terms) > 0 {
		cmds = append(cmds, command{name: "Close Terminal", run: Model.closeFocusedTerminal})
	}
	cmds = append(cmds, command{name: "Quit", key: QuitKey, run: func(m Model) (tea.Model, tea.Cmd) {
		return m.Update(quitRequestMsg{})
	}})
	return cmds
}

// closeFocusedTerminal closes the focused terminal, or the bottom one when the
// diff has focus.
func (m Model) closeFocusedTerminal() (tea.Model, tea.Cmd) {
	if len(m.terms) == 0 {
		return m, nil
	}
	i := m.focus
	if i == focusDiff {
		i = len(m.terms) - 1
	}
	return m.closeTerminal(i)
}

// paletteItems is the command list filtered by the typed query.
func (m Model) paletteItems() []command {
	var out []command
	for _, c := range m.commands() {
		if fuzzyMatch(m.palette.query, c.name) {
			out = append(out, c)
		}
	}
	return out
}

// fuzzyMatch reports whether every character of query appears in s in order,
// ignoring case and spaces ("opt" matches "Open Terminal").
func fuzzyMatch(query, s string) bool {
	s = strings.ToLower(s)
	i := 0
	for _, r := range strings.ToLower(query) {
		if unicode.IsSpace(r) {
			continue
		}
		j := strings.IndexRune(s[i:], r)
		if j < 0 {
			return false
		}
		i += j + len(string(r))
	}
	return true
}

func (m Model) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.paletteItems()
	switch msg.String() {
	case "esc", PaletteKey:
		m.palette = palette{}
		return m, nil
	case "enter":
		if m.palette.sel < len(items) {
			run := items[m.palette.sel].run
			m.palette = palette{}
			return run(m)
		}
		return m, nil
	case "up", "ctrl+k":
		m.palette.sel--
	case "down", "ctrl+n", "ctrl+j", "tab":
		m.palette.sel++
	case "backspace":
		if r := []rune(m.palette.query); len(r) > 0 {
			m.palette.query = string(r[:len(r)-1])
		}
		m.palette.sel = 0
	default:
		if (msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace) && !msg.Alt && !msg.Paste {
			m.palette.query += string(msg.Runes)
			if msg.Type == tea.KeySpace {
				m.palette.query += " "
			}
			m.palette.sel = 0
		}
	}
	n := len(m.paletteItems())
	switch {
	case n == 0:
		m.palette.sel = 0
	case m.palette.sel < 0:
		m.palette.sel = n - 1
	case m.palette.sel >= n:
		m.palette.sel = 0
	}
	return m, nil
}

// paletteView renders the popup box.
func (m Model) paletteView() string {
	w := min(56, max(20, m.width-4))
	inner := w - 2
	items := m.paletteItems()

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	lines := []string{
		fitLine(" > "+m.palette.query+"█", inner),
		dim.Render(strings.Repeat("─", inner)),
	}
	if len(items) == 0 {
		lines = append(lines, dim.Render(fitLine("  no matching commands", inner)))
	}
	start := max(0, m.palette.sel-paletteMaxRows+1)
	for i := start; i < len(items) && i < start+paletteMaxRows; i++ {
		c := items[i]
		row := "  " + c.name
		if c.key != "" {
			pad := max(1, inner-ansi.StringWidth(row)-len(c.key)-1)
			row += strings.Repeat(" ", pad) + c.key + " "
		}
		row = fitLine(row, inner)
		if i == m.palette.sel {
			row = lipgloss.NewStyle().Reverse(true).Render(row)
		}
		lines = append(lines, row)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")).
		Render(strings.Join(lines, "\n"))
}

// overlayCenter draws box over base, horizontally centered, a third of the
// way down.
func overlayCenter(base, box string, width, height int) string {
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")
	bw := 0
	for _, l := range boxLines {
		bw = max(bw, ansi.StringWidth(l))
	}
	x0 := max(0, (width-bw)/2)
	y0 := max(0, (height-len(boxLines))/3)
	for i, bl := range boxLines {
		y := y0 + i
		if y >= len(baseLines) {
			break
		}
		line := baseLines[y]
		left := fitLine(ansi.Cut(line, 0, x0), x0)
		right := ansi.Cut(line, x0+bw, width)
		baseLines[y] = left + ansi.ResetStyle + fitLine(bl, bw) + ansi.ResetStyle + right
	}
	return strings.Join(baseLines, "\n")
}

// quit dialog buttons
const (
	quitButton = iota
	cancelButton
)

func (m Model) handleQuitDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", QuitKey:
		m.confirmQuit = false
		return m, tea.Quit
	case "n", "N", "esc":
		m.confirmQuit = false
	case "left", "right", "tab", "shift+tab", "h", "l":
		m.quitSel = 1 - m.quitSel
	case "enter", " ":
		m.confirmQuit = false
		if m.quitSel == quitButton {
			return m, tea.Quit
		}
	}
	return m, nil // anything else keeps the dialog open
}

// quitDialogView renders the quit confirmation popup.
func (m Model) quitDialogView() string {
	running := 0
	for _, t := range m.terms {
		if !t.Exited() {
			running++
		}
	}
	detail := "Closes the terminal and everything running in it."
	if running > 1 {
		detail = "Closes " + strconv.Itoa(running) + " terminals and everything running in them."
	}
	inner := min(56, max(20, m.width-4)) - 2

	button := func(label string, selected bool) string {
		st := lipgloss.NewStyle().Padding(0, 2)
		if selected {
			st = st.Reverse(true)
		}
		return st.Render(label)
	}
	buttons := button("Quit", m.quitSel == quitButton) + "  " + button("Cancel", m.quitSel == cancelButton)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	lines := []string{" Quit aidiff?", ""}
	for _, l := range strings.Split(ansi.Wordwrap(detail, inner-2, ""), "\n") {
		lines = append(lines, " "+l)
	}
	lines = append(lines,
		"",
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, buttons),
		"",
		dim.Render(lipgloss.PlaceHorizontal(inner, lipgloss.Center, "y quit · n/esc cancel · ←→ enter")),
	)
	for i, l := range lines {
		lines[i] = fitLine(l, inner)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")).
		Render(strings.Join(lines, "\n"))
}
