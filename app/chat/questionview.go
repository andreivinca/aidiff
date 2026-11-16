package chat

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// customLabel is the picker's last row, which takes an answer in the user's
// own words.
const customLabel = "Type something…"

// questionPromptRows draws an open prompt as a picker: a strip of tabs when
// it asks several questions, then the current question in full, its options
// with their text wrapped under the text rather than under the number, and
// one line of keys. tab past the last question is the review of the answers.
func questionPromptRows(e Questions, w int, st styles, answered map[string]Answer, tab, choice int) []string {
	multi := len(e.Questions) > 1
	var out []string
	if multi {
		out = append(out, questionTabRows(e.Questions, tab, answered, w, st)...)
		out = append(out, "")
		if tab >= len(e.Questions) {
			return append(out, reviewRows(e.Questions, answered, w, st)...)
		}
	}
	tab = min(max(tab, 0), len(e.Questions)-1)
	q := e.Questions[tab]
	saved, isSaved := answered[q.ID]

	mark, markStyle := "? ", st.warn
	if isSaved {
		mark, markStyle = "✓ ", st.muted
	}
	for i, row := range hangRows(mark, q.Text, w) {
		if i == 0 {
			row = markStyle.Render(mark) + st.bold.Render(strings.TrimPrefix(row, mark))
		} else {
			row = st.bold.Render(row)
		}
		out = append(out, inset+row)
	}
	out = append(out, "")

	if len(q.Options) > 0 {
		out = append(out, optionRows(q, saved, isSaved, choice, w, st)...)
		out = append(out, "")
	} else if isSaved {
		out = append(out, styledHang(st.text, "  ✓ ", strings.Join(saved.Values, ", "), w)...)
		out = append(out, "")
	}
	return append(out, keyRows(q, multi, w, st)...)
}

// questionTabRows is the strip naming each question, the current one
// highlighted and an answered one ticked, then the review.
func questionTabRows(qs []Question, tab int, answered map[string]Answer, w int, st styles) []string {
	parts := make([]string, 0, len(qs)+1)
	for i, q := range qs {
		label := q.Header
		if label == "" {
			label = firstLine(q.Text)
		}
		label = ansi.Truncate(label, 32, "…")
		if _, ok := answered[q.ID]; ok {
			label += " ✓"
		}
		parts = append(parts, tabLabel(label, i == tab, st))
	}
	parts = append(parts, tabLabel("Review", tab >= len(qs), st))
	return packRows(parts, st.muted.Render(" · "), w)
}

func tabLabel(label string, current bool, st styles) string {
	if current {
		return st.selected.Render(" " + label + " ")
	}
	return st.muted.Render(label)
}

// optionRows lists a question's options and the row for an answer of the
// user's own. The highlighted row is drawn as a bar; the saved answer is
// ticked. A description is shown only when it says more than the label.
func optionRows(q Question, saved Answer, isSaved bool, choice, w int, st styles) []string {
	picked := map[string]bool{}
	if isSaved {
		for _, v := range saved.Values {
			picked[v] = true
		}
	}
	options := append(append([]Option{}, q.Options...), Option{Label: customLabel})
	custom := ""
	if isSaved && savedChoice(q, saved, true) == len(q.Options) {
		custom = strings.Join(saved.Values, ", ")
	}
	var out []string
	for i, o := range options {
		cursor := "  "
		if i == choice {
			cursor = "› "
		}
		lead := fmt.Sprintf("%s%d) ", cursor, i+1)
		label := o.Label
		if (i < len(q.Options) && picked[o.Value]) || (i == len(q.Options) && custom != "") {
			label += " ✓"
		}
		style := st.text
		if i == choice {
			style = st.selected
		}
		for _, row := range hangRows(lead, label, w) {
			if i == choice {
				row = fit(row, max(1, w-len(inset)))
			}
			out = append(out, inset+style.Render(row))
		}
		pad := strings.Repeat(" ", ansi.StringWidth(lead))
		switch {
		case i == len(q.Options) && custom != "":
			out = append(out, styledHang(st.muted, pad, "“"+custom+"”", w)...)
		case describes(o):
			out = append(out, styledHang(st.muted, pad, o.Description, w)...)
		}
	}
	return out
}

