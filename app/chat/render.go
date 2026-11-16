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
	sepH := 0 // a blank row between the transcript and the status line; queued cards end in one already
	if gapH > 0 && len(pending) == 0 && h >= 10 {
		sepH = 1
	}
	l.bodyH = max(0, h-l.headerH-gapH-sepH-len(footer)-len(pending))
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
	if sepH > 0 {
		rows = append(rows, "")
	}
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
// scroll it shows within the most there is. The user's message the rows
// below it answer stays on top once it scrolled out of view (see
// stickyRows). mu must be held.
func (p *Pane) transcriptWindowLocked(w, h int) (rows []string, scroll, maxScroll int) {
	lines, spans := p.transcriptPanelsLocked(w)
	maxScroll = max(0, len(lines)-h)
	scroll = min(p.scroll, maxScroll)
	end := len(lines) - scroll
	start := max(0, end-h)
	rows = make([]string, h-(end-start), h)
	rows = append(rows, lines[start:end]...)
	if sticky := p.stickyLocked(lines, spans, start, w); len(sticky) > 0 && len(sticky)+minBelowSticky <= h {
		copy(rows, sticky)
	}
	return rows, scroll, maxScroll
}

// Sticky message sizes: the lines a compact sticky message keeps from its
// start, and the transcript rows left visible below it, or it is not shown.
const (
	stickyHead     = 3
	minBelowSticky = 3
)

// stickyLocked is the user's message to keep on top of a window whose first
// row is transcript row top: the last one whose panel starts above it. nil
// when none does. mu must be held.
func (p *Pane) stickyLocked(lines []string, spans []panelSpan, top, w int) []string {
	for i := len(spans) - 1; i >= 0; i-- {
		s := spans[i]
		if s.start < top {
			return stickyRows(lines[s.start:s.start+s.n], top-s.start, w, p.styles)
		}
	}
	return nil
}

// stickyRows is a user's message kept on top of the transcript, its text
// lines without the panel's padding and a blank row under them, off rows of
// its panel scrolled above the window. A message of more than four lines is
// compact while its end is out of view: its first three lines and a "…" row.
// Scrolling back to it brings its end in under the "…", one line a row, as
// its lines really come into view, until nothing is left out and the whole
// message shows. The panel's first and last rows are padding.
func stickyRows(panel []string, off, w int, st styles) []string {
	n := len(panel)
	text := panel[1 : n-1]
	tail := max(0, n-1-off) // its lines in view below the window's top
	out := make([]string, 0, len(text)+1)
	if len(text) <= stickyHead+1 || stickyHead+1+tail >= len(text) {
		out = append(out, text...)
	} else {
		out = append(out, text[:stickyHead]...)
		out = append(out, st.rail.Render(rail)+st.panel.Render(fit("  …", max(1, w-1))))
		out = append(out, text[len(text)-tail:]...)
	}
	return append(out, "") // apart from the transcript under it
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
