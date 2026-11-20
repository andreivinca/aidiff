package ui

import (
	"fmt"
	"path"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
)

// aidiff reverts changes from the review: x the change block under the
// cursor, X the whole file on screen, each after a y confirmation. The work
// is the Reverter's (app/revert); the review only says what to revert and
// reloads quietly afterwards, keeping its annotations.

// Reverter undoes changes of the review in the working tree.
type Reverter interface {
	// RevertChange undoes the change block lines[start:end+1] of file,
	// whose whole diff is lines.
	RevertChange(file diff.FileEntry, lines []diff.DiffLine, start, end int) error
	// RevertFile puts file back as the review's base has it.
	RevertFile(file diff.FileEntry) error
}

// revertState is a revert waiting for its confirmation.
type revertState struct {
	reverter Reverter // nil: this review cannot revert
	pending  bool
	whole    bool // the whole file, else lines[start:end+1]
	file     diff.FileEntry
	lines    []diff.DiffLine
	start    int
	end      int
	hint     string
}

// revertedMsg reports a finished revert.
type revertedMsg struct {
	what string
	err  error
}

// handleRevert asks to confirm reverting the change under the cursor, or
// the whole file on screen.
func (m Model) handleRevert(whole bool) (tea.Model, tea.Cmd) {
	switch {
	case m.revert.reverter == nil:
		m.revert.hint = "Revert works in a local git review of the working tree"
		return m, nil
	case m.file.name == "" || m.browsingFolders():
		m.revert.hint = "No changed file to revert"
		return m, nil
	}
	file := diff.FileEntry{Path: m.file.name, Status: m.tree.FileStatus(m.file.name), OldPath: m.tree.OldPath(m.file.name)}
	name := path.Base(file.Path)
	m.revert.pending, m.revert.whole, m.revert.file = true, whole, file
	if whole {
		action := "Revert " + name
		switch file.Status {
		case diff.FileAdded, diff.FileUntracked:
			action = "Delete " + name + " (new file)"
		case diff.FileRenamed:
			action = "Revert " + name + " (undo the rename)"
		default:
		}
		m.revert.hint = action + "? y to confirm, any other key to cancel"
		return m, nil
	}
	start, end, ok := m.hunkBounds(m.nav.diffCursor)
	if !ok {
		m.revert.pending = false
		m.revert.hint = "Put the cursor on a change to revert it (X reverts the whole file)"
		return m, nil
	}
	m.revert.lines, m.revert.start, m.revert.end = m.file.lines, start, end
	m.revert.hint = fmt.Sprintf("Revert this change in %s (%d line%s)? y to confirm, any other key to cancel",
		name, end-start+1, plural(end-start+1))
	return m, nil
}

// handlePendingRevert runs the revert on y and drops it on any other key.
func (m Model) handlePendingRevert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.revert
	m.revert = revertState{reverter: r.reverter}
	if msg.String() != "y" {
		m.revert.hint = "Revert cancelled"
		return m, nil
	}
	what := "the change in " + path.Base(r.file.Path)
	if r.whole {
		what = path.Base(r.file.Path)
	}
	return m, func() tea.Msg {
		var err error
		if r.whole {
			err = r.reverter.RevertFile(r.file)
		} else {
			err = r.reverter.RevertChange(r.file, r.lines, r.start, r.end)
		}
		return revertedMsg{what: what, err: err}
	}
}

// handleReverted reports the revert and reloads the review to show it.
func (m Model) handleReverted(msg revertedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.revert.hint = "Revert failed: " + msg.err.Error()
		return m, nil
	}
	m.revert.hint = "Reverted " + msg.what
	return m, func() tea.Msg { return RefreshMsg{} }
}

// plural is "s" unless n is 1.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
