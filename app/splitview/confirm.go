package splitview

import (
	tea "github.com/charmbracelet/bubbletea"
)

// ConfirmMsg opens a confirmation popup over the split, as the quit dialog
// is, with its keys: Yes runs when the user confirms (y, or enter on the
// confirm button), No when the popup goes without a yes: the user cancels
// (n, esc, or enter on Cancel, which is selected first), another ConfirmMsg
// replaces it, or it is dropped unseen because the model that asked is no
// longer on screen. Both are tagged like the asking model's own commands.
type ConfirmMsg struct {
	Title   string  // the question, such as "Revert a.go?"
	Detail  string  // what confirming does, wrapped under the title; may be empty
	Confirm string  // the confirm button's label; "OK" by default
	Yes     tea.Cmd // what confirming runs
	No      tea.Cmd // what any other end of the popup runs; may be nil
}

// confirmState is the open confirmation popup.
type confirmState struct {
	msg ConfirmMsg
	sel int // confirmButton or cancelButton
	ws  int // the wrapped model on screen when it opened: Yes is scoped to it
}

// showConfirm opens the popup msg asks for; one open already is answered
// no.
func (m Model) showConfirm(msg ConfirmMsg) (tea.Model, tea.Cmd) {
	var replaced tea.Cmd
	if c := m.confirm; c != nil {
		replaced = scopeCmd(c.ws, c.msg.No)
	}
	if msg.Confirm == "" {
		msg.Confirm = "OK"
	}
	m.confirm = &confirmState{msg: msg, sel: cancelButton, ws: m.wsID}
	return m, replaced
}

// modal reports whether a popup takes the keys and swallows the mouse.
func (m Model) modal() bool {
	return m.palette.open || m.quitDialog || m.confirm != nil || m.switcher.open
}

// handleConfirmKey answers the popup (dialogKey).
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := *m.confirm // a copy: older Models share the pointer
	switch dialogKey(msg, &c.sel) {
	case dialogYes:
		m.confirm = nil
		return m, scopeCmd(c.ws, c.msg.Yes)
	case dialogNo:
		m.confirm = nil
		return m, scopeCmd(c.ws, c.msg.No)
	case dialogOpen:
		m.confirm = &c
	}
	return m, nil
}

// confirmView renders the popup.
func (m Model) confirmView() string {
	c := m.confirm
	d := dialog{title: c.msg.Title, confirm: c.msg.Confirm, sel: c.sel}
	if c.msg.Detail != "" {
		d.details = []string{c.msg.Detail}
	}
	return m.dialogView(d)
}
