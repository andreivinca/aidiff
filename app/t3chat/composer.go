package t3chat

import (
	"strings"

	"github.com/umputun/revdiff/app/t3client"

	"github.com/charmbracelet/x/ansi"
)

// composerRowsLocked fits the popup, input panel and hints into budget rows.
// Small panes give up padding and popup rows before hiding the input cursor.
func (p *Pane) composerRowsLocked(w, budget int, showCursor bool) []string {
	meta, hints := budget >= 2, budget >= 3
	reserved := 0
	if meta {
		reserved++
	}
	if hints {
		reserved++
	}
	inner := max(1, w-5) // rail, gap, two cells of left padding, right padding
	if w < 6 {
		inner = w
	}
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
	if room >= 2 {
		rows = append(rows, p.composerRowLocked("", w, showCursor))
	}
	for _, line := range input {
		rows = append(rows, p.composerRowLocked(line, w, showCursor))
	}
	if room >= 2 {
		rows = append(rows, p.composerRowLocked("", w, showCursor))
	}
	if meta {
		rows = append(rows, p.composerRowLocked(p.composerSettingsLocked(inner), w, showCursor))
	}
	if hints {
		rows = append(rows, p.composerHintsLocked(w, len(popup) > 0))
	}
	return append(rows, notes...)
}

// composerRowLocked paints the entire panel width, including trailing spaces.
func (p *Pane) composerRowLocked(text string, w int, focused bool) string {
	if w < 6 {
		return p.styles.composerLine(fit(text, w))
	}
	accent := p.styles.rail
	if p.interaction == "plan" {
		accent = p.styles.warn
	}
	if !focused {
		accent = p.styles.muted
	}
	return accent.Render("│") + " " + p.styles.composerLine(fit("  "+text+" ", w-2))
}

// composerLine restores the panel background after each nested color or
// cursor style resets it. Lipgloss does not restore an enclosing style.
func (s styles) composerLine(text string) string {
	var row strings.Builder
	for part := range strings.SplitSeq(text, "\x1b[0m") {
		row.WriteString(s.composer.Render(part))
	}
	return row.String()
}

// composerSettingsLocked shows the thread's actual model and permission level.
// The model keeps its place as narrow panes drop the secondary details.
func (p *Pane) composerSettingsLocked(w int) string {
	mode, accent := "Build", p.styles.rail
	if p.interaction == "plan" {
		mode, accent = "Plan", p.styles.warn
	}
	model, provider := p.composerModelLocked()
	if effort := p.composerEffortLocked(); effort != "" {
		model += " · " + effort
	}
	var details []string
	if provider != "" {
		details = append(details, provider)
	}
	if p.runtimeMode != "" {
		details = append(details, strings.ReplaceAll(p.runtimeMode, "-", " "))
	}
	if p.fresh {
		details = append(details, "fresh context")
	}
	lead := accent.Render(mode) + "  "
	for len(details) > 0 && ansi.StringWidth(lead+model+" "+strings.Join(details, " · ")) > w {
		details = details[:len(details)-1]
	}
	text := lead + p.styles.text.Render(ansi.Truncate(model, max(0, w-ansi.StringWidth(lead)), "…"))
	if len(details) > 0 {
		text += " " + p.styles.muted.Render(strings.Join(details, " · "))
	}
	return text
}

// composerModelLocked resolves the display names without guessing from a slug.
func (p *Pane) composerModelLocked() (model, provider string) {
	if p.current == nil || p.current.Model == "" {
		return "T3 Code", ""
	}
	model, provider = p.current.Model, p.current.InstanceID
	if p.catalog == nil {
		return model, provider
	}
	for _, m := range p.catalog.Models {
		if m.Slug != p.current.Model || m.InstanceID != p.current.InstanceID {
			continue
		}
		if m.Name != "" {
			model = m.Name
		}
		return model, m.Provider
	}
	return model, provider
}

