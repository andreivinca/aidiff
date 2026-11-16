package chat

import (
	"fmt"

	"github.com/charmbracelet/x/ansi"
)

// defaultTitle names a conversation neither the pane nor the backend named.
const defaultTitle = "Chat"

// layout is where a frame put the pane's parts. Keys and the mouse read the
// layout of the frame on screen to page through the transcript and to find
// the text under the pointer.
type layout struct {
	headerH        int // the header's rows: 1, or 0 in a one-row pane
	bodyH          int // the transcript's rows, a page
	textBottom     int // the first row below the transcript and the queued messages, which a drag selects from
	scroll         int // how far up the transcript is shown
	maxScroll      int // how far up it can go
	activityScroll int // the first row of the activity list shown
}

// renderLocked lays the pane out: header, transcript, queued messages,
// status line and composer. The transcript sticks to the bottom like a chat.
// It changes nothing; mu must be held.
func (p *Pane) renderLocked(showCursor bool) ([]string, layout) {
	w, h := p.width, p.height
	if p.stop.turn != "" {
		return p.stopRowsLocked(w, h), layout{}
	}
	var l layout
	gapH := 0
	if h > 1 {
		l.headerH = 1
	}
	if h >= 7 {
		gapH = 1
	}
	footer := p.composerRowsLocked(w, max(1, h-l.headerH-gapH-1), showCursor)
	queued := p.queuedMessagesLocked()
	pending := p.pendingRowsLocked(queued, w, min(maxPendingRows, max(0, h-l.headerH-gapH-len(footer)-1)))
	l.bodyH = max(0, h-l.headerH-gapH-len(footer)-len(pending))
	l.textBottom = l.headerH + l.bodyH + len(pending)
	var body []string
	if p.activity.open {
		body, l.activityScroll = p.activityRowsLocked(w, l.bodyH)
	} else {
		body, l.scroll, l.maxScroll = p.transcriptWindowLocked(w, l.bodyH)
	}

	rows := make([]string, 0, h)
	if l.headerH > 0 {
		rows = append(rows, p.headerLocked(w, l.scroll))
	}
	rows = append(rows, body...)
	rows = append(rows, pending...)
	if gapH > 0 {
		rows = append(rows, p.styles.muted.Render(ansi.Truncate(inset+p.activityStatusLocked(), w, "…")))
	}
	rows = append(rows, footer...)
	return fitRows(rows, w, h), l
}

// fitRows forces rows to exactly h rows of exactly w cells.
func fitRows(rows []string, w, h int) []string {
	for len(rows) < h {
		rows = append(rows, "")
	}
	rows = rows[:h]
	for i := range rows {
		rows[i] = fit(rows[i], w)
	}
	return rows
}

// transcriptWindowLocked is the part of the transcript that fills h rows at
// the scroll asked for, with blank rows above a short transcript, and the
// scroll it shows within the most there is. mu must be held.
func (p *Pane) transcriptWindowLocked(w, h int) (rows []string, scroll, maxScroll int) {
	lines := p.transcriptLocked(w)
	maxScroll = max(0, len(lines)-h)
	scroll = min(p.scroll, maxScroll)
	end := len(lines) - scroll
	start := max(0, end-h)
	rows = make([]string, h-(end-start), h)
	rows = append(rows, lines[start:end]...)
	return rows, scroll, maxScroll
}

// headerLocked is the title and the agent's state, with how far the
// transcript is scrolled up; mu must be held.
func (p *Pane) headerLocked(w, scroll int) string {
	state := p.link
	if state == "" {
		state = string(p.agentStateLocked())
	}
	if scroll > 0 {
		state += fmt.Sprintf(" · ↑%d · end jumps down", scroll)
	}
	title := p.titleLocked()
	// the state keeps its place; a long title yields to it, down to a third of the width
	room := w - ansi.StringWidth(state) - 3
	if room < min(ansi.StringWidth(title), max(8, w/3)) {
		room = max(1, min(ansi.StringWidth(title), max(8, w/3)))
		state = ansi.Truncate(state, max(1, w-room-3), "…")
	}
	return p.styles.bold.Render(ansi.Truncate(title, room, "…")) + " · " + p.styles.muted.Render(state)
}

// titleLocked names the conversation: as the backend does, else as the
// pane was told; mu must be held.
func (p *Pane) titleLocked() string {
	switch {
	case p.conv.Title != "":
		return p.conv.Title
	case p.title != "":
		return p.title
	}
	return defaultTitle
}

// agentStateLocked is what the agent is doing, idle when the backend did
// not say; mu must be held.
func (p *Pane) agentStateLocked() AgentState {
	if p.conv.State == "" {
		return AgentIdle
	}
	return p.conv.State
}

// runningTurnLocked is the newest turn still going, or ""; mu must be held.
func (p *Pane) runningTurnLocked() string {
	if len(p.conv.Running) == 0 {
		return ""
	}
	return p.conv.Running[0]
}
