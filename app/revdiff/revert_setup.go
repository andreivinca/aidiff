package main

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/revert"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/ui"
)

// reverter undoes the review's changes for x / X: only in a review of a local
// git repository's working tree (the unstaged changes, or the working tree
// against one ref), since elsewhere the new side is not a file here to edit
// (--staged, two refs, stdin, a compare pair, a folder, a T3 host).
func (s sourceSetup) reverter(opts options) ui.Reverter {
	if s.vcsType != diff.VCSGit || s.gitRoot == "" || s.t3 != nil || s.folder != nil || s.remote != nil ||
		opts.Stdin || opts.AllFiles || opts.CompareOld != "" || opts.Staged || opts.twoRef() {
		return nil
	}
	return revert.Git{Root: s.gitRoot, Ref: opts.ref()}
}

// confirmPopup asks in the split's confirmation popup.
func confirmPopup(title, detail, button string, yes, no tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		return splitview.ConfirmMsg{Title: title, Detail: detail, Confirm: button, Yes: yes, No: no}
	}
}
