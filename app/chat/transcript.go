package chat

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	return lines.rows
}

// panelSpan is where one of the user's messages sits in the transcript's
// rows: its panel's first row and how many rows it takes.
type panelSpan struct {
	start, n int
}

// transcriptPanelsLocked is the transcript's rows and where the user's
// messages sit in them, in order; mu must be held.
func (p *Pane) transcriptPanelsLocked(w int) (block, []panelSpan) {
	defer p.rowCache.sweep()
	type part struct {
		rows       block
		user, work bool // a user's message; a run of steps, which follows what is above it without a blank row
	}
	var parts []part
	var work []Step
	n := 0
	add := func(b block, user, work bool) {
		if len(b.rows) == 0 {
			return
		}
		parts = append(parts, part{rows: b, user: user, work: work})
		n += len(b.rows) + 1
	}
	for _, e := range p.conv.Entries {
		if s, ok := e.(Step); ok {
			work = append(work, s)
			continue
		}
		add(p.workRows(work, w, false), false, true)
		work = nil
		_, user := e.(UserMessage)
		add(p.entryRowsLocked(e, w), user, false)
	}
	add(p.workRows(work, w, true), false, true) // the last run of steps, the one the agent may be adding to
	// the rows are gathered first and copied once: a frame builds the whole transcript
	out := block{rows: make([]string, 0, n), meta: make([]rowMeta, 0, n)}
	var spans []panelSpan
	for i, pt := range parts {
		if i > 0 && !pt.work {
			out.plain("")
		}
		if pt.user {
			spans = append(spans, panelSpan{start: len(out.rows), n: len(pt.rows.rows)})
		}
		out.join(pt.rows)
	}
	return out, spans
}

// entryRowsLocked turns one entry other than a step into rows of at most w
// cells, wrapped, never cut; mu must be held.
func (p *Pane) entryRowsLocked(e Entry, w int) block {
	st := p.styles
	switch e := e.(type) {
	case UserMessage:
		return p.rowCache.rows(rowKey{kind: 'u', text: e.Text + attachmentsKey(e.Attachments), w: w}, func() block {
			return userPanel(e, w, st)
		})
	case AgentMessage:
		text := e.Markdown
		if e.Streaming {
			text += streamCursor
		}
		return p.rowCache.rows(rowKey{kind: 'a', text: text, w: w}, func() block {
			return markdownRows(text, w, st)
		})
	case Plan:
		return p.rowCache.rows(rowKey{kind: 'p', text: e.Markdown, w: w}, func() block {
			var b block
			b.plain(st.heading.Render(inset + "Plan"))
			b.join(markdownRows(e.Markdown, w, st))
			return b
		})
	case TodoList:
		return todoRows(e, w, st)
	case Approval:
		choice := -1 // only the approval the keys answer gets the picker
		if e.Pending && e.ID != "" && e.ID == p.pickedApprovalLocked() {
			choice = p.approvalChoice
		}
		return approvalRows(e, w, st, choice)
	case SecretRequest:
		return secretRows(e, w, st) // secret.go
	case Questions:
		var answered map[string]Answer
		if e.ID == p.answersFor {
			answered = p.answers
		}
		if !e.Pending {
			return questionRows(e, w, st, answered)
		}
		tab := 0
		if e.ID == p.answersFor {
			tab = p.questionTab
		}
		return questionPromptRows(e, w, st, answered, tab, p.questionChoice)
	case Failure:
		return insetRows(wrapText("✗ ", strings.TrimSpace(e.Text), w, false), st.bad) // the whole error: it may be all the user learns
	}
	return block{}
}

// insetRows is rows of wrapText drawn in style behind the inset.
func insetRows(b block, style lipgloss.Style) block {
	return b.styled(func(r string) string {
		return style.Render(inset + r)
	})
}

// workRows shows a run of the agent's steps: one quiet summary line, or
// each step in full after /work. last is the run that ends the transcript:
// while the agent works, it is the one being added to.
func (p *Pane) workRows(work []Step, w int, last bool) block {
	var b block
	if len(work) == 0 {
		return b
	}
	if !p.showWork {
		b.join(insetRows(wrapText("", workSummary(work), w, false), p.styles.muted)) // what it did; /work shows the steps
		b.join(p.liveStepRows(work, w, last && p.workingLocked()))
		return b
	}
	for _, s := range work {
		lead, text := s.line()
		b.join(insetRows(wrapText(lead, text, w, false), p.styles.muted))
	}
	return b
}

// The live steps under a collapsed summary: how many at most, and how many
// rows a thought still being written keeps of its end.
const (
	liveSteps       = 3
	liveThoughtRows = 3
)

