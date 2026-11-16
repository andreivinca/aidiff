package chat

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

type activityView struct {
	open, recent, expanded bool
	selected               string // the entry's id, so updates keep the selection; "" is the first entry
	scroll                 int    // the list's first row shown, as last moved; a frame keeps the selection in view
}

// window is the first list row to show in room rows of a list of n rows:
// the scroll, moved as little as keeps the selected entry's rows, first up
// to last, in view.
func (v activityView) window(first, last, n, room int) int {
	scroll := v.scroll
	if last > scroll+room {
		scroll = last - room
	}
	if first < scroll {
		scroll = first
	}
	return max(0, min(scroll, n-room))
}

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
	page := max(1, p.frame.layout.bodyH-4)
	switch k.Code {
	case uv.KeyLeft, uv.KeyRight:
		p.activity.open = false
	case uv.KeyUp:
		p.moveActivityLocked(-1)
	case uv.KeyDown:
		p.moveActivityLocked(1)
	case uv.KeyPgUp:
		p.moveActivityLocked(-page)
	case uv.KeyPgDown:
		p.moveActivityLocked(page)
	case uv.KeyHome:
		p.moveActivityLocked(-len(p.activities))
	case uv.KeyEnd:
		p.moveActivityLocked(len(p.activities))
	}
}

func (p *Pane) visibleActivityLocked() []Activity {
	if p.activity.recent {
		return p.activities
	}
	var active []Activity
	for _, entry := range p.activities {
		if entry.active() {
			active = append(active, entry)
		}
	}
	return active
}

// selectedActivityLocked is the index of the selected entry, the first when
// none is selected or the selected one is gone; mu must be held.
func (p *Pane) selectedActivityLocked(entries []Activity) int {
	for i, e := range entries {
		if e.ID == p.activity.selected {
			return i
		}
	}
	return 0
}

// moveActivityLocked moves the selection by delta entries. The list then
// scrolls on from where the frame on screen showed it. mu must be held.
func (p *Pane) moveActivityLocked(delta int) {
	entries := p.visibleActivityLocked()
	if len(entries) == 0 {
		return
	}
	i := min(max(0, p.selectedActivityLocked(entries)+delta), len(entries)-1)
	p.activity.selected = entries[i].ID
	p.activity.scroll = p.frame.layout.activityScroll
}

// activityRowsLocked fills the conversation area with the selected live list,
// and says which list row it starts at. The list scrolls independently of the
// conversation it temporarily covers. mu must be held.
func (p *Pane) activityRowsLocked(w, h int) ([]string, int) {
	if h == 0 {
		return nil, 0
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
			message = "No subagents or processes have been reported in this conversation."
		}
		rows = append(rows, "")
		rows = append(rows, wrapLines(message, w)...)
		return activityFitRows(rows, h), 0
	}
	list, first, last := p.activityListLocked(entries, w, h)
	room := h - len(rows)
	scroll := p.activity.window(first, last, len(list), room)
	rows = append(rows, list[scroll:min(len(list), scroll+room)]...)
	return activityFitRows(rows, h), scroll
}

// activityListLocked is every row of the list, grouped, with the selected
// entry's first row and the row after its last. mu must be held.
func (p *Pane) activityListLocked(entries []Activity, w, h int) (list []string, first, last int) {
	selected := p.selectedActivityLocked(entries)
	lastGroup := ""
	for i, e := range entries {
		group := "Processes & tasks"
		if e.Agent {
			group = "Subagents"
		}
		if group != lastGroup {
			list = append(list, "", p.styles.muted.Render(inset+group))
			lastGroup = group
		}
		if i == selected {
			first = len(list)
		}
		list = append(list, p.ActivityRow(e, i == selected, w))
		if e.Progress != "" {
			list = append(list, p.styles.muted.Render(ansi.Truncate(inset+"    "+firstLine(e.Progress), w, "…")))
		}
		if i == selected && p.activity.expanded {
			list = append(list, p.activityDetailRows(e, w, max(1, h-3))...)
		}
		if i == selected {
			last = len(list)
		}
	}
	return list, first, last
}

func activityFitRows(rows []string, h int) []string {
	rows = rows[:min(len(rows), h)]
	for len(rows) < h {
		rows = append(rows, "")
	}
	return rows
}

func (p *Pane) ActivityRow(e Activity, selected bool, w int) string {
	mark := "  "
	if selected {
		mark = "› "
	}
	status := e.Status
	if status == "" {
		status = "unknown"
	}
	st := p.styles.muted
	switch {
	case e.State == Failed:
		st = p.styles.bad
	case e.State == Waiting:
		st = p.styles.warn
	case e.active():
		st = p.styles.rail
	}
	label := e.Title
	if label == "" {
		label = e.kind()
	}
	lead := inset + p.styles.rail.Render(mark) + st.Render(fit(status, 11)) + " "
	return lead + ansi.Truncate(firstLine(label), max(0, w-ansi.StringWidth(lead)), "…")
}

func (p *Pane) activityDetailRows(e Activity, w, limit int) []string {
	var rows []string
	for _, part := range e.Details {
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
	agents, processes := p.counts.agents, p.counts.processes
	var counts []string
	if agents > 0 {
		counts = append(counts, fmt.Sprintf("%d agent%s", agents, plural(agents)))
	}
	if processes > 0 {
		counts = append(counts, fmt.Sprintf("%d %s", processes, processWord(processes)))
	}
	if len(counts) == 0 {
		return "activity"
	}
	return strings.Join(counts, ", ")
}

// kind names the work for an entry without a title.
func (a Activity) kind() string {
	if a.Agent {
		return "subagent"
	}
	return "process"
}

// groupActivity orders the activity list: subagents first, then the rest,
// each with what is still going ahead of what finished, otherwise as the
// backend listed them, most recent first.
func groupActivity(entries []Activity) []Activity {
	out := slices.Clone(entries)
	slices.SortStableFunc(out, func(a, b Activity) int {
		return cmp.Or(boolOrder(a.Agent, b.Agent), boolOrder(a.active(), b.active()))
	})
	return out
}

// boolOrder sorts a true before a false.
func boolOrder(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return -1
	}
	return 1
}
