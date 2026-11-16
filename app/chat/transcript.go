package chat

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// maxInputRows is how tall the input line grows before it scrolls.
const maxInputRows = 5

const (
	inset       = "   " // the agent's prose and work start three cells in, as in OpenCode
	rail        = "┃"   // the user's messages carry a rail in the agent's color
	placeholder = "message the agent…"
)

// transcriptLocked renders every entry, newest last. A run of the agent's
// steps collapses to one summary line unless /work expanded it; mu must be
// held.
func (p *Pane) transcriptLocked(w int) []string {
	lines, _ := p.transcriptPanelsLocked(w)
	return lines
}

// panelSpan is where one of the user's messages sits in the transcript's
// rows: its panel's first row and how many rows it takes.
type panelSpan struct {
	start, n int
}

// transcriptPanelsLocked is the transcript's rows and where the user's
// messages sit in them, in order; mu must be held.
func (p *Pane) transcriptPanelsLocked(w int) ([]string, []panelSpan) {
	var out []string
	var spans []panelSpan
	var work []Step
	for _, e := range p.conv.Entries {
		if s, ok := e.(Step); ok {
			work = append(work, s)
			continue
		}
		out = append(out, p.workRows(work, w)...)
		work = nil
		lines := p.entryRowsLocked(e, w)
		if len(lines) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		if _, user := e.(UserMessage); user {
			spans = append(spans, panelSpan{start: len(out), n: len(lines)})
		}
		out = append(out, lines...)
	}
	return append(out, p.workRows(work, w)...), spans
}

// entryRowsLocked turns one entry other than a step into rows of at most w
// cells; mu must be held.
func (p *Pane) entryRowsLocked(e Entry, w int) []string {
	st := p.styles
	switch e := e.(type) {
	case UserMessage:
		return userPanel(e.Text, w, st)
	case AgentMessage:
		text := e.Markdown
		if e.Streaming {
			text += "▍"
		}
		return markdownRows(text, w, st)
	case Plan:
		head := st.heading.Render(inset + "Plan")
		return append([]string{head}, markdownRows(e.Markdown, w, st)...)
	case TodoList:
		return todoRows(e, w, st)
	case Approval:
		return approvalRows(e, w, st)
	case Questions:
		var answered map[string]Answer
		if e.ID == p.answersFor {
			answered = p.answers
		}
		return questionRows(e, w, st, answered)
	case Failure:
		return []string{st.bad.Render(ansi.Truncate(inset+"✗ "+firstLine(e.Text), w, "…"))}
	}
	return nil
}

// workRows shows a run of the agent's steps: one quiet summary line, or a
// line per step after /work.
func (p *Pane) workRows(work []Step, w int) []string {
	if len(work) == 0 {
		return nil
	}
	if !p.showWork {
		return []string{p.styles.muted.Render(ansi.Truncate(inset+workSummary(work), w, "…"))}
	}
	rows := make([]string, 0, len(work))
	for _, s := range work {
		rows = append(rows, p.styles.muted.Render(ansi.Truncate(inset+s.line(), w, "…")))
	}
	return rows
}

// stepNouns are what the work summary counts, in its order.
var stepNouns = []string{"command", "edit", "thought", "search", "tool", "subagent", "step"}

// noun is what the work summary counts the step as.
func (s Step) noun() string {
	switch s.Kind {
	case StepCommand:
		return "command"
	case StepEdit:
		return "edit"
	case StepThought:
		return "thought"
	case StepSearch, StepWeb:
		return "search"
	case StepTool:
		return "tool"
	case StepSubagent:
		return "subagent"
	default:
		return "step"
	}
}

// workSummary counts a run of steps the way OpenCode does: "+ 2 commands,
// 1 edit, 3 thoughts", zero counts left out, "⋯" instead of "+" while any of
// it is still going.
func workSummary(work []Step) string {
	counts := map[string]int{}
	going := false
	for _, s := range work {
		counts[s.noun()]++
		if s.State.going() {
			going = true
		}
	}
	var parts []string
	for _, noun := range stepNouns {
		if n := counts[noun]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s%s", n, noun, plural(n)))
		}
	}
	mark := "+"
	if going {
		mark = "⋯"
	}
	return mark + " " + strings.Join(parts, ", ") + " · /work shows them"
}

