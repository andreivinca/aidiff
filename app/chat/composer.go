package chat

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// composerRowsLocked fits the popup, input panel and hints into budget rows.
// Small panes give up padding and popup rows before hiding the input cursor.
func (p *Pane) composerRowsLocked(w, budget int, showCursor bool) []string {
	meta, hints := budget >= 2, budget >= 3 && !p.hintsOut
	reserved := 0
	if meta {
		reserved++
	}
	if hints {
		reserved++
	}
	inner := inputWidth(w)
	input := p.inputRowsLocked(inner, min(maxInputRows, budget-reserved), showCursor)
	room := budget - reserved - len(input)
	var notes []string
	if p.note != "" && room > 0 {
		notes = noteRows(p.note, w, p.styles.warn)
		notes = notes[:min(len(notes), room)]
		room -= len(notes)
	}
	popup := p.suggestionRowsLocked(w, min(maxSuggestionRows, room))
	room -= len(popup)
	rows := append([]string(nil), popup...)
	// Separate the input panel from the status row when space allows.
	spaced := room >= 4
	if spaced {
		rows = append(rows, "")
	}
	if room >= 2 {
		rows = append(rows, p.composerRowLocked("", w, showCursor))
	}
	for _, line := range input {
		rows = append(rows, p.composerRowLocked(line, w, showCursor))
	}
	if room >= 3 {
		rows = append(rows, p.composerRowLocked("", w, showCursor))
	}
	if meta {
		rows = append(rows, p.composerRowLocked(p.composerSettingsLocked(inner), w, showCursor))
	}
	if room >= 2 {
		rows = append(rows, p.composerEdgeLocked(w, showCursor))
	}
	if hints {
		rows = append(rows, p.composerHintsLocked(w, len(popup) > 0))
	}
	return append(rows, notes...)
}

// composerRowLocked paints the entire panel width, including trailing
// spaces. text is drawn with the composer palette.
func (p *Pane) composerRowLocked(text string, w int, focused bool) string {
	if w < 6 {
		return p.composer.fill(text, w)
	}
	accent := p.styles.rail
	if p.conv.Settings.Attention {
		accent = p.styles.warn
	}
	if !focused {
		accent = p.styles.muted
	}

	return accent.Render("│") + " " + p.composer.fill(p.composer.text.Render("  ")+text, w-2)
}

// composerEdgeLocked uses half-height blocks to soften the panel's padding.
// The unpainted half retains the terminal background.
func (p *Pane) composerEdgeLocked(w int, focused bool) string {
	if w < 6 {
		return p.composerRowLocked("", w, focused)
	}
	if _, colored := p.composer.text.GetBackground().(lipgloss.Color); !colored {
		return p.composerRowLocked("", w, focused)
	}

	edge := lipgloss.NewStyle().Foreground(composerBackground)
	return "  " + edge.Render(strings.Repeat("▀", w-2))
}

// composerSettingsLocked shows how the agent runs: its mode, its model and
// further details. The model keeps its place as narrow panes drop the
// details, the least important first.
func (p *Pane) composerSettingsLocked(w int) string {
	c, set := p.composer, p.conv.Settings
	accent := c.rail
	if set.Attention {
		accent = c.warn
	}
	lead, leadW := "", 0
	if set.Mode != "" {
		lead, leadW = accent.Render(set.Mode)+c.text.Render("  "), ansi.StringWidth(set.Mode)+2
	}
	details := set.Details
	for len(details) > 0 && leadW+ansi.StringWidth(set.Model+" "+strings.Join(details, " · ")) > w {
		details = details[:len(details)-1]
	}
	text := lead + c.text.Render(ansi.Truncate(set.Model, max(0, w-leadW), "…"))
	if len(details) > 0 {
		text += c.text.Render(" ") + c.muted.Render(strings.Join(details, " · "))
	}
	return text
}

// composerHintsLocked lists shortcuts that work in the current input state.
// Whole hints are omitted as space runs out, keeping the keys readable.
func (p *Pane) composerHintsLocked(w int, popup bool) string {
	primary, secondary := p.hintsLocked(popup)
	left := inset + p.hintText(primary)
	var right string
	for _, h := range secondary {
		candidate := p.hintText(h)
		if right != "" {
			candidate = right + "  " + candidate
		}
		if ansi.StringWidth(left)+ansi.StringWidth(candidate)+3 <= w {
			right = candidate
		}
	}
	return left + strings.Repeat(" ", max(0, w-ansi.StringWidth(left+right)-1)) + right
}

