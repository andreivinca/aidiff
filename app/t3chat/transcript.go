package t3chat

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/t3client"
)

// maxInputRows is how tall the input line grows before it scrolls.
const maxInputRows = 5

const (
	inset       = "   " // the assistant's prose and the agent's work start three cells in, as in OpenCode
	rail        = "┃"   // the user's messages carry a rail in the agent's color
	placeholder = "message the agent…"
)

// styles are the pane's colors, OpenCode's default palette; plain when color
// is off.
type styles struct {
	text, muted, rail, panel, composer, cursor, heading, code, warn, bad, bold lipgloss.Style
}

func newStyles(color bool) styles {
	plain := lipgloss.NewStyle()
	if !color {
		return styles{text: plain, muted: plain, rail: plain, panel: plain, composer: plain, cursor: plain, heading: plain, code: plain,
			warn: plain, bad: plain, bold: plain}
	}
	return styles{
		text:     lipgloss.NewStyle().Foreground(lipgloss.Color("#eeeeee")),
		muted:    lipgloss.NewStyle().Foreground(lipgloss.Color("#808080")),
		rail:     lipgloss.NewStyle().Foreground(lipgloss.Color("#5c9cf5")),
		panel:    lipgloss.NewStyle().Foreground(lipgloss.Color("#eeeeee")).Background(lipgloss.Color("#141414")),
		composer: lipgloss.NewStyle().Foreground(lipgloss.Color("#eeeeee")).Background(lipgloss.Color("#202020")),
		cursor:   lipgloss.NewStyle().Reverse(true),
		heading:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#9d7cd8")),
		code:     lipgloss.NewStyle().Foreground(lipgloss.Color("#7fd88f")),
		warn:     lipgloss.NewStyle().Foreground(lipgloss.Color("#f5a742")),
		bad:      lipgloss.NewStyle().Foreground(lipgloss.Color("#e06c75")),
		bold:     lipgloss.NewStyle().Bold(true),
	}
}

// renderLocked lays the pane out: header, transcript, composer; mu must
// be held. The transcript sticks to the bottom like a chat.
func (p *Pane) renderLocked(showCursor bool) string {
	w, h := p.width, p.height
	if p.stop.runID != "" {
		return p.renderStopLocked(w, h)
	}
	headerH, gapH := 0, 0
	if h > 1 {
		headerH = 1
	}
	if h >= 7 {
		gapH = 1
	}
	footer := p.composerRowsLocked(w, max(1, h-headerH-gapH-1), showCursor)
	pending := p.pendingRowsLocked(w, min(6, max(0, h-headerH-gapH-len(footer)-1)))
	bodyH := max(0, h-headerH-gapH-len(footer)-len(pending))
	p.bodyH = bodyH

	rows := make([]string, 0, h)
	if headerH > 0 {
		rows = append(rows, p.headerLocked(w))
	}
	if p.activity.open {
		rows = append(rows, p.activityRowsLocked(w, bodyH)...)
	} else {
		lines := p.transcriptLocked(w)
		if p.scroll > len(lines)-bodyH {
			p.scroll = max(0, len(lines)-bodyH)
		}
		end := len(lines) - p.scroll
		start := max(0, end-bodyH)
		for i := end - start; i < bodyH; i++ {
			rows = append(rows, "") // blank rows above a short transcript
		}
		rows = append(rows, lines[start:end]...)
	}
	rows = append(rows, pending...)
	if gapH > 0 {
		rows = append(rows, p.styles.muted.Render(ansi.Truncate(inset+p.activityStatusLocked(), w, "…")))
	}
	rows = append(rows, footer...)
	for len(rows) < h {
		rows = append(rows, "")
	}
	for i := range rows {
		rows[i] = fit(rows[i], w)
	}
	return strings.Join(rows[:h], "\n")
}

// headerLocked is the title and the thread's state; mu must be held.
func (p *Pane) headerLocked(w int) string {
	state := p.link
	if state == "" {
		state = p.stateLocked()
	}
	if p.scroll > 0 {
		state += fmt.Sprintf(" · ↑%d · end jumps down", p.scroll)
	}
	title := p.title
	if title == "" {
		title = "T3 Code"
	}
	// the state keeps its place; a long title yields to it, down to a third of the width
	room := w - ansi.StringWidth(state) - 3
	if room < min(ansi.StringWidth(title), max(8, w/3)) {
		room = max(1, min(ansi.StringWidth(title), max(8, w/3)))
		state = ansi.Truncate(state, max(1, w-room-3), "…")
	}
	return p.styles.bold.Render(ansi.Truncate(title, room, "…")) + " · " + p.styles.muted.Render(state)
}

// stateLocked derives the agent's state from its runs and open requests.
func (p *Pane) stateLocked() string {
	for _, r := range p.requests {
		if r.Pending() {
			return "waiting for you"
		}
	}
	var latest *t3client.Run
	for id := range p.runs {
		r := p.runs[id]
		if latest == nil || r.Ordinal > latest.Ordinal {
			latest = &r
		}
	}
	switch {
	case latest == nil:
		return "idle"
	case latest.Status == "waiting":
		return "waiting for you"
	case latest.Active():
		return "running"
	case latest.Status == "completed":
		return "idle"
	}
	return latest.Status
}

