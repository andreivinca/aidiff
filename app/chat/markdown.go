package chat

import (
	"regexp"
	"slices"
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
// headings bold in the accent color, bullets as • with their text hanging
// under itself, [links](url) as their text, **bold** bold, `code` green,
// fenced code as an indented block, and pipe tables drawn as tables with
// box-drawing borders, their cells keeping inline styles (table.go). Rows
// are inset and fitted to w, never cut: prose wraps at spaces, code
// anywhere with its spacing kept, a table's columns shrink and their cells
// wrap; a copy of the selection puts each wrapped line back together. A
// reply still being written ends with streamCursor.
func markdownRows(text string, w int, st styles) block {
	var b block
	streaming := strings.HasSuffix(text, streamCursor)
	text = strings.TrimRight(strings.TrimSuffix(text, streamCursor), "\n")
	if text == "" {
		if streaming {
			b.plain(inset + st.text.Render(streamCursor))
		}
		return b
	}
	lines := strings.Split(text, "\n") // what is parsed
	shown := lines                     // what is drawn: the cursor after the last line
	if streaming {
		shown = slices.Clone(lines)
		shown[len(shown)-1] += streamCursor
	}
	fenced := false
	for i := 0; i < len(lines); i++ {
		para := shown[i]
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			b.join(codeRows(para, w, st))
			continue
		}
		if strings.TrimSpace(para) == "" {
			b.plain("")
			continue
		}
		if t, n := tableAt(lines, i, streaming); n > 0 {
			if streaming && i+n == len(lines) {
				t = t.withCursor()
			}
			b.join(tableRows(t, w, st))
			i += n - 1
			continue
		}
		style := st.text
		if heading.MatchString(para) {
			para = heading.ReplaceAllString(para, "")
			style = st.heading
		}
		lead := ""
		if m := bullet.FindStringSubmatchIndex(para); m != nil {
			lead, para = para[m[2]:m[3]]+"• ", para[m[1]:]
		}
		para = linkSpan.ReplaceAllString(para, "$1") // a link shows its text, as OpenCode conceals it
		b.join(wrapText(lead, para, w, false).styled(func(r string) string {
			return inset + styleSpans(r, style, st)
		}))
	}
	return b
}

// codeGutter indents a fenced block within the inset.
const codeGutter = "  "

// codeRows is one line of a fenced block, wrapped at any character rather
// than cut, since code has long tokens without spaces: its text exactly,
// spacing included, in the code style on every row. Rows after the first
// hang two cells under the line's own indentation, so they read as one
// line, and a copy joins them back into it.
func codeRows(line string, w int, st styles) block {
	limit := max(1, w-len(inset)-len(codeGutter))
	indent := ansi.StringWidth(line[:len(line)-len(strings.TrimLeft(line, " \t"))])
	hang := min(indent+2, limit/2)
	var b block
	for _, pc := range wrapLine(line, limit, limit-hang, true) {
		lead := inset + codeGutter
		if pc.cont {
			lead += strings.Repeat(" ", hang)
		}
		start := len(lead)
		b.add(st.code.Render(lead+pc.text), rowMeta{start: start, end: start + ansi.StringWidth(pc.text), cont: pc.cont, joint: pc.joint})
	}
	return b
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
