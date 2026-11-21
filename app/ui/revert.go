package ui

import (
	"fmt"
	"path"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/ui/style"
)

// aidiff reverts changes from the review: x the change block under the
// cursor, X the whole file on screen, each after a confirmation: a popup
// (ModelConfig.Confirm), else a y on the status bar. The work
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

// Confirm asks the user to confirm in a popup: title is the question, detail
// what confirming does, button the confirm button's label; yes runs on
// confirm, no (which may be nil) whenever the popup ends otherwise. It
// returns the command that opens the popup.
type Confirm func(title, detail, button string, yes, no tea.Cmd) tea.Cmd

// revertState is a revert waiting for its confirmation.
type revertState struct {
	reverter Reverter // nil: this review cannot revert
	confirm  Confirm  // the popup asking; nil asks on the status bar
	pending  bool
	// asking marks a popup asking to revert as possibly open: the revert
	// it confirms edits the lines on screen, so refreshes wait (CanRefresh)
	// to keep them the ones asked about. The popup's no, the revert's
	// result, a key or mouse event reaching the review (which the open
	// popup would have taken), or askSeq's expiry, a backstop for a popup
	// that never answers, ends the wait.
	asking bool
	askSeq uint64
	whole  bool // the whole file, else lines[start:end+1]
	file   diff.FileEntry
	lines  []diff.DiffLine
	start  int
	end    int
	hint   string
}

// revertedMsg reports a finished revert.
type revertedMsg struct {
	what string
	err  error
}

// revertAskTimeout bounds how long refreshes wait on a revert popup that
// never said it ended: a revert confirmed after that still refuses a file
// that changed under it (app/revert).
const revertAskTimeout = 15 * time.Second

// revertAskEndedMsg ends the wait on the popup asked as seq: it was
// cancelled, or the wait expired.
type revertAskEndedMsg struct{ seq uint64 }

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
	name, shown, oldShown := style.SanitizeFilenameForDisplay(path.Base(file.Path)),
		style.SanitizeFilenameForDisplay(file.Path), style.SanitizeFilenameForDisplay(file.OldPath)
	r := revertState{reverter: m.revert.reverter, confirm: m.revert.confirm, whole: whole, file: file, askSeq: m.revert.askSeq}
	var title, detail, button string
	if whole {
		title, detail, button = "Revert "+name+"?", "Puts "+shown+" back as the review's base has it. Its changes are lost.", "Revert"
		switch file.Status {
		case diff.FileAdded, diff.FileUntracked:
			title, detail, button = "Delete "+name+"?", shown+" is a new file: reverting it deletes it.", "Delete"
		case diff.FileRenamed:
			detail = "Undoes the rename: " + oldShown + " comes back and " + shown + " is deleted. Its changes are lost."
		default:
		}
	} else {
		start, end, ok := m.hunkBounds(m.nav.diffCursor)
		if !ok {
			m.revert.hint = "Put the cursor on a change to revert it (X reverts the whole file)"
			return m, nil
		}
		r.lines, r.start, r.end = m.file.lines, start, end
		n := end - start + 1
		title, button = "Revert this change?", "Revert"
		detail = fmt.Sprintf("Undoes the %d changed line%s under the cursor in %s.", n, plural(n), shown)
	}
	if r.confirm != nil {
		m.revert.asking = true
		m.revert.askSeq++
		seq := m.revert.askSeq
		ended := func() tea.Msg { return revertAskEndedMsg{seq: seq} }
		expire := tea.Tick(revertAskTimeout, func(time.Time) tea.Msg { return ended() })
		return m, tea.Batch(r.confirm(title, detail, button, r.run(), ended), expire)
	}
	r.pending = true
	r.hint = title + " y to confirm, any other key to cancel"
	m.revert = r
	return m, nil
}

// run is the command doing the revert r describes.
func (r revertState) run() tea.Cmd {
	name := style.SanitizeFilenameForDisplay(path.Base(r.file.Path))
	what := "the change in " + name
	if r.whole {
		what = name
	}
	return func() tea.Msg {
		var err error
		if r.whole {
			err = r.reverter.RevertFile(r.file)
		} else {
			err = r.reverter.RevertChange(r.file, r.lines, r.start, r.end)
		}
		return revertedMsg{what: what, err: err}
	}
}

// handlePendingRevert runs the revert on y and drops it on any other key.
func (m Model) handlePendingRevert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.revert
	m.revert = revertState{reverter: r.reverter, confirm: r.confirm, askSeq: r.askSeq}
	if msg.String() != "y" {
		m.revert.hint = "Revert cancelled"
		return m, nil
	}
	return m, r.run()
}

// handleReverted reports the revert and reloads the review to show it.
func (m Model) handleReverted(msg revertedMsg) (tea.Model, tea.Cmd) {
	m.revert.asking = false
	if msg.err != nil {
		m.revert.hint = "Revert failed: " + msg.err.Error()
		return m, nil
	}
	m.revert.hint = "Reverted " + msg.what
	return m, func() tea.Msg { return RefreshMsg{} }
}

// handleRevertAskEnded stops refreshes waiting on the popup asked as seq.
func (m Model) handleRevertAskEnded(msg revertAskEndedMsg) Model {
	if msg.seq == m.revert.askSeq {
		m.revert.asking = false
	}
	return m
}

// inputSeen notes that a key or mouse event reached the review: a revert
// popup, which takes them while open, is gone, and the hint has been read.
func (r *revertState) inputSeen() {
	r.hint = ""
	r.asking = false
}

// plural is "s" unless n is 1.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