// composerHintsLocked lists shortcuts that work in the current input state.
// Whole hints are omitted as space runs out, keeping the keys readable.
func (p *Pane) composerHintsLocked(w int, popup bool) string {
	type hint struct{ key, label string }
	primary := hint{"enter", "send"}
	secondary := []hint{{"ctrl+p", "commands"}, {"ctrl+v", "paste"}, {"shift+enter", "newline"}}
	if p.activeRunLocked() != "" {
		primary.label = "queue"
		secondary = append([]hint{{"esc", "stop…"}}, secondary...)
	}
	if len(p.input.runes) == 0 {
		secondary = append([]hint{{"←", p.activityHintLocked()}}, secondary...)
	}
	if popup {
		primary = hint{"↑↓", "select"}
		secondary = []hint{{"enter", "choose"}, {"tab", "complete"}, {"esc", "close"}}
	}
	if p.busy {
		primary = hint{"", "sending…"}
	} else if p.pasting {
		primary = hint{"", "pasting…"}
	}
	if p.activity.open {
		primary = hint{"↑↓", "select"}
		filter := "recent"
		if p.activity.recent {
			filter = "active"
		}
		secondary = []hint{{"enter", "details"}, {"tab", filter}, {"esc", "back"}}
	}
	render := func(h hint) string {
		if h.key == "" {
			return p.styles.muted.Render(h.label)
		}
		return p.styles.text.Render(h.key) + " " + p.styles.muted.Render(h.label)
	}
	left := inset + render(primary)
	var right string
	for _, h := range secondary {
		candidate := render(h)
		if right != "" {
			candidate = right + "  " + candidate
		}
		if ansi.StringWidth(left)+ansi.StringWidth(candidate)+3 <= w {
			right = candidate
		}
	}
	return left + strings.Repeat(" ", max(0, w-ansi.StringWidth(left+right)-1)) + right
}

// inputRowsLocked wraps the draft and keeps the cursor's row in view. One
// extra cell is reserved for the block cursor at the end of a full line.
func (p *Pane) inputRowsLocked(w, limit int, showCursor bool) []string {
	if len(p.input.runes) == 0 {
		cursor := ""
		if showCursor {
			cursor = "█"
		}
		return []string{cursor + p.styles.muted.Render(ansi.Truncate(placeholder, max(0, w-ansi.StringWidth(cursor)), "…"))}
	}
	var rows [][]rune
	var row []rune
	width, curRow, curCol := 0, 0, 0
	for i, r := range p.input.runes {
		if i == p.input.cur {
			curRow, curCol = len(rows), len(row)
		}
		if r == '\n' {
			rows = append(rows, row)
			row, width = nil, 0
			continue
		}
		rw := ansi.StringWidth(string(r))
		if width > 0 && width+rw > max(1, w-1) {
			rows = append(rows, row)
			row, width = nil, 0
			if i == p.input.cur {
				curRow, curCol = len(rows), 0
			}
		}
		row = append(row, r)
		width += rw
	}
	if p.input.cur == len(p.input.runes) {
		if width >= w {
			rows = append(rows, row)
			row = nil
		}
		curRow, curCol = len(rows), len(row)
	}
	rows = append(rows, row)
	first := min(max(0, curRow-limit+1), max(0, len(rows)-limit))
	out := make([]string, 0, limit)
	for i := first; i < len(rows) && i < first+limit; i++ {
		text := string(rows[i])
		if showCursor && i == curRow {
			if curCol < len(rows[i]) {
				text = string(rows[i][:curCol]) + p.styles.cursor.Render(string(rows[i][curCol])) + string(rows[i][curCol+1:])
			} else {
				text += "█"
			}
		}
		out = append(out, text)
	}
	return out
}

// suggestionRowsLocked renders a window of the slash-command popup above the
// panel, keeping the selected row visible even in a short pane.
func (p *Pane) suggestionRowsLocked(w, limit int) []string {
	sugs := p.suggestionsLocked()
	if len(sugs) == 0 || limit == 0 {
		return nil
	}
	p.sug = min(p.sug, len(sugs)-1)
	first := max(0, min(p.sug-limit+1, len(sugs)-limit))
	labelW := 0
	for i := first; i < len(sugs) && i < first+limit; i++ {
		labelW = max(labelW, ansi.StringWidth(sugs[i].label))
	}
	labelW = min(labelW, max(8, w/2))
	rows := make([]string, 0, limit)
	for i := first; i < len(sugs) && i < first+limit; i++ {
		s := sugs[i]
		label := fit(ansi.Truncate(s.label, labelW, "…"), labelW)
		desc := ansi.Truncate(s.desc, max(0, w-labelW-6), "…")
		mark := "  "
		if i == p.sug && s.insert != "" {
			mark = p.styles.rail.Render("› ")
			label = p.styles.rail.Render(label)
		}
		row := "  " + mark + label + "  " + p.styles.muted.Render(desc)
		rows = append(rows, p.styles.composerLine(fit(row, w)))
	}
	return rows
}

// composerEffortLocked uses the matching provider's model defaults.
func (p *Pane) composerEffortLocked() string {
	var caps t3client.ModelCapabilities
	if p.current != nil && p.catalog != nil {
		for _, m := range p.catalog.Models {
			if m.InstanceID == p.current.InstanceID && m.Slug == p.current.Model {
				caps = m.Capabilities
				break
			}
		}
	}
	return p.current.Effort(caps)
}