// liveStepRows shows under a collapsed summary what the agent is doing right
// now, the newest last: the command it runs, the thought it writes. A count
// alone left the user unsure anything happened. Those are the steps still
// going; for the run the working agent adds to (latest), its last steps
// whether or not they finished, as T3's own client shows them, so the gap
// between two steps still says what came last. Once the agent stops they
// go, and the count stands alone.
func (p *Pane) liveStepRows(work []Step, w int, latest bool) block {
	var going []Step
	for _, s := range work {
		if latest || s.State.going() {
			going = append(going, s)
		}
	}
	if len(going) > liveSteps {
		going = going[len(going)-liveSteps:]
	}
	var b block
	for _, s := range going {
		if s.Kind == StepThought {
			b.join(insetRows(thoughtTail(s.Text, w), p.styles.muted))
			continue
		}
		lead, _ := s.line()
		text := firstLine(s.Text)
		if text == "" {
			text = s.noun()
		}
		b.join(insetRows(wrapText(lead, text+s.suffix(), w, false), p.styles.text))
	}
	return b
}

// thoughtTail is the end of a thought still being written: what it says
// now, in at most liveThoughtRows rows, "…" leading when the start is cut.
func thoughtTail(text string, w int) block {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return wrapText("✻ ", "thinking…", w, false) // a thought shows only as it is written, or not at all
	}
	b := wrapText("✻ ", text, w, false)
	if len(b.rows) <= liveThoughtRows {
		return b
	}
	// keep the end, "…" for the cut start; words wrap short of the width, so
	// the end kept shrinks until it fits and the first row keeps its ✻
	r := []rune(text)
	room := min(len(r), max(1, (w-len(inset)-2)*liveThoughtRows-1))
	for {
		b = wrapText("✻ ", "…"+strings.TrimLeft(string(r[len(r)-room:]), " "), w, false)
		if len(b.rows) <= liveThoughtRows || room == 1 {
			return b
		}
		room = max(1, room-max(1, room/10))
	}
}

// stepPhrases say what the agent did n times, for each noun the summary
// counts.
var stepPhrases = map[string]func(n int) string{
	"command":  func(n int) string { return fmt.Sprintf("ran %d command%s", n, plural(n)) },
	"edit":     func(n int) string { return fmt.Sprintf("changed %d file%s", n, plural(n)) },
	"search":   func(n int) string { return fmt.Sprintf("searched %d time%s", n, plural(n)) },
	"tool":     func(n int) string { return fmt.Sprintf("used %d tool%s", n, plural(n)) },
	"subagent": func(n int) string { return fmt.Sprintf("started %d subagent%s", n, plural(n)) },
	"step":     func(n int) string { return fmt.Sprintf("took %d other step%s", n, plural(n)) },
	"thought": func(n int) string {
		if n == 1 {
			return "thought"
		}
		return fmt.Sprintf("thought %d times", n)
	},
}

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