// StatusHints are the few shortcuts a host that shows them itself
// (Config.HintsOutside) puts on its status bar, as key and label pairs: the
// way into the activity view while the input is empty, then the host's own
// keys. The keys of the input itself (enter, esc, the popup's) are not
// repeated there.
func (p *Pane) StatusHints() [][2]string {
	p.lock()
	defer p.mu.Unlock()
	var out [][2]string
	if p.input.empty() && !p.activity.open {
		out = append(out, [2]string{"←", p.activityHintLocked()})
	}
	for _, h := range p.hostKeys {
		out = append(out, [2]string{h.Key, h.Label})
	}
	return out
}

// hintsLocked picks the shortcuts for the composer's state: the main one,
// shown first, and the others by importance; mu must be held.
func (p *Pane) hintsLocked(popup bool) (primary Hint, secondary []Hint) {
	switch {
	case p.activity.open:
		filter := "recent"
		if p.activity.recent {
			filter = "active"
		}
		return Hint{"↑↓", "select"}, []Hint{{"enter", "details"}, {"tab", filter}, {"esc", "back"}}
	case p.busy && p.cancelSend != nil:
		primary = Hint{"esc", "cancel upload"}
	case p.busy:
		primary = Hint{"", "sending…"}
	case p.secretLocked() != nil: // secret.go
		return Hint{"enter", "send to T3 Code"}, []Hint{{"esc", "decline"}, {"ctrl+u", "clear"}}
	case p.reading > 0:
		primary = Hint{"", "pasting…"}
	case popup:
		primary = Hint{"↑↓", "select"}
	default:
		primary = Hint{"enter", "send"}
	}
	if popup {
		return primary, []Hint{{"enter", "choose"}, {"tab", "complete"}, {"esc", "close"}}
	}
	secondary = append(slices.Clone(p.hostKeys), Hint{"shift+enter", "newline"})
	if p.runningTurnLocked() != "" {
		if primary.Key == "enter" {
			primary.Label = "queue"
		}
		if primary.Key != "esc" { // esc cancels the upload first
			secondary = append([]Hint{{"esc", "stop…"}}, secondary...)
		}
	}
	if p.input.empty() {
		secondary = append([]Hint{{"←", p.activityHintLocked()}}, secondary...)
		if _, ok := p.lastQueuedLocked(); ok {
			secondary = append([]Hint{{"↑", "edit queued"}}, secondary...)
		}
	}
	if primary.Key == "enter" && p.input.empty() && p.pickedApprovalLocked() != "" {
		primary.Label = strings.ToLower(approvalChoices[min(max(p.approvalChoice, 0), len(approvalChoices)-1)].label)
		secondary = append([]Hint{{"↑↓", "choose"}}, secondary...)
	}
	if prompt := p.pendingQuestionsLocked(); prompt != nil && primary.Key == "enter" {
		primary.Label = "answer"
		if len(prompt.Questions) > 1 {
			primary.Label = "save answer"
			if p.answersFor == prompt.ID && p.questionTab == len(prompt.Questions) {
				primary.Label = "submit answers"
			}
			secondary = append([]Hint{{"tab/shift+tab", "questions"}}, secondary...)
		}
	}
	return primary, secondary
}

// hintText draws a shortcut: the key bright, what it does muted.
func (p *Pane) hintText(h Hint) string {
	if h.Key == "" {
		return p.styles.muted.Render(h.Label)
	}
	return p.styles.text.Render(h.Key) + " " + p.styles.muted.Render(h.Label)
}

// inputWidth is how many cells of a pane w wide the input's rows take.
func inputWidth(w int) int {
	if w < 6 {
		return w
	}
	return max(1, w-5) // rail, gap, two cells of left padding, right padding
}

