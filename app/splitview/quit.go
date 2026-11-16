package splitview

import (
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// quit dialog buttons
const (
	quitButton = iota
	cancelButton
)

// dialogIndent is the blank column on each side of the quit dialog's text.
const dialogIndent = 1

// QuitGuard is the program option that makes every quit go through the split:
// a tea.QuitMsg the user has not confirmed becomes a quit request, which
// quits at once when no pane is open and asks first otherwise. Filtering the
// message itself, rather than the commands that produce it, covers quits from
// anywhere: the wrapped model, palette commands, batches and sequences.
func QuitGuard() tea.ProgramOption {
	return tea.WithFilter(filterQuit)
}

// filterQuit is QuitGuard's filter.
func filterQuit(model tea.Model, msg tea.Msg) tea.Msg {
	if _, ok := msg.(tea.QuitMsg); !ok {
		return msg
	}
	if m, ok := model.(Model); ok && !m.quitConfirmed {
		return quitRequestMsg{}
	}
	return msg
}

// requestQuit quits when no pane is open and opens the quit dialog otherwise,
// so a stray quit never drops a shell or a chat.
func (m Model) requestQuit() (tea.Model, tea.Cmd) {
	for _, t := range m.terms {
		if !t.Exited() {
			return m.askQuit()
		}
	}
	return m.quit()
}

// askQuit opens the quit dialog whatever is open: the quit key is deliberate,
// and a confirmation costs one key.
func (m Model) askQuit() (tea.Model, tea.Cmd) {
	m.quitDialog, m.quitSel = true, cancelButton
	return m, nil
}

// quit ends the program; QuitGuard lets this quit through.
func (m Model) quit() (tea.Model, tea.Cmd) {
	m.quitDialog = false
	m.quitConfirmed = true
	return m, tea.Quit
}

func (m Model) handleQuitDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", m.cfg.Keys.Quit:
		return m.quit()
	case "n", "N", "esc":
		m.quitDialog = false
	case "left", "right", "tab", "shift+tab", "h", "l":
		m.quitSel = 1 - m.quitSel
	case "enter", " ":
		if m.quitSel == quitButton {
			return m.quit()
		}
		m.quitDialog = false
	}
	return m, nil // anything else keeps the dialog open
}

// quitDetails are the paragraphs saying what quitting closes: the shells,
// then each other pane in its own words.
func (m Model) quitDetails() []string {
	shells := 0
	var notes []string
	for _, t := range m.terms {
		if t.Exited() {
			continue
		}
		noter, ok := t.(quitNoter)
		if !ok {
			shells++
			continue
		}
		if note := noter.QuitNote(); !slices.Contains(notes, note) {
			notes = append(notes, note)
		}
	}
	var details []string
	switch {
	case shells == 1:
		details = append(details, "Closes the terminal and everything running in it.")
	case shells > 1:
		details = append(details, "Closes "+strconv.Itoa(shells)+" terminals and everything running in them.")
	}
	details = append(details, notes...)
	if len(details) == 0 && m.cfg.Quit.Idle != "" {
		details = append(details, m.cfg.Quit.Idle)
	}
	return details
}

// quitDialogView renders the quit confirmation popup.
func (m Model) quitDialogView() string {
	inner := m.boxInner(paletteWidth)
	indent := strings.Repeat(" ", dialogIndent)
	lines := []string{indent + m.cfg.Quit.Title, ""}
	for _, detail := range m.quitDetails() {
		for l := range strings.SplitSeq(ansi.Wordwrap(detail, inner-2*dialogIndent, ""), "\n") {
			lines = append(lines, indent+l)
		}
		lines = append(lines, "")
	}
	buttons := quitDialogButton("Quit", m.quitSel == quitButton) + "  " + quitDialogButton("Cancel", m.quitSel == cancelButton)
	lines = append(lines,
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, buttons),
		"",
		m.styles.muted.Render(lipgloss.PlaceHorizontal(inner, lipgloss.Center, "y quit · n/esc cancel · ←→ enter")),
	)
	for i, l := range lines {
		lines[i] = fitLine(l, inner)
	}
	return m.styles.box.Render(strings.Join(lines, "\n"))
}

// quitDialogButton renders a button, reversed when selected.
func quitDialogButton(label string, selected bool) string {
	st := lipgloss.NewStyle().Padding(0, 2)
	if selected {
		st = st.Reverse(true)
	}
	return st.Render(label)
}