// transcriptLocked renders every item, newest last. A run of the agent's
// work collapses to one summary line unless /work expanded it; mu must be
// held.
func (p *Pane) transcriptLocked(w int) []string {
	var out []string
	queued := p.queuedMessagesLocked()
	queuedIDs := make(map[string]bool, len(queued))
	for _, m := range queued {
		queuedIDs[m.id] = true
	}
	var work []t3client.TurnItem
	flush := func() {
		if len(work) == 0 {
			return
		}
		if p.showWork {
			for _, it := range work {
				out = append(out, p.styles.muted.Render(ansi.Truncate(inset+workLine(it), w, "…")))
			}
		} else {
			out = append(out, p.styles.muted.Render(ansi.Truncate(inset+workSummary(work), w, "…")))
		}
		work = nil
	}
	for _, it := range p.items {
		if it.Type == "user_message" && queuedIDs[it.MessageID] {
			continue
		}
		if it.Type == "checkpoint" { // T3's own turn markers carry nothing to show
			continue
		}
		if isWork(it) {
			work = append(work, it)
			continue
		}
		flush()
		var answered map[string]any
		if it.Type == "user_input_request" && it.RequestID == p.answersFor {
			answered = p.answers
		}
		lines := renderItem(it, w, p.styles, answered)
		if len(lines) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, lines...)
	}
	flush()
	return out
}

// isWork tells the agent's activity, shown as one quiet summary, from what
// the conversation shows in full: messages, plans, todo lists, requests and
// errors.
func isWork(it t3client.TurnItem) bool {
	switch it.Type {
	case "user_message", "assistant_message", "proposed_plan", "todo_list", "approval_request", "user_input_request", "error":
		return false
	}
	return true
}

// workSummary counts a run of work the way OpenCode does: "+ 2 commands,
// 1 edit, 3 thoughts", zero counts left out, "⋯" instead of "+" while any of
// it is still going.
func workSummary(work []t3client.TurnItem) string {
	counts := map[string]int{}
	running := false
	for _, it := range work {
		switch it.Type {
		case "command_execution":
			counts["command"]++
		case "file_change":
			counts["edit"]++
		case "reasoning":
			counts["thought"]++
		case "file_search", "web_search":
			counts["search"]++
		case "dynamic_tool":
			counts["tool"]++
		case "subagent":
			counts["subagent"]++
		default:
			counts["step"]++
		}
		if !it.Done() && it.Status != "idle" {
			running = true
		}
	}
	var parts []string
	for _, kind := range []string{"command", "edit", "thought", "search", "tool", "subagent", "step"} {
		if n := counts[kind]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s%s", n, kind, plural(n)))
		}
	}
	mark := "+"
	if running {
		mark = "⋯"
	}
	return mark + " " + strings.Join(parts, ", ") + " · /work shows them"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// renderItem turns one conversation item into rows of at most w cells.
// answered holds the answers given so far to a question request.
func renderItem(it t3client.TurnItem, w int, st styles, answered map[string]any) []string {
	switch it.Type {
	case "user_message":
		return userPanel(it.Text, w, st)
	case "assistant_message":
		text := it.Text
		if it.Streaming || it.Status == "running" {
			text += "▍"
		}
		return markdownRows(text, w, st)
	case "proposed_plan":
		head := st.heading.Render(inset + "Plan")
		return append([]string{head}, markdownRows(it.Markdown, w, st)...)
	case "todo_list":
		return renderTodo(it, w, st)
	case "approval_request":
		return renderApproval(it, w, st)
	case "user_input_request":
		return renderQuestions(it, w, st, answered)
	case "error":
		return []string{st.bad.Render(ansi.Truncate(inset+"✗ "+firstLine(it.FailureText()), w, "…"))}
	}
	return []string{st.muted.Render(ansi.Truncate(inset+workLine(it), w, "…"))}
}

// userPanel is the user's message as OpenCode draws it: a raised panel with
// a rail in the agent's color, one blank row above and below the text.
func userPanel(text string, w int, st styles) []string {
	inner := max(1, w-4)
	rows := []string{""}
	for para := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(para) == "" {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, strings.Split(ansi.Wrap(para, inner, ""), "\n")...)
	}
	rows = append(rows, "")
	for i, r := range rows {
		rows[i] = st.rail.Render(rail) + st.panel.Render(fit("  "+r, max(1, w-1)))
	}
	return rows
}