// workSummary says what a run of steps did, the way T3's own client does:
// "+ Changed 1 file and ran 5 commands", in the order the kinds first came,
// thoughts counted only when the run is nothing else, "⋯" instead of "+"
// while any of it is still going.
func workSummary(work []Step) string {
	counts := map[string]int{}
	var order []string
	going := false
	for _, s := range work {
		noun := s.noun()
		if counts[noun] == 0 {
			order = append(order, noun)
		}
		counts[noun]++
		if s.State.going() {
			going = true
		}
	}
	if len(order) > 1 {
		order = slices.DeleteFunc(order, func(n string) bool { return n == "thought" })
	}
	parts := make([]string, 0, len(order))
	for _, noun := range order {
		parts = append(parts, stepPhrases[noun](counts[noun]))
	}
	said := strings.Join(parts, ", ")
	if len(parts) > 1 {
		said = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
	mark := "+"
	if going {
		mark = "⋯"
	}
	return mark + " " + capitalize(said) + " · /work shows them"
}

// line is the step as /work shows it, with OpenCode's glyphs: $ for a
// command, ✎ an edit, ✱ a search, ◈ the web, → a tool. The glyph leads,
// and the text, all of it, hangs under itself as it wraps.
func (s Step) line() (lead, text string) {
	text = strings.TrimSpace(s.Text)
	switch s.Kind {
	case StepThought:
		if s.State.going() {
			return "· ", "thinking… " + text
		}
		return "· ", "thought"
	case StepNotice:
		return "ℹ ", text
	case StepInterrupt:
		return "■ ", text
	case StepCompaction:
		return "⇅ ", text
	case StepCommand:
		return "$ ", text + s.suffix()
	case StepEdit:
		return "✎ ", text + s.suffix()
	case StepSearch:
		return "✱ ", text + s.suffix()
	case StepWeb:
		return "◈ ", text + s.suffix()
	case StepTool:
		return "→ ", text + s.suffix()
	case StepSubagent:
		return "↳ ", text + s.suffix()
	default:
		return "· ", text + s.suffix()
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
func userPanel(m UserMessage, w int, st styles) block {
	lines := panelBlock(m.Text, w)
	for i, r := range lines.rows {
		lines.rows[i] = st.rail.Render(rail) + st.panel.Render(panelText(r, w))
	}
	if len(m.Attachments) == 0 {
		return lines
	}
	// the attachments go under the text, muted on the panel, above its blank last row
	last := len(lines.rows) - 1
	out := block{rows: lines.rows[:last:last], meta: lines.meta[:last:last]}
	atts := attachmentRows(m.Attachments, w)
	for i, r := range atts.rows {
		out.add(st.rail.Render(rail)+st.queued.Render(panelText(r, w)), atts.meta[i])
	}
	out.add(lines.rows[last], lines.meta[last])
	return out
}

// attachmentRows lists what a message carries, one row each, as the user's
// panel shows them: its name and size, and its kind unless it is a file.
func attachmentRows(atts []Attachment, w int) block {
	inner := max(1, w-4)
	var b block
	for _, a := range atts {
		row := "📎 " + a.Name + " · " + sizeText(a.Size)
		if a.Kind != AttachedFile {
			row += " · " + a.Kind
		}
		row = ansi.Truncate(row, inner, "…")
		b.add(row, rowMeta{start: panelTextStart, end: panelTextStart + ansi.StringWidth(row)})
	}
	return b
}

// attachmentsKey tells apart, for the row cache, messages of the same text
// that carry different attachments.
func attachmentsKey(atts []Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, a := range atts {
		fmt.Fprintf(&b, "\x00%s\x00%s\x00%d", a.Kind, a.Name, a.Size)
	}
	return b.String()
}

// panelTextStart is the column a panel row's text starts at: past the rail
// and two cells of padding.
const panelTextStart = 3

// panelBlock is the user's message wrapped for a panel w cells wide, one
// blank row above and below it, without styles.
func panelBlock(text string, w int) block {
	inner := max(1, w-4)
	var b block
	b.plain("")
	for para := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		for _, pc := range wrapLine(para, inner, inner, false) {
			b.add(pc.text, rowMeta{start: panelTextStart, end: panelTextStart + ansi.StringWidth(pc.text), cont: pc.cont, joint: pc.joint})
		}
	}
	b.plain("")
	return b
}

// panelLines is the rows of panelBlock.
func panelLines(text string, w int) []string {
	return panelBlock(text, w).rows
}

// panelText is one row of a panel's text, indented and filling the panel,
// which takes the pane's width but the rail's cell.
func panelText(r string, w int) string {
	return fit("  "+r, max(1, w-1))
}

func todoRows(list TodoList, w int, st styles) block {
	var b block
	for _, s := range list.Steps {
		mark := "☐"
		switch s.State {
		case Done:
			mark = "☑"
		case Running:
			mark = "▸"
		default:
		}
		b.join(insetRows(wrapText(mark+" ", strings.TrimSpace(s.Text), w, false), st.muted))
	}
	return b
}

// approvalChoices are the approval picker's rows, in order.
var approvalChoices = []struct {
	label    string
	decision Decision
}{{"Accept", Accept}, {"Decline", Decline}}

// approvalRows shows what the agent asks to do in full, wrapped: a command
// to approve has to be read whole, and so does one approved already. The
// approval the keys answer (choice >= 0) is drawn as a picker, its
// highlighted row a bar as a question's is: a hint to type /accept alone
// was easy to miss.
func approvalRows(a Approval, w int, st styles, choice int) block {
	prompt := strings.TrimSpace(a.Prompt)
	if !a.Pending {
		return insetRows(wrapText("", "approval: "+prompt, w, false), st.muted)
	}
	b := insetRows(wrapText("⚠ ", "approval needed: "+prompt, w, false), st.warn)
	if a.Reason != "" {
		b.join(wrapLines(a.Reason, w))
	}
	if choice < 0 {
		b.plain(st.muted.Render(ansi.Truncate(inset+"  type /accept or /decline", w, "…")))
		return b
	}
	b.plain("")
	for i, c := range approvalChoices {
		cursor, style := "  ", st.text
		if i == choice {
			cursor, style = "› ", st.selected
		}
		row := fmt.Sprintf("%s%d) %s", cursor, i+1, c.label)
		if i == choice {
			row = fit(row, max(1, w-len(inset)))
		}
		b.plain(inset + style.Render(row))
	}
	b.plain("")
	b.plain(keyHints(w, st, "↑/↓ choose", "enter confirm", "or type /accept · /decline", "or a message to the agent")...)
	return b
}

// questionRows lists the agent's questions with numbered options. Once a
// prompt is answered or dropped it shrinks to its questions, each in full.
func questionRows(q Questions, w int, st styles, answered map[string]Answer) block {
	var out block
	if !q.Pending {
		for _, question := range q.Questions {
			out.join(insetRows(wrapText("", "question: "+strings.TrimSpace(question.Text), w, false), st.muted))
		}
		return out
	}
	for _, question := range q.Questions {
		mark := "? "
		if _, ok := answered[question.ID]; ok {
			mark = "✓ "
		}
		out.join(insetRows(wrapText(mark, question.Text, w, false), st.warn))
		for i, o := range question.Options {
			line := fmt.Sprintf("%d) %s", i+1, o.Label)
			if o.Description != "" {
				line += " — " + o.Description
			}
			out.join(insetRows(wrapText("    ", line, w, false), st.muted))
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
		if len(next.Options) > 0 {
			hint += " · /answer <text> for your own"
		}
	}
	out.join(wrapLines(hint+"\n  Type a number or your own answer, then Enter.\n  /dismiss skips the question.", w).styled(func(r string) string {
		return st.muted.Render(r)
	}))
	return out
}
