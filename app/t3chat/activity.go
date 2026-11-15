package t3chat

import (
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

type activityView struct {
	open, recent, expanded bool
	selected               string // server identity, so streaming updates keep the selection
	scroll                 int
}

func (p *Pane) activityCmd(string) { p.activity.open = true }

// activityTextLocked handles the list's controls. Typing returns to the
// composer and falls through to its normal editor without losing the text.
func (p *Pane) activityTextLocked(s string) bool {
	switch s {
	case "\x1b":
		p.activity.open = false
	case "\t":
		p.activity.recent = !p.activity.recent
		p.activity.selected, p.activity.scroll, p.activity.expanded = "", 0, false
	case "\r":
		p.activity.expanded = !p.activity.expanded
	default:
		p.activity.open = false
		return false
	}
	return true
}

func (p *Pane) activityKeyLocked(k uv.KeyPressEvent) {
	switch k.Code {
	case uv.KeyLeft, uv.KeyRight:
		p.activity.open = false
	case uv.KeyUp:
		p.moveActivityLocked(-1)
	case uv.KeyDown:
		p.moveActivityLocked(1)
	case uv.KeyPgUp:
		p.moveActivityLocked(-max(1, p.bodyH-4))
	case uv.KeyPgDown:
		p.moveActivityLocked(max(1, p.bodyH-4))
	case uv.KeyHome:
		p.moveActivityLocked(-len(p.activityEntriesLocked()))
	case uv.KeyEnd:
		p.moveActivityLocked(len(p.activityEntriesLocked()))
	}
}

func (p *Pane) visibleActivityLocked() []activityEntry {
	all := p.activityEntriesLocked()
	if p.activity.recent {
		return all
	}
	active := all[:0]
	for _, entry := range all {
		if entry.active() {
			active = append(active, entry)
		}
	}
	return active
}

func (p *Pane) selectedActivityLocked(entries []activityEntry) int {
	for i, e := range entries {
		if e.id == p.activity.selected {
			return i
		}
	}
	if len(entries) > 0 {
		p.activity.selected = entries[0].id
	}
	return 0
}

func (p *Pane) moveActivityLocked(delta int) {
	entries := p.visibleActivityLocked()
	if len(entries) == 0 {
		return
	}
	i := min(max(0, p.selectedActivityLocked(entries)+delta), len(entries)-1)
	p.activity.selected = entries[i].id
}

// activityRowsLocked fills the conversation area with the selected live list.
// The list scrolls independently of the conversation it temporarily covers.
func (p *Pane) activityRowsLocked(w, h int) []string {
	if h == 0 {
		return nil
	}
	entries := p.visibleActivityLocked()
	mode := "Active"
	if p.activity.recent {
		mode = "Recent (active and finished)"
	}
	rows := []string{p.styles.bold.Render(inset + "Subagents & processes · " + mode)}
	if len(entries) == 0 {
		message := "No active subagents or processes. Tab shows recent work."
		if p.activity.recent {
			message = "T3 has not reported any subagents or processes in this thread."
		}
		rows = append(rows, "")
		rows = append(rows, wrapLines(message, w)...)
		return activityFitRows(rows, h)
	}
	selected := p.selectedActivityLocked(entries)
	var list []string
	lastGroup, selectedRow, selectedEnd := "", 0, 0
	for i, e := range entries {
		group := "Processes & tasks"
		if e.kind == "subagent" {
			group = "Subagents"
		}
		if group != lastGroup {
			list = append(list, "", p.styles.muted.Render(inset+group))
			lastGroup = group
		}
		if i == selected {
			selectedRow = len(list)
		}
		list = append(list, p.activityEntryRow(e, i == selected, w))
		if e.progress != "" {
			list = append(list, p.styles.muted.Render(ansi.Truncate(inset+"    "+firstLine(e.progress), w, "…")))
		}
		if i == selected && p.activity.expanded {
			list = append(list, p.activityDetailRows(e, w, max(1, h-3))...)
		}
		if i == selected {
			selectedEnd = len(list)
		}
	}
	room := max(0, h-len(rows))
	if room == 0 {
		return rows
	}
	if selectedEnd > p.activity.scroll+room {
		p.activity.scroll = selectedEnd - room
	}
	if selectedRow < p.activity.scroll {
		p.activity.scroll = selectedRow
	}
	p.activity.scroll = max(0, min(p.activity.scroll, len(list)-room))
	end := min(len(list), p.activity.scroll+room)
	rows = append(rows, list[p.activity.scroll:end]...)
	return activityFitRows(rows, h)
}

func activityFitRows(rows []string, h int) []string {
	rows = rows[:min(len(rows), h)]
	for len(rows) < h {
		rows = append(rows, "")
	}
	return rows
}

func (p *Pane) activityEntryRow(e activityEntry, selected bool, w int) string {
	mark := "  "
	if selected {
		mark = "› "
	}
	status := e.status
	if status == "" {
		status = "unknown"
	}
	st := p.styles.muted
	switch {
	case e.status == "failed":
		st = p.styles.bad
	case e.status == "waiting":
		st = p.styles.warn
	case e.active():
		st = p.styles.rail
	}
	label := e.title
	if label == "" {
		label = e.kind
	}
	if e.kind == "monitor" {
		label = "monitor · " + label
	}
	lead := inset + p.styles.rail.Render(mark) + st.Render(fit(status, 11)) + " "
	return lead + ansi.Truncate(firstLine(label), max(0, w-ansi.StringWidth(lead)), "…")
}

func (p *Pane) activityDetailRows(e activityEntry, w, limit int) []string {
	var rows []string
	for _, part := range e.details {
		rows = append(rows, wrapLines(part, max(1, w-2))...)
		if len(rows) >= limit {
			break
		}
	}
	if len(rows) == 0 {
		rows = []string{inset + "No further details reported."}
	}
	if len(rows) > limit {
		rows = rows[:limit]
		rows[limit-1] = ansi.Truncate(rows[limit-1], max(1, w-3), "") + "…"
	}
	for i := range rows {
		rows[i] = p.styles.muted.Render("  " + rows[i])
	}
	return rows
}

// activityHintLocked keeps the entry point visible whenever the input is empty.
func (p *Pane) activityHintLocked() string {
	agents, processes := p.activeCountsLocked()
	var counts []string
	if agents > 0 {
		counts = append(counts, fmt.Sprintf("%d agent%s", agents, plural(agents)))
	}
	if processes > 0 {
		label := "processes"
		if processes == 1 {
			label = "process"
		}
		counts = append(counts, fmt.Sprintf("%d %s", processes, label))
	}
	if len(counts) == 0 {
		return "activity"
	}
	return strings.Join(counts, ", ")
}