// line sums up the step in a line, with OpenCode's glyphs: $ for a command,
// ✎ an edit, ✱ a search, ◈ the web, → a tool.
func (s Step) line() string {
	text := firstLine(s.Text)
	switch s.Kind {
	case StepThought:
		if s.State.going() {
			return "· thinking… " + text
		}
		return "· thought"
	case StepNotice:
		return "ℹ " + text
	case StepInterrupt:
		return "■ " + text
	case StepCompaction:
		return "⇅ " + text
	case StepCommand:
		return "$ " + text + s.suffix()
	case StepEdit:
		return "✎ " + text + s.suffix()
	case StepSearch:
		return "✱ " + text + s.suffix()
	case StepWeb:
		return "◈ " + text + s.suffix()
	case StepTool:
		return "→ " + text + s.suffix()
	case StepSubagent:
		return "↳ " + text + s.suffix()
	default:
		return "· " + text + s.suffix()
	}
}

// suffix marks a step still going or gone wrong.
func (s Step) suffix() string {
	switch {
	case s.State == Pending || s.State == Running:
		return " …"
	case s.State == Failed && s.Why != "":
		return " ✗ " + s.Why
	case s.State == Failed:
		return " ✗"
	}
	return ""
}

// userPanel is the user's message as OpenCode draws it: a raised panel with
// a rail in the agent's color, one blank row above and below the text.
func userPanel(text string, w int, st styles) []string {
	rows := panelLines(text, w)
	for i, r := range rows {
		rows[i] = st.rail.Render(rail) + st.panel.Render(panelText(r, w))
	}
	return rows
}

// panelLines is the user's message wrapped for a panel w cells wide, one
// blank row above and below it, without styles.
func panelLines(text string, w int) []string {
	inner := max(1, w-4)
	rows := []string{""}
	for para := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(para) == "" {
			rows = append(rows, "")
			continue
		}
		rows = append(rows, strings.Split(ansi.Wrap(para, inner, ""), "\n")...)
	}
	return append(rows, "")
}

// panelText is one row of a panel's text, indented and filling the panel,
// which takes the pane's width but the rail's cell.
func panelText(r string, w int) string {
	return fit("  "+r, max(1, w-1))
}

func todoRows(list TodoList, w int, st styles) []string {
	out := make([]string, 0, len(list.Steps))
	for _, s := range list.Steps {
		mark := "☐"
		switch s.State {
		case Done:
			mark = "☑"
		case Running:
			mark = "▸"
		default:
		}
		out = append(out, st.muted.Render(ansi.Truncate(inset+mark+" "+firstLine(s.Text), w, "…")))
	}
	return out
}

func approvalRows(a Approval, w int, st styles) []string {
	if !a.Pending {
		return []string{st.muted.Render(ansi.Truncate(inset+"approval: "+firstLine(a.Prompt), w, "…"))}
	}
	out := []string{st.warn.Render(ansi.Truncate(inset+"⚠ approval needed: "+firstLine(a.Prompt), w, "…"))}
	if a.Reason != "" {
		out = append(out, wrapLines(a.Reason, w)...)
	}
	return append(out, st.muted.Render(ansi.Truncate(inset+"  type /accept or /decline", w, "…")))
}

// questionRows lists the agent's questions with numbered options. Once a
// prompt is answered or dropped it collapses to a line.
func questionRows(q Questions, w int, st styles, answered map[string]Answer) []string {
	if !q.Pending {
		return []string{st.muted.Render(ansi.Truncate(inset+"question: "+firstQuestion(q), w, "…"))}
	}
	var out []string
	for _, question := range q.Questions {
		mark := "? "
		if _, ok := answered[question.ID]; ok {
			mark = "✓ "
		}
		out = append(out, st.warn.Render(ansi.Truncate(inset+mark+firstLine(question.Text), w, "…")))
		for i, o := range question.Options {
			line := fmt.Sprintf("%s    %d) %s", inset, i+1, o.Label)
			if o.Description != "" {
				line += " — " + firstLine(o.Description)
			}
			out = append(out, st.muted.Render(ansi.Truncate(line, w, "…")))
		}
	}
	hint := "  /1, /2… picks an option"
	if next := nextQuestion(q.Questions, answered); next != nil {
		switch {
		case next.Multiple:
			hint = "  /answer 1,3 picks several"
		case len(next.Options) == 0:
			hint = "  /answer <text> replies"
		}
		if next.OwnWords && len(next.Options) > 0 {
			hint += " · /answer <text> for your own"
		}
	}
	return append(out, st.muted.Render(ansi.Truncate(inset+hint+" · /dismiss drops it", w, "…")))
}

func firstQuestion(q Questions) string {
	if len(q.Questions) == 0 {
		return ""
	}
	return firstLine(q.Questions[0].Text)
}