// inputRowsLocked wraps the draft and keeps the cursor's row in view.
func (p *Pane) inputRowsLocked(w, limit int, showCursor bool) []string {
	if row, ok := p.secretRowLocked(w, showCursor); ok {
		return []string{row}
	}
	c := p.composer
	if p.input.empty() {
		cursor := ""
		if showCursor {
			cursor = c.text.Render("█")
		}
		return []string{cursor + c.muted.Render(ansi.Truncate(placeholder, max(0, w-ansi.StringWidth(cursor)), "…"))}
	}
	text := p.input.wrap(w)
	lo, hi, _ := p.input.selection()
	first := p.inputWindowLocked(text, limit)
	out := make([]string, 0, limit)
	for i := first; i < len(text.rows) && i < first+limit; i++ {
		col := -1
		if showCursor && i == text.curRow {
			col = text.curCol
		}
		out = append(out, p.inputRowLocked(text.rows[i], text.starts[i], col, lo, hi))
	}
	return out
}

// inputWindowLocked is the first row of a draft taller than the input box
// that the box shows, limit rows at most. Like a textarea, the box stays where
// it is while the cursor moves within it and scrolls only as far as the cursor
// leaves it; it stays full when the draft shrinks. mu must be held.
func (p *Pane) inputWindowLocked(text wrapped, limit int) int {
	top := min(p.inputTop, text.curRow)
	if text.curRow >= top+limit {
		top = text.curRow - limit + 1
	}
	p.inputTop = max(0, min(top, len(text.rows)-limit))
	return p.inputTop
}

// inputRowLocked draws one row of the draft, whose first character is
// character start of the text: the cursor on column col (none when col < 0,
// a block after the text when col is past it), and characters lo to hi
// selected. A block (an atom) is drawn as a chip; the cursor and the
// selection show over it. Neighbors of one style are drawn together. mu must
// be held.
func (p *Pane) inputRowLocked(row []string, start, col, lo, hi int) string {
	c := p.composer
	styleOf := [...]lipgloss.Style{c.text, c.selected, c.cursor, c.atom}
	const (
		plain = iota
		selected
		cursor
		atom
	)
	var b, run strings.Builder
	kind := plain
	flush := func() {
		if run.Len() > 0 {
			b.WriteString(styleOf[kind].Render(run.String()))
			run.Reset()
		}
	}
	for j, ch := range row {
		k := plain
		switch idx := start + j; {
		case j == col:
			k = cursor
		case idx >= lo && idx < hi:
			k = selected
		case p.input.isAtom(ch):
			k = atom
		}
		if k != kind || k == cursor || k == atom {
			flush()
			kind = k
		}
		run.WriteString(ch)
	}
	flush()
	if col >= len(row) {
		b.WriteString(c.text.Render("█"))
	}
	return b.String()
}

// suggestionRowsLocked renders a window of the slash-command popup above the
// panel, keeping the selected row visible even in a short pane.
func (p *Pane) suggestionRowsLocked(w, limit int) []string {
	sugs := p.suggestionsLocked()
	if len(sugs) == 0 || limit == 0 {
		return nil
	}
	sugs = slices.Clone(sugs) // a command's Complete may hand out a slice of its own
	selected := min(p.sug, len(sugs)-1)
	first := max(0, min(selected-limit+1, len(sugs)-limit))
	labelW := 0
	for i := first; i < len(sugs) && i < first+limit; i++ {
		sugs[i].Label, sugs[i].Description = sanitize(sugs[i].Label), sanitize(sugs[i].Description) // a backend's command names and words
		labelW = max(labelW, ansi.StringWidth(sugs[i].Label))
	}
	labelW = min(labelW, max(8, w/2))
	c := p.composer
	rows := make([]string, 0, limit)
	for i := first; i < len(sugs) && i < first+limit; i++ {
		s := sugs[i]
		label := fit(ansi.Truncate(s.Label, labelW, "…"), labelW)
		desc := ansi.Truncate(s.Description, max(0, w-labelW-6), "…")
		mark, labelStyle := "  ", c.text
		if i == selected && s.Insert != "" {
			mark, labelStyle = "› ", c.rail
		}
		row := c.text.Render("  ") + labelStyle.Render(mark+label) + c.text.Render("  ") + c.muted.Render(desc)
		rows = append(rows, c.fill(row, w))
	}
	return rows
}
