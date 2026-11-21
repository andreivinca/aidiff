package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
)

// RefreshMsg reloads from the VCS like R, but quietly, for an external file
// watcher: annotations are kept, there is no prompt, the current file list
// stays on screen while loading, and the cursor stays on its line at the same
// row on screen, with the search, horizontal scroll and expanded hunks kept.
type RefreshMsg struct{}

// anchorSearchRadius bounds how far the cursor's line is looked for when
// edits above it shifted line numbers.
const anchorSearchRadius = 200

// refreshState tracks the quiet reloads a RefreshMsg starts.
type refreshState struct {
	filesSeq uint64         // filesLoadSeq of the latest refresh's file-list load; 0 before the first
	anchor   *refreshAnchor // what that refresh restores once the displayed file reloads; nil when nothing is pending
}

// refreshAnchor is the view of the displayed file captured before a refresh.
type refreshAnchor struct {
	file       string       // the displayed file; the anchor applies to its reload only
	fileLevel  bool         // the cursor is on the file-level annotation, above the lines; line is unset
	line       lineAnchor   // the cursor's line
	onAnnot    bool         // the cursor is on the annotation below its line
	screenRow  int          // cursor row within the viewport
	searchTerm string       // the submitted search, re-run on the reloaded lines
	scrollX    int          // horizontal scroll offset
	expanded   []lineAnchor // first lines of the hunks expanded in collapsed mode
}

// lineAnchor identifies a diff line across a reload that may have moved it.
type lineAnchor struct {
	idx     int             // index before the reload
	srcLine int             // diffLineNum (0 for dividers)
	kind    diff.ChangeType // change type
	content string          // the line itself, to follow it when lines above change
}

// CanRefresh reports whether a RefreshMsg is applied now. It is not while a
// load is running (a refresh's included) or a mark-reviewed fingerprint is
// pending, while a cross-file jump waits for its file, while the user is typing
// an annotation or a search, or while a popup or prompt is open, a revert's
// included: a reload then would undo or lose what is in progress. The sender holds the refresh and
// retries after the next message.
func (m Model) CanRefresh() bool {
	loading := !m.filesLoaded || m.file.requestedPath != "" || len(m.reviewed.pending) > 0
	jumping := m.pendingAnnotJump != nil || m.nav.pendingHunkJump != nil || m.nav.pendingBoundaryJump != nil
	busy := m.annot.annotating || m.search.active || m.reload.pending || m.revert.pending || m.revert.asking || m.inConfirmDiscard ||
		m.keys.chordPending != "" || m.overlay.Active()
	return m.reload.applicable && !loading && !jumping && !busy
}

// refreshLoading reports whether the file list is being reloaded by a
// refresh, during which the current list stays on screen.
func (m Model) refreshLoading() bool {
	return !m.filesLoaded && m.refreshCurrent()
}

// handleRefresh starts a quiet reload, unless CanRefresh says to wait.
func (m Model) handleRefresh() (tea.Model, tea.Cmd) {
	if !m.CanRefresh() {
		return m, nil
	}
	anchor := m.captureRefreshAnchor()
	cmd := m.triggerReload()
	m.refresh = refreshState{filesSeq: m.filesLoadSeq, anchor: anchor}
	return m, cmd
}

// refreshCurrent reports whether the latest refresh's file list is still the
// current one: a later reload (R, a folder toggle) supersedes its anchor.
func (m Model) refreshCurrent() bool {
	return m.refresh.filesSeq != 0 && m.refresh.filesSeq == m.filesLoadSeq
}

// captureRefreshAnchor records the displayed file's view, nil when no file
// is displayed.
func (m Model) captureRefreshAnchor() *refreshAnchor {
	fileLevel := m.nav.diffCursor == -1
	if m.file.name == "" || m.nav.diffCursor < -1 || m.nav.diffCursor >= len(m.file.lines) {
		return nil
	}
	a := &refreshAnchor{
		file:       m.file.name,
		fileLevel:  fileLevel,
		onAnnot:    m.annot.cursorOnAnnotation,
		screenRow:  m.cursorViewportY() - m.layout.viewport.YOffset,
		searchTerm: m.search.term,
		scrollX:    m.layout.scrollX,
	}
	if !fileLevel {
		a.line = m.lineAnchorAt(m.nav.diffCursor)
	}
	for _, start := range m.findHunks() {
		if m.modes.collapsed.expandedHunks[start] {
			a.expanded = append(a.expanded, m.lineAnchorAt(start))
		}
	}
	return a
}

// recaptureRefreshAnchor records the view again just before the displayed
// file's reload replaces its lines (Update calls it ahead of
// handleFileLoaded): keys are not blocked while a refresh loads, so the
// cursor may have moved, or an annotation started on another line, since
// the refresh began, and restoring the view from then would undo it. Once
// another file is displayed the anchor no longer applies.
func (m *Model) recaptureRefreshAnchor(msg fileLoadedMsg) {
	a := m.refresh.anchor
	if a == nil || msg.err != nil || msg.seq != m.file.loadSeq || msg.file != a.file {
		return
	}
	if m.file.name != a.file {
		m.refresh.anchor = nil
		return
	}
	m.refresh.anchor = m.captureRefreshAnchor()
}

// refreshShownFile reloads the displayed file when a refresh's file list
// landed with the tree selecting no file (a folder row, or a filter hiding
// it), which would leave the diff pane stale. The tree cursor stays put.
func (m *Model) refreshShownFile(entries []diff.FileEntry) tea.Cmd {
	a := m.refresh.anchor
	if a == nil || !m.refreshCurrent() || a.file != m.file.name {
		return nil
	}
	for _, entry := range entries {
		if entry.Path == a.file {
			return m.requestFileDiff(a.file)
		}
	}
	m.refresh.anchor = nil // the displayed file is gone from the list
	return nil
}

