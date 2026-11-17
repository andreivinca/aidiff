package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/ui/style"
)

// aidiff's additions to the pane chrome: the machine identity at the start of
// the status bar and the drag handle on the tree/diff divider. The diff pane
// has no right border to draw a scrollbar on (style.paneBorders), so View
// leaves applyScrollbar out.

// statusBarPadding is the horizontal padding of the status bar style
// (Padding(0, 1)), which statusBarText reserves too.
const statusBarPadding = 2

const (
	// dividerHandle marks the middle of the tree/diff divider as draggable.
	dividerHandle = "↔"
	// scrollbarThumbGlyph is the visible glyph inside scrollbarThumbRune.
	scrollbarThumbGlyph = "┃"
)

// statusBarWithMachine starts the status bar with the machine identity and
// lays the usual content out in the width left beside it, so its segments
// degrade gracefully instead of being cut off. Hints and search input, which
// statusBarText does not fit to the width, are truncated.
func (m Model) statusBarWithMachine() string {
	sep := m.renderer.StatusBarSeparator()
	identity := m.identity.label(sep)
	if identity == "" {
		return m.statusBarText()
	}
	available := max(m.layout.width-statusBarPadding, 0)
	prefix := identity + sep
	prefixWidth := lipgloss.Width(prefix)
	if prefixWidth >= available {
		return ansi.Truncate(identity, available, "…")
	}
	rest := m.statusBarTextWithin(m.layout.width - prefixWidth)
	return prefix + ansi.Truncate(rest, available-prefixWidth, "…")
}

// statusBarTextWithin renders statusBarText for a bar width columns wide.
// statusBarText sizes its segments by m.layout.width, so it is given a copy
// of the model narrowed to width; the model itself is untouched.
func (m Model) statusBarTextWithin(width int) string {
	m.layout.width = width
	return m.statusBarText()
}

// withDividerHandle marks the draggable divider on the middle row of the
// rendered tree pane.
func withDividerHandle(rendered string) string {
	rows := strings.Split(rendered, "\n")
	middle := len(rows) / 2
	rows[middle] = replaceDivider(rows[middle])
	return strings.Join(rows, "\n")
}

// replaceDivider swaps a row's divider glyph for the handle. The divider is
// the row's last visible cell: the border track, or the navigation
// scrollbar's thumb drawn over it. A row whose last visible cell is neither
// is returned unchanged, so pane content is never replaced.
func replaceDivider(row string) string {
	for _, glyph := range []string{scrollbarTrackRune, scrollbarThumbGlyph} {
		idx := strings.LastIndex(row, glyph)
		if idx < 0 {
			continue
		}
		rest := row[idx+len(glyph):]
		if ansi.StringWidth(rest) == 0 {
			return row[:idx] + dividerHandle + rest
		}
	}
	return row
}

// HasStatusLine reports whether the review draws a status bar as its last
// row. aidiff's split then draws that row across the whole window instead
// (StatusLine), under every pane.
func (m Model) HasStatusLine() bool {
	return !m.cfg.noStatusBar
}

// minStatusWidth is the least the review's own status keeps when the bar
// also carries a pane's hints.
const minStatusWidth = 30

// StatusLine is the status bar rendered width columns wide, for the split to
// draw under the whole window: the review's own status, then hints, the
// shortcuts of the focused pane as key and label pairs, on the right. Hints
// that do not fit are left out from the last. While View shows no status
// bar yet (still loading) only the hints show.
func (m Model) StatusLine(width int, hints [][2]string) string {
	bar := m.resolver.Style(style.StyleKeyStatusBar).Width(width)
	inner := max(0, width-statusBarPadding)
	sep := m.renderer.StatusBarSeparator()
	loading := !m.ready || (!m.filesLoaded && !m.refreshLoading())
	room := inner - lipgloss.Width(sep) - minStatusWidth
	if loading {
		room = inner
	}
	right := m.statusHints(hints, room)
	if loading {
		return bar.Render(strings.Repeat(" ", max(0, inner-lipgloss.Width(right))) + right)
	}
	if right == "" {
		m.layout.width = width // a copy: the model's own layout is untouched
		return bar.Render(m.statusBarWithMachine())
	}
	leftW := inner - lipgloss.Width(right) - lipgloss.Width(sep)
	m.layout.width = leftW + statusBarPadding
	left := m.statusBarWithMachine()
	left += strings.Repeat(" ", max(0, leftW-lipgloss.Width(left)))
	return bar.Render(left + sep + right)
}

// statusHints draws as many of hints as fit in width: each key in the
// bar's color and its label muted, in raw ANSI so the bar's background
// survives.
func (m Model) statusHints(hints [][2]string, width int) string {
	muted := string(m.resolver.Color(style.ColorKeyMutedFg))
	normal := string(m.resolver.Color(style.ColorKeyStatusFg))
	out := ""
	for _, h := range hints {
		text := muted + h[1] + normal
		if h[0] != "" {
			text = h[0] + " " + text
		}
		if out != "" {
			text = out + "  " + text
		}
		if lipgloss.Width(text) > width {
			break
		}
		out = text
	}
	return out
}
