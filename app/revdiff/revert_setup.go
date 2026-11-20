package main

import (
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/revert"
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
