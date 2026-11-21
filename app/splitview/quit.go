package splitview

import (
	"slices"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
)

// QuitGuard is the program option that makes every quit go through the split:
// a tea.QuitMsg the user has not confirmed becomes a quit request, which
// quits at once when no pane is open and asks first otherwise. Filtering the
// message itself, rather than the commands that produce it, covers quits from
// anywhere: the wrapped model, palette commands, batches and sequences. The
// filter also takes the switcher's keyboard flags back before Bubble Tea
// gives the terminal away (keyreports.go).
func QuitGuard() tea.ProgramOption {
	return tea.WithFilter(filterQuit)
}

// filterQuit is QuitGuard's filter. Bubble Tea runs it before it acts on a
// message, so a pop written here still reaches the alternate screen the
// flags were pushed on; the next Update pushes them again should the
// switcher still be open when the program comes back.
func filterQuit(model tea.Model, msg tea.Msg) tea.Msg {
	m, ok := model.(Model)
	if !ok {
		return msg
	}
	if _, quit := msg.(tea.QuitMsg); quit && !m.quitConfirmed {
		return quitRequestMsg{}
	}
	if handsOverTerminal(msg) {
		m.keys.pop()
	}
	return msg
}

// requestQuit quits when no pane is open and opens the quit dialog otherwise,
// so a stray quit never drops a shell or a chat.
func (m Model) requestQuit() (tea.Model, tea.Cmd) {
	for _, t := range m.allTerms() {
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

// handleQuitDialogKey answers the quit dialog (dialogKey); the quit key
// confirms too, so pressing it twice quits.
func (m Model) handleQuitDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch dialogKey(msg, &m.quitSel, m.cfg.Keys.Quit) {
	case dialogYes:
		return m.quit()
	case dialogNo:
		m.quitDialog = false
	case dialogOpen:
	}
	return m, nil
}

// quitDetails are the paragraphs saying what quitting closes: the shells,
// then each other pane in its own words.
func (m Model) quitDetails() []string {
	shells := 0
	var notes []string
	for _, t := range m.allTerms() {
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
	return m.dialogView(dialog{title: m.cfg.Quit.Title, details: m.quitDetails(), confirm: "Quit", sel: m.quitSel})
}