// applyRefreshAnchor restores the view captured before a refresh when msg is
// the displayed file's reload, and reports whether it did. Any file load
// consumes the anchor: once another file is shown, it no longer applies.
func (m *Model) applyRefreshAnchor(msg fileLoadedMsg) bool {
	a := m.refresh.anchor
	if a == nil {
		return false
	}
	m.refresh.anchor = nil
	if a.file != msg.file || !m.refreshCurrent() {
		return false
	}
	m.restoreExpandedHunks(a.expanded)
	m.restoreSearch(a.searchTerm)
	if !m.modes.wrap {
		m.layout.scrollX = a.scrollX
		m.clampHorizontalScroll() // the reloaded lines may be shorter
	}
	m.restoreCursor(a)
	return true
}

// restoreCursor puts the cursor back on its line, or on the file-level
// annotation or a line's annotation when it was on one, and keeps it on the
// same screen row. See findAnchoredLine for how the line is found.
func (m *Model) restoreCursor(a *refreshAnchor) {
	m.annot.cursorOnAnnotation = false
	idx := m.findAnchoredLine(a)
	switch {
	case a.fileLevel && m.hasFileAnnotation():
		m.nav.diffCursor = -1
	case a.fileLevel || idx < 0:
		m.skipInitialDividers()
	default:
		m.nav.diffCursor = idx
		m.adjustCursorIfHidden()
		m.annot.cursorOnAnnotation = a.onAnnot && m.nav.diffCursor == idx && m.lineHasAnnotation(idx)
	}
	m.realignSearchCursor()
	m.syncTOCActiveSection()
	m.layout.viewport.SetContent(m.renderDiff())
	m.layout.viewport.SetYOffset(max(0, m.cursorViewportY()-a.screenRow))
}

// findAnchoredLine finds the cursor's line in the reloaded file, trying in
// order: the same line number with the same content and type (nothing moved
// it), the nearest line with that content and type (lines above it were added
// or removed), the same line number and type (its own line was edited), any
// line with that number, and finally the same position. -1 for an empty file.
func (m Model) findAnchoredLine(a *refreshAnchor) int {
	line := a.line
	if line.srcLine > 0 && line.kind != diff.ChangeDivider {
		if idx := m.findDiffLineIndex(line.srcLine, string(line.kind)); idx >= 0 && m.file.lines[idx].Content == line.content {
			return idx
		}
		if idx := m.nearestLine(line.idx, m.sameAs(line)); idx >= 0 {
			return idx
		}
		if idx := m.findDiffLineIndex(line.srcLine, string(line.kind)); idx >= 0 {
			return idx
		}
		if idx := m.nearestLine(line.idx, m.numberedLike(line)); idx >= 0 {
			return idx
		}
	}
	if len(m.file.lines) == 0 {
		return -1
	}
	return min(max(0, line.idx), len(m.file.lines)-1)
}

// restoreExpandedHunks expands again the hunks that were expanded before the
// refresh, found by their first line.
func (m *Model) restoreExpandedHunks(expanded []lineAnchor) {
	if len(expanded) == 0 {
		return
	}
	starts := make(map[int]bool)
	for _, start := range m.findHunks() {
		starts[start] = true
	}
	for _, hunk := range expanded {
		sameLine := m.sameAs(hunk)
		match := func(i int) bool {
			return starts[i] && sameLine(i)
		}
		if idx := m.nearestLine(hunk.idx, match); idx >= 0 {
			m.modes.collapsed.expandedHunks[idx] = true
		}
	}
}

// restoreSearch re-runs the submitted search on the reloaded lines, the way
// submitSearch matches them, without moving the cursor.
func (m *Model) restoreSearch(term string) {
	if term == "" {
		return
	}
	m.search.term = term
	m.search.matches = nil
	for i, dl := range m.file.lines {
		if strings.Contains(strings.ToLower(dl.Content), term) {
			m.search.matches = append(m.search.matches, i)
		}
	}
}

// lineHasAnnotation reports whether the diff line at idx carries an annotation.
func (m Model) lineHasAnnotation(idx int) bool {
	dl := m.file.lines[idx]
	return m.store.Has(m.file.name, m.diffLineNum(dl), string(dl.ChangeType))
}

// lineAnchorAt describes the diff line at idx.
func (m Model) lineAnchorAt(idx int) lineAnchor {
	dl := m.file.lines[idx]
	return lineAnchor{idx: idx, srcLine: m.diffLineNum(dl), kind: dl.ChangeType, content: dl.Content}
}

// sameAs returns a match for the lines with the anchored line's content and
// change type.
func (m Model) sameAs(a lineAnchor) func(i int) bool {
	return func(i int) bool {
		dl := m.file.lines[i]
		return dl.ChangeType == a.kind && dl.Content == a.content
	}
}

// numberedLike returns a match for any non-divider line with the anchor's
// line number, whatever its change type.
func (m Model) numberedLike(a lineAnchor) func(i int) bool {
	return func(i int) bool {
		dl := m.file.lines[i]
		return dl.ChangeType != diff.ChangeDivider && m.diffLineNum(dl) == a.srcLine
	}
}

// nearestLine returns the line closest to around that satisfies match,
// within anchorSearchRadius, or -1. At equal distance the line below wins:
// lines added above the anchored one push it down.
func (m Model) nearestLine(around int, match func(i int) bool) int {
	lines := len(m.file.lines)
	if lines == 0 {
		return -1
	}
	around = min(max(0, around), lines-1)
	for d := 0; d <= anchorSearchRadius; d++ {
		if below := around + d; below < lines && match(below) {
			return below
		}
		if above := around - d; above >= 0 && match(above) {
			return above
		}
	}
	return -1
}