// describes reports whether an option's description adds to its label.
func describes(o Option) bool {
	d := strings.ToLower(strings.TrimSpace(o.Description))
	return d != "" && !strings.Contains(strings.ToLower(o.Label), d)
}

// reviewRows lists every question with its answer, for a last look before
// the answers are sent.
func reviewRows(qs []Question, answered map[string]Answer, w int, st styles) []string {
	out := []string{inset + st.bold.Render("Review your answers")}
	for _, q := range qs {
		out = append(out, "")
		a, ok := answered[q.ID]
		mark, markStyle := "○ ", st.warn
		if ok {
			mark, markStyle = "✓ ", st.muted
		}
		for i, row := range hangRows(mark, firstLine(q.Text), w) {
			if i == 0 {
				row = markStyle.Render(mark) + st.text.Render(strings.TrimPrefix(row, mark))
			} else {
				row = st.text.Render(row)
			}
			out = append(out, inset+row)
		}
		value := "not answered yet"
		if ok {
			value = strings.Join(a.Values, ", ")
		}
		out = append(out, styledHang(st.muted, "  → ", value, w)...)
	}
	out = append(out, "")
	return append(out, keyHints(w, st, "enter submit all", "tab / shift+tab revise", "/dismiss skips")...)
}

// keyRows is the line of keys under a question.
func keyRows(q Question, multi bool, w int, st styles) []string {
	var keys []string
	if len(q.Options) > 0 {
		keys = append(keys, "↑/↓ choose", "enter select", "or type your own answer")
		if q.Multiple {
			keys = append(keys, "/answer 1,3 picks several")
		}
	} else {
		keys = append(keys, "type your answer, then enter")
	}
	if multi {
		keys = append(keys, "tab next")
	}
	keys = append(keys, "/dismiss skips")
	return keyHints(w, st, keys...)
}

// keyHints draws key hints muted, never breaking a hint across rows.
func keyHints(w int, st styles, keys ...string) []string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = st.muted.Render(k)
	}
	return packRows(parts, st.muted.Render(" · "), w)
}

// packRows joins styled parts with sep, starting a new inset row instead of
// splitting a part.
func packRows(parts []string, sep string, w int) []string {
	limit := max(1, w-len(inset))
	var out []string
	row := ""
	for _, part := range parts {
		switch {
		case row == "":
			row = part
		case ansi.StringWidth(row+sep+part) <= limit:
			row += sep + part
		default:
			out = append(out, inset+row)
			row = part
		}
	}
	if row != "" {
		out = append(out, inset+row)
	}
	return out
}

// hangRows wraps text after lead so the rows after the first continue under
// the text, not under the lead. Rows carry no inset.
func hangRows(lead, text string, w int) []string {
	lw := ansi.StringWidth(lead)
	limit := max(1, w-len(inset)-lw)
	pad := strings.Repeat(" ", lw)
	var out []string
	for para := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		for l := range strings.SplitSeq(ansi.Wrap(para, limit, ""), "\n") {
			if len(out) == 0 {
				out = append(out, lead+l)
			} else {
				out = append(out, pad+l)
			}
		}
	}
	return out
}

// styledHang is hangRows inset and drawn in one style.
func styledHang(style lipgloss.Style, lead, text string, w int) []string {
	rows := hangRows(lead, text, w)
	for i, row := range rows {
		rows[i] = inset + style.Render(row)
	}
	return rows
}

// savedChoice is the picker row of a saved answer: its option, or the row
// for an answer of the user's own when it matches none. ok false starts at
// the first option.
func savedChoice(q Question, a Answer, ok bool) int {
	if !ok || len(a.Values) == 0 {
		return 0
	}
	for i, o := range q.Options {
		if o.Value == a.Values[0] {
			return i
		}
	}
	return len(q.Options)
}
