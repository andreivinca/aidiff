package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
)

// RefreshMsg reloads from the VCS like R, but quietly, for an external file
// watcher: annotations are kept, there is no prompt, the current file list
// stays on screen while loading, and the cursor stays on its line at the same
// row on screen.
type RefreshMsg struct{}

// anchorSearchRadius bounds how far the cursor's line is looked for when
// edits above it shifted line numbers.
const anchorSearchRadius = 200

// refreshAnchor is the cursor captured before a RefreshMsg reload.
type refreshAnchor struct {
	compactAnchor        // seq, source line number, change type, nearest hunk
	content       string // the line itself, to follow it when lines above change
	oldIdx        int    // cursor index before the reload
	screenRow     int    // cursor row within the viewport
}

// CanRefresh reports whether a RefreshMsg is applied now. It is not while the
// user is typing an annotation or a search, or a popup or prompt is open; the
// sender should hold the refresh and retry after the next input.
func (m Model) CanRefresh() bool {
	return m.reload.applicable && m.filesLoaded && !m.annot.annotating && !m.search.active &&
		!m.reload.pending && !m.inConfirmDiscard && m.keys.chordPending == "" && !m.overlay.Active()
}

func (m Model) handleRefresh() (tea.Model, tea.Cmd) {
	if !m.CanRefresh() {
		return m, nil
	}
	var anchor *refreshAnchor
	if ca := m.captureCompactAnchor(); ca != nil {
		anchor = &refreshAnchor{
			compactAnchor: *ca,
			content:       m.file.lines[m.nav.diffCursor].Content,
			oldIdx:        m.nav.diffCursor,
			screenRow:     m.cursorViewportY() - m.layout.viewport.YOffset,
		}
	}
	cmd := m.triggerReload()
	m.filesLoaded = true // keep showing the current list instead of "loading files..."
	if anchor != nil {
		// handleFilesLoaded requests the displayed file once more; tie the anchor
		// to that request, so any other load in between (the user moving on)
		// leaves it unused.
		anchor.seq = m.file.loadSeq + 1
		m.refresh = anchor
	}
	return m, cmd
}

// applyRefreshAnchor puts the cursor back on its line, trying in order: the
// nearest line with the same content and change type (lines above it may have
// moved it), the same line number and type, any line with that number (its own
// line was edited), and finally the same position. The viewport keeps the
// cursor on the same screen row.
func (m *Model) applyRefreshAnchor(a *refreshAnchor) {
	m.annot.cursorOnAnnotation = false
	idx := -1
	if a.srcLine > 0 && a.changeType != diff.ChangeDivider {
		idx = m.nearestLineWithContent(a)
		if idx < 0 {
			idx = m.findDiffLineIndex(a.srcLine, string(a.changeType))
		}
		if idx < 0 {
			idx = m.nearestLineNumbered(a)
		}
	}
	if idx < 0 && len(m.file.lines) > 0 {
		idx = min(max(0, a.oldIdx), len(m.file.lines)-1)
	}
	if idx < 0 {
		m.skipInitialDividers()
	} else {
		m.nav.diffCursor = idx
		m.adjustCursorIfHidden()
	}
	m.layout.viewport.SetContent(m.renderDiff())
	m.layout.viewport.SetYOffset(max(0, m.cursorViewportY()-a.screenRow))
}

// nearestLineNumbered finds the line closest to the old index whose line
// number matches, whatever its change type.
func (m Model) nearestLineNumbered(a *refreshAnchor) int {
	best, bestDist := -1, 0
	for i, dl := range m.file.lines {
		if dl.ChangeType == diff.ChangeDivider || m.diffLineNum(dl) != a.srcLine {
			continue
		}
		if d := abs(i - a.oldIdx); best < 0 || d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// nearestLineWithContent searches outward from the cursor's old index.
func (m Model) nearestLineWithContent(a *refreshAnchor) int {
	lines := m.file.lines
	around := min(max(0, a.oldIdx), max(0, len(lines)-1))
	for d := 0; d <= anchorSearchRadius; d++ {
		for _, i := range [2]int{around - d, around + d} {
			if i >= 0 && i < len(lines) && lines[i].ChangeType == a.changeType && lines[i].Content == a.content {
				return i
			}
		}
	}
	return -1
}
