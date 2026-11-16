package ui

import (
	"strings"

	"github.com/umputun/revdiff/app/ui/style"
)

// machineIdentity names, at the start of the status bar, the host the
// reviewed files live on, the project they belong to and whether they come
// from T3 Code.
type machineIdentity struct {
	name    string // the host, sanitized for display; "" shows none
	project string // the folder, or the T3 project, sanitized for display; "" shows none
	t3      bool   // the review comes from T3 Code
}

// MachineIdentityMsg updates the active host after connecting through the palette.
type MachineIdentityMsg struct {
	Name    string
	Project string
	T3      bool
}

// newMachineIdentity sanitizes a host and project name for the status bar.
func newMachineIdentity(name, project string, t3 bool) machineIdentity {
	return machineIdentity{name: style.SanitizeFilenameForDisplay(name), project: style.SanitizeFilenameForDisplay(project), t3: t3}
}

// handleMachineIdentity shows the host the palette connected to.
func (m Model) handleMachineIdentity(msg MachineIdentityMsg) Model {
	m.identity = newMachineIdentity(msg.Name, msg.Project, msg.T3)
	return m
}

// label is the status-bar prefix: the host, the project, then "T3" for a T3
// Code review, joined by sep. "" when there is nothing to show.
func (id machineIdentity) label(sep string) string {
	var parts []string
	for _, p := range []string{id.name, id.project} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if id.t3 {
		parts = append(parts, "T3")
	}
	return strings.Join(parts, sep)
}
