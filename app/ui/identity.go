package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/ui/style"
)

// machineIdentity names, at the start of the status bar, the host the
// reviewed files live on, the project they belong to and whether they come
// from T3 Code.
type machineIdentity struct {
	name    string // the host, sanitized for display; "" shows none
	branch  string
	project string // the folder, or the T3 project, sanitized for display; "" shows none
	t3      bool   // the live connection is T3 Code's, so the label says "T3"
	// t3Review marks a review from T3 Code, whatever connection the palette
	// has made since: set once in NewModel, it keeps the branch poll going
	t3Review bool
}

// MachineIdentityMsg updates the active host after connecting through the
// palette. A Project or Branch left empty keeps the review's own.
type MachineIdentityMsg struct {
	Name    string
	Project string
	Branch  string
	T3      bool
}

// newMachineIdentity sanitizes a host and project name for the status bar.
func newMachineIdentity(name, project string, t3 bool, branch ...string) machineIdentity {
	b := ""
	if len(branch) > 0 {
		b = style.SanitizeFilenameForDisplay(branch[0])
	}
	return machineIdentity{branch: b, name: style.SanitizeFilenameForDisplay(name), project: style.SanitizeFilenameForDisplay(project),
		t3: t3, t3Review: t3}
}

// handleMachineIdentity shows the host the palette connected to. The
// review's project and branch stay unless the message names others: a
// connection says which machine, not what the review on screen shows.
func (m Model) handleMachineIdentity(msg MachineIdentityMsg) Model {
	project, branch, review := m.identity.project, m.identity.branch, m.identity.t3Review
	m.identity = newMachineIdentity(msg.Name, msg.Project, msg.T3, msg.Branch)
	m.identity.t3Review = review
	if msg.Project == "" {
		m.identity.project = project
	}
	if msg.Branch == "" {
		m.identity.branch = branch
	}
	return m
}

// label is the status-bar prefix: the host, the project, then "T3" for a T3
// Code review, joined by sep. "" when there is nothing to show.
func (id machineIdentity) label(sep string) string {
	var parts []string
	for _, p := range []string{id.name, id.project, id.branch} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if id.t3 {
		parts = append(parts, "T3")
	}
	return strings.Join(parts, sep)
}

// branchPollInterval is how often a T3 review re-reads its branch: a
// checkout on the T3 host, in one of its terminals say, reaches no watcher
// here, so nothing else would reload the review.
const branchPollInterval = 5 * time.Second

// branchLoadedMsg is the branch a loadBranchCmd read. poll marks the T3
// poll's own read, which schedules the next one.
type branchLoadedMsg struct {
	branch string
	err    error
	poll   bool
}

// loadBranchCmd re-reads the branch; nil when the branch is fixed.
func (m Model) loadBranchCmd(poll bool) tea.Cmd {
	load := m.loadBranch
	if load == nil {
		return nil
	}
	return func() tea.Msg {
		branch, err := load()
		return branchLoadedMsg{branch: branch, err: err, poll: poll}
	}
}

// pollBranch schedules the T3 poll's next read; nil outside a T3 review,
// where the file watcher's reloads re-read the branch.
func (m Model) pollBranch() tea.Cmd {
	if !m.identity.t3Review || m.loadBranch == nil {
		return nil
	}
	load := m.loadBranchCmd(true)
	return tea.Tick(branchPollInterval, func(time.Time) tea.Msg { return load() })
}

// handleBranchLoaded shows the branch read; a failed read keeps the one
// shown, since a dropped connection says nothing about the branch.
func (m Model) handleBranchLoaded(msg branchLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil {
		m.identity.branch = style.SanitizeFilenameForDisplay(msg.branch)
	}
	if msg.poll {
		return m, m.pollBranch()
	}
	return m, nil
}
