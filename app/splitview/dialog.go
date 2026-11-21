package splitview

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The quit dialog and ConfirmMsg's popup are one dialog: a question, what
// confirming does, and a confirm and a Cancel button, Cancel selected first.

// dialog buttons
const (
	confirmButton = iota
	cancelButton
)

// dialogIndent is the blank column on each side of a dialog's text.
const dialogIndent = 1

// dialog is what a dialog shows.
type dialog struct {
	title   string
	details []string // paragraphs under the title, wrapped to the box
	confirm string   // the confirm button's label
	sel     int      // the selected button: confirmButton or cancelButton
}

// dialogAnswer is what a key did to a dialog.
type dialogAnswer int

const (
	dialogOpen dialogAnswer = iota // still asking
	dialogYes
	dialogNo
)

// dialogKey answers a dialog whose selected button is *sel: y, or any of
// yesKeys, confirms; n or esc cancels; ←→, tab and h/l move between the
// buttons; enter or space presses the selected one. Any other key leaves it
// open.
func dialogKey(msg tea.KeyMsg, sel *int, yesKeys ...string) dialogAnswer {
	k := msg.String()
	switch {
	case k == "y" || k == "Y" || slices.Contains(yesKeys, k):
		return dialogYes
	case k == "n" || k == "N" || k == "esc":
		return dialogNo
	}
	switch k {
	case "left", "right", "tab", "shift+tab", "h", "l":
		*sel = 1 - *sel
	case "enter", " ":
		if *sel == confirmButton {
			return dialogYes
		}
		return dialogNo
	}
	return dialogOpen
}

// dialogView renders d. Its text may come from the remote side, so it is
// cleaned of escape sequences first.
func (m Model) dialogView(d dialog) string {
	inner := m.boxInner(paletteWidth)
	indent := strings.Repeat(" ", dialogIndent)
	lines := []string{indent + cleanLine(d.title), ""}
	for _, detail := range d.details {
		for l := range strings.SplitSeq(ansi.Wordwrap(cleanParagraph(detail), inner-2*dialogIndent, ""), "\n") {
			lines = append(lines, indent+l)
		}
		lines = append(lines, "")
	}
	label := cleanLine(d.confirm)
	buttons := dialogButton(label, d.sel == confirmButton) + "  " + dialogButton("Cancel", d.sel == cancelButton)
	lines = append(lines,
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, buttons),
		"",
		m.styles.muted.Render(lipgloss.PlaceHorizontal(inner, lipgloss.Center, "y "+strings.ToLower(label)+" · n/esc cancel · ←→ enter")),
	)
	for i, l := range lines {
		lines[i] = fitLine(l, inner)
	}
	return m.styles.box.Render(strings.Join(lines, "\n"))
}

// dialogButton renders a button, reversed when selected.
func dialogButton(label string, selected bool) string {
	st := lipgloss.NewStyle().Padding(0, 2)
	if selected {
		st = st.Reverse(true)
	}
	return st.Render(label)
}
