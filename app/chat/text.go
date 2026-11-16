package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// maxNoteRows bounds a wrapped note, so /help fits a narrow pane.
const maxNoteRows = 4

// noteRows wraps a note under the input line.
func noteRows(note string, w int, st lipgloss.Style) []string {
	rows := strings.Split(ansi.Wrap(note, max(1, w), ""), "\n")
	if len(rows) > maxNoteRows {
		rows = rows[:maxNoteRows]
		rows[maxNoteRows-1] = ansi.Truncate(rows[maxNoteRows-1], max(1, w-1), "") + "…"
	}
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

// wrapLines wraps text to the pane, inset, one string per row.
func wrapLines(text string, w int) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	limit := max(1, w-len(inset))
	var out []string
	for para := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(para) == "" {
			out = append(out, "")
			continue
		}
		for l := range strings.SplitSeq(ansi.Wrap(para, limit, ""), "\n") {
			out = append(out, inset+l)
		}
	}
	return out
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
