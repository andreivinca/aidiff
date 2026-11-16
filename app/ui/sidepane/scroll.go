package sidepane

// ScrollBy shifts the visible window by delta entries without moving the
// cursor, for the mouse wheel. The window stays where the wheel put it until
// EnsureVisible brings the cursor back into view.
func (ft *FileTree) ScrollBy(delta, height int) {
	ft.offset = ft.clampOffset(ft.offset+delta, height)
	ft.manualScroll = true
}

// followCursor keeps the cursor within the visible window, or, after the
// wheel scrolled it, keeps the window in range.
func (ft *FileTree) followCursor(height int) {
	if ft.manualScroll {
		ft.offset = ft.clampOffset(ft.offset, height)
		return
	}
	ft.EnsureVisible(height)
}

// clampOffset limits offset so a window of height rows stays within the entries.
func (ft *FileTree) clampOffset(offset, height int) int {
	last := max(len(ft.entries)-max(height, 1), 0)
	return min(max(offset, 0), last)
}
