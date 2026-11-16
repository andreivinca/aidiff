package chat

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	linkSpan = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	boldSpan = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	codeSpan = regexp.MustCompile("`([^`]+)`")
	heading  = regexp.MustCompile(`^#{1,6}\s+`)
	bullet   = regexp.MustCompile(`^(\s*)[-*]\s+`)
)

// markdownRows styles the agent's prose the way OpenCode conceals markdown:
// headings bold in the accent color, bullets as •, [links](url) as their
// text, **bold** bold, `code` green, fenced code as an indented block. Rows
// are inset and wrapped to w.
func markdownRows(text string, w int, st styles) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	limit := max(1, w-len(inset))
	var out []string
	fenced := false
	for para := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(para), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			out = append(out, st.code.Render(ansi.Truncate(inset+"  "+para, w, "…")))
			continue
		}
		if strings.TrimSpace(para) == "" {
			out = append(out, "")
			continue
		}
		style := st.text
		if heading.MatchString(para) {
			para = heading.ReplaceAllString(para, "")
			style = st.heading
		}
		para = bullet.ReplaceAllString(para, "$1• ")
		para = linkSpan.ReplaceAllString(para, "$1") // a link shows its text, as OpenCode conceals it
		for l := range strings.SplitSeq(ansi.Wrap(para, limit, ""), "\n") {
			out = append(out, inset+styleSpans(l, style, st))
		}
	}
	return out
}

// styleSpans renders inline markdown within one wrapped line.
func styleSpans(line string, base lipgloss.Style, st styles) string {
	line = boldSpan.ReplaceAllStringFunc(line, func(m string) string {
		return st.bold.Render(m[2 : len(m)-2])
	})
	line = codeSpan.ReplaceAllStringFunc(line, func(m string) string {
		return st.code.Render(m[1 : len(m)-1])
	})
	return base.Render(line)
}
