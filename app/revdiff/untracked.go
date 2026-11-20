package main

// showUntrackedAtStart reports whether the review lists untracked files when
// it opens. aidiff reviews what an agent changed, and a file the agent created
// is untracked until someone stages it, so a review of the working tree (the
// uncommitted changes, or the working tree against one ref) lists them unless
// --no-untracked says otherwise, the way a T3 server's working-tree diff does.
// Staged and two-ref reviews compare no working tree, so they list them only
// when --untracked asks, as upstream revdiff does. u toggles them either way.
func (o options) showUntrackedAtStart() bool {
	if o.startupUntracked() {
		return true
	}
	return !o.NoUntracked && !o.Staged && !o.twoRef()
}