// workLine sums up one of the agent's work items in a line, with OpenCode's
// glyphs: $ for a command, ✎ an edit, ✱ a search, ◈ the web, → a tool.
func workLine(it t3client.TurnItem) string {
	suffix := statusSuffix(it)
	switch it.Type {
	case "reasoning":
		if it.Streaming || !it.Done() {
			return "· thinking… " + firstLine(it.Text)
		}
		return "· thought"
	case "command_execution":
		return "$ " + firstLine(it.InputText()) + suffix
	case "file_change":
		s := "✎ " + it.FileName
		if it.Additions != nil || it.Deletions != nil {
			s += fmt.Sprintf(" +%d −%d", count(it.Additions), count(it.Deletions))
		}
		return s + suffix
	case "file_search":
		return "✱ " + it.Pattern + suffix
	case "web_search":
		return "◈ " + strings.Join(it.Patterns, ", ") + suffix
	case "dynamic_tool":
		name := "tool"
		if it.ToolName != nil && *it.ToolName != "" {
			name = *it.ToolName
		}
		return "→ " + name + " " + firstLine(it.InputText()) + suffix
	case "subagent":
		return "↳ " + firstLine(it.Prompt) + suffix
	case "system_notice":
		return "ℹ " + firstLine(it.Message)
	case "run_interrupt_request", "run_interrupt_result":
		return "■ " + firstLine(it.Message)
	case "compaction":
		return "⇅ context compacted"
	}
	label := strings.ReplaceAll(it.Type, "_", " ")
	if it.Title != nil && *it.Title != "" {
		label += ": " + *it.Title
	}
	return "· " + label + suffix
}

func renderTodo(it t3client.TurnItem, w int, st styles) []string {
	out := make([]string, 0, len(it.Steps))
	for _, s := range it.Steps {
		mark := "☐"
		switch s.Status {
		case "completed":
			mark = "☑"
		case "running":
			mark = "▸"
		}
		out = append(out, st.muted.Render(ansi.Truncate(inset+mark+" "+firstLine(s.Text), w, "…")))
	}
	return out
}

func renderApproval(it t3client.TurnItem, w int, st styles) []string {
	if it.Done() {
		return []string{st.muted.Render(ansi.Truncate(inset+"approval: "+firstLine(it.Prompt), w, "…"))}
	}
	out := []string{st.warn.Render(ansi.Truncate(inset+"⚠ approval needed: "+firstLine(it.Prompt), w, "…"))}
	if it.Reason != "" {
		out = append(out, wrapLines(it.Reason, w)...)
	}
	return append(out, st.muted.Render(ansi.Truncate(inset+"  type /accept or /decline", w, "…")))
}

// renderQuestions lists the agent's questions with numbered options. Once a
// request is answered or dropped it collapses to a line.
func renderQuestions(it t3client.TurnItem, w int, st styles, answered map[string]any) []string {
	if it.Done() {
		return []string{st.muted.Render(ansi.Truncate(inset+"question: "+firstQuestion(it), w, "…"))}
	}
	var out []string
	for _, q := range it.Questions {
		mark := "? "
		if _, ok := answered[q.ID]; ok {
			mark = "✓ "
		}
		out = append(out, st.warn.Render(ansi.Truncate(inset+mark+firstLine(q.Question), w, "…")))
		for i, o := range q.Options {
			line := fmt.Sprintf("%s    %d) %s", inset, i+1, o.Label)
			if o.Description != "" {
				line += " — " + firstLine(o.Description)
			}
			out = append(out, st.muted.Render(ansi.Truncate(line, w, "…")))
		}
	}
	hint := "  /1, /2… picks an option"
	if q := nextQuestion(it.Questions, answered); q != nil {
		switch {
		case q.MultiSelect:
			hint = "  /answer 1,3 picks several"
		case len(q.Options) == 0:
			hint = "  /answer <text> replies"
		}
		if q.AllowCustomAnswer && len(q.Options) > 0 {
			hint += " · /answer <text> for your own"
		}
	}
	return append(out, st.muted.Render(ansi.Truncate(inset+hint+" · /dismiss drops it", w, "…")))
}

func firstQuestion(it t3client.TurnItem) string {
	if len(it.Questions) == 0 {
		return ""
	}
	return firstLine(it.Questions[0].Question)
}

// statusSuffix marks work still going or gone wrong.
func statusSuffix(it t3client.TurnItem) string {
	switch {
	case it.Status == "running" || it.Status == "pending":
		return " …"
	case it.Status == "failed":
		return " ✗"
	case it.ExitCode != nil && *it.ExitCode != 0:
		return fmt.Sprintf(" ✗ exit %d", *it.ExitCode)
	}
	return ""
}

func count(n *int) int {
	if n == nil {
		return 0
	}
	return *n
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

var (
	linkSpan = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	boldSpan = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	codeSpan = regexp.MustCompile("`([^`]+)`")
	heading  = regexp.MustCompile(`^#{1,6}\s+`)
	bullet   = regexp.MustCompile(`^(\s*)[-*]\s+`)
)

// markdownRows styles the agent's prose the way OpenCode conceals markdown:
// headings bold in the accent color, bullets as •, [links](url) as their
// text, **bold** bold, `code` green, fenced code as an indented block. Rows are inset and wrapped to w.
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
