package ui

import (
	"github.com/umputun/revdiff/app/ui/style"
)

// machineIdentity names, at the start of the status bar, the host the
// reviewed files live on and whether they come from T3 Code.
type machineIdentity struct {
	name string // the host, sanitized for display; "" shows none
	t3   bool   // the review comes from T3 Code
}

// MachineIdentityMsg updates the active host after connecting through the palette.
type MachineIdentityMsg struct {
	Name string
	T3   bool
}

// newMachineIdentity sanitizes a host name for the status bar.
func newMachineIdentity(name string, t3 bool) machineIdentity {
	return machineIdentity{name: style.SanitizeFilenameForDisplay(name), t3: t3}
}

// handleMachineIdentity shows the host the palette connected to.
func (m Model) handleMachineIdentity(msg MachineIdentityMsg) Model {
	m.identity = newMachineIdentity(msg.Name, msg.T3)
	return m
}

// label is the status-bar prefix: the host, then "T3" for a T3 Code review,
// joined by sep. "" when there is nothing to show.
func (id machineIdentity) label(sep string) string {
	switch {
	case id.t3 && id.name != "":
		return id.name + sep + "T3"
	case id.t3:
		return "T3"
	}
	return id.name
}
