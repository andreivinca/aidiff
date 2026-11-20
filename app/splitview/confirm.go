package splitview

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ConfirmMsg opens a confirmation popup over the split, as the quit dialog
// is: Yes runs when the user confirms (y, or enter on the confirm button),
// nothing when they cancel (n, esc, or enter on Cancel, which is selected
// first). Another one replaces an open one.
type ConfirmMsg struct {
	Title   string  // the question, such as "Revert a.go?"
	Detail  string  // what confirming does, wrapped under the title; may be empty
	Confirm string  // the confirm button's label; "OK" by default
	Yes     tea.Cmd // what confirming runs
}

// confirmState is the open confirmation popup.
type confirmState struct {
	msg ConfirmMsg
	sel int // quitButton (the confirm button) or cancelButton
}

// showConfirm opens the popup msg asks for.
func (m Model) showConfirm(msg ConfirmMsg) (tea.Model, tea.Cmd) {
	if msg.Confirm == "" {
		msg.Confirm = "OK"
	}
	m.confirm = &confirmState{msg: msg, sel: cancelButton}
	return m, nil
}

// modal reports whether a popup takes the keys and swallows the mouse.
func (m Model) modal() bool {
	return m.palette.open || m.quitDialog || m.confirm != nil
}

// handleConfirmKey answers the popup.
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.confirm
	switch msg.String() {
	case "y", "Y":
		m.confirm = nil
		return m, c.msg.Yes
	case "n", "N", "esc", "q":
		m.confirm = nil
	case "left", "right", "tab", "shift+tab", "h", "l":
		m.confirm = &confirmState{msg: c.msg, sel: 1 - c.sel}
	case "enter", " ":
		m.confirm = nil
		if c.sel == quitButton {
			return m, c.msg.Yes
		}
	}
	return m, nil // anything else keeps the popup open
}

// confirmView renders the popup.
func (m Model) confirmView() string {
	c := m.confirm
	inner := m.boxInner(paletteWidth)
	indent := strings.Repeat(" ", dialogIndent)
	lines := []string{indent + c.msg.Title, ""}
	if c.msg.Detail != "" {
		for l := range strings.SplitSeq(ansi.Wordwrap(c.msg.Detail, inner-2*dialogIndent, ""), "\n") {
			lines = append(lines, indent+l)
		}
		lines = append(lines, "")
	}
	buttons := quitDialogButton(c.msg.Confirm, c.sel == quitButton) + "  " + quitDialogButton("Cancel", c.sel == cancelButton)
	lines = append(lines,
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, buttons),
		"",
		m.styles.muted.Render(lipgloss.PlaceHorizontal(inner, lipgloss.Center, "y "+strings.ToLower(c.msg.Confirm)+" · n/esc cancel · ←→ enter")),
	)
	for i, l := range lines {
		lines[i] = fitLine(l, inner)
	}
	return m.styles.box.Render(strings.Join(lines, "\n"))
}
