package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// noteRows wraps a note under the input line, all of it: a note may be the
// only word of what failed.
func noteRows(note string, w int, st lipgloss.Style) []string {
	rows := strings.Split(ansi.Wrap(sanitize(note), max(1, w), ""), "\n") // a note may carry the backend's words
	for i := range rows {
		rows[i] = st.Render(rows[i])
	}
	return rows
}

// firstLine is the first line of s, trimmed.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

// wrapLines wraps text to the pane, inset, each line on rows of its own.
func wrapLines(text string, w int) block {
	if strings.TrimRight(text, "\n") == "" {
		return block{}
	}
	return wrapText("", text, w, false).styled(func(r string) string {
		return inset + r
	})
}

// fit forces a row to exactly w cells.
func fit(s string, w int) string {
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "")
	}
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}
