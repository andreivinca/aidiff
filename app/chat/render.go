package chat

import (
	"fmt"
	"slices"
	"strings"

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
	statusY        int // the status row above the composer, -1 when none; a click on it while scrolled up goes to the end
	pin            stickyPin
}

// stickyPin is the user's message kept on top of the transcript as a frame
// drew it: the rows it covers, under the header, and the scroll that
// brings it back in its place, whole, with the start of what answers it
// under it. A click on it scrolls there (clickStickyLocked).
type stickyPin struct {
	rows   int // 0: none on top
	scroll int
}

// renderLocked lays the pane out: header, transcript, queued messages,
// status line and composer, with how a copy reads each row. The transcript
// sticks to the bottom like a chat. It changes nothing shown, but it moves
// the input box's window to keep the cursor in view (inputWindowLocked) and
// drops what was typed for a secret request no longer pending
// (secretLocked); mu must be held.
func (p *Pane) renderLocked(showCursor bool) ([]string, []rowMeta, layout) {
	w, h := p.width, p.height
	if p.stop.turn != "" {
		return p.stopRowsLocked(w, h), make([]rowMeta, h), layout{}
	}
	l := layout{statusY: -1}
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
	var bodyMeta []rowMeta
	if p.activity.open {
		body, l.activityScroll = p.activityRowsLocked(w, l.bodyH)
	} else {
		body, bodyMeta, l.scroll, l.maxScroll, l.pin = p.transcriptWindowLocked(w, l.bodyH)
	}

	rows := make([]string, 0, h)
	meta := make([]rowMeta, l.headerH, h) // the header's
	if l.headerH > 0 {
		rows = append(rows, p.headerLocked(w, l.scroll))
	}
	rows = append(rows, body...)
	if bodyMeta == nil {
		bodyMeta = make([]rowMeta, len(body))
	}
	meta = append(meta, bodyMeta...)
	rows = append(rows, pending...)
	if sepH > 0 {
		rows = append(rows, "")
	}
	if gapH > 0 {
		l.statusY = len(rows)
		rows = append(rows, p.statusRowLocked(w))
	}
	rows = append(rows, footer...)
	for len(meta) < h {
		meta = append(meta, rowMeta{})
	}
	return fitRows(rows, w, h), meta[:h], l
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
// stickyRows), and pin says where. mu must be held.
func (p *Pane) transcriptWindowLocked(w, h int) (rows []string, meta []rowMeta, scroll, maxScroll int, pin stickyPin) {
	lines, spans := p.transcriptPanelsLocked(w)
	n := len(lines.rows)
	p.anchorScrollLocked(w, n)
	maxScroll = max(0, n-h)
	scroll = min(p.scroll, maxScroll)
	end := n - scroll
	start := max(0, end-h)
	rows = make([]string, h-(end-start), h)
	rows = append(rows, lines.rows[start:end]...)
	meta = make([]rowMeta, h-(end-start), h)
	meta = append(meta, lines.meta[start:end]...)
	if sticky, span := p.stickyLocked(lines, spans, start, w, h); len(sticky.rows) > 0 {
		copy(rows, sticky.rows)
		copy(meta, sticky.meta)
		if k := len(sticky.rows); k < len(meta) {
			meta[k].cont = false // the row under the sticky message no longer goes on from the one above it
		}
		// its first row at the top, or, when it and the start of the answer
		// do not fit, its end and that start
		top := max(span.start, span.start+span.n+minBelowSticky-h)
		pin = stickyPin{rows: len(sticky.rows), scroll: max(0, min(maxScroll, n-h-max(0, top)))}
	}
	return rows, meta, scroll, maxScroll, pin
}

// seenLines is the transcript's size as the last frame laid it out.
type seenLines struct {
	w, n int
}

// anchorScrollLocked keeps a transcript scrolled up on the text the reader
// is on: the scroll counts lines from the end, so output arriving under it
// would slide the text up, a line a line. It grows by what the transcript
// grew since the last frame, at the same width (a resize wraps every line
// again). mu must be held.
func (p *Pane) anchorScrollLocked(w, n int) {
	if p.scroll > 0 && p.seen.w == w && p.seen.n > 0 {
		p.scroll = max(0, p.scroll+n-p.seen.n)
	}
	p.seen = seenLines{w: w, n: n}
}

// Sticky message sizes: the lines a compact sticky message keeps from its
// start, and the transcript rows left visible below it, or it is not shown.
const (
	stickyHead     = 3
	minBelowSticky = 3
)

// stickyLocked is the user's message to keep on top of an h-row window
// whose first row is transcript row top, and where its panel is: the last
// one whose panel starts above it. Like a heading under CSS's position: sticky, it stays within
// what answers it: once the next message's panel comes up under it, that
// panel pushes it up, a row a row, until it reaches the top and is the one
// kept there, so a message is never drawn over by the one before it. No
// rows when there is none, or when a window too short to keep
// minBelowSticky rows under it would show nothing else. mu must be held.
func (p *Pane) stickyLocked(lines block, spans []panelSpan, top, w, h int) (block, panelSpan) {
	for i := len(spans) - 1; i >= 0; i-- {
		s := spans[i]
		if s.start >= top {
			continue
		}
		panel := block{rows: lines.rows[s.start : s.start+s.n], meta: lines.meta[s.start : s.start+s.n]}
		sticky := stickyRows(panel, top-s.start, w, p.styles)
		if len(sticky.rows)+minBelowSticky > h {
			return block{}, panelSpan{}
		}
		if i+1 < len(spans) { // the next one starts at the window's top or under it
			if pushed := len(sticky.rows) - (spans[i+1].start - top); pushed > 0 {
				sticky = block{rows: sticky.rows[pushed:], meta: sticky.meta[pushed:]}
			}
		}
		return sticky, s
	}
	return block{}, panelSpan{}
}

// stickyRows is a user's message kept on top of the transcript, its text
// lines without the panel's padding and a blank row under them, off rows of
// its panel scrolled above the window. A message of more than four lines is
// compact while its end is out of view: its first three lines and a "…" row.
// Scrolling back to it brings its end in under the "…", one line a row, as
// its lines really come into view, until nothing is left out and the whole
// message shows. The panel's first and last rows are padding.
func stickyRows(panel block, off, w int, st styles) block {
	n := len(panel.rows)
	text := block{rows: panel.rows[1 : n-1], meta: panel.meta[1 : n-1]}
	k := len(text.rows)
	tail := max(0, n-1-off) // its lines in view below the window's top
	var out block
	if k <= stickyHead+1 || stickyHead+1+tail >= k {
		out.join(text)
	} else {
		out.join(block{rows: text.rows[:stickyHead], meta: text.meta[:stickyHead]})
		out.plain(st.rail.Render(rail) + st.panel.Render(fit("  …", max(1, w-1))))
		rest := block{rows: text.rows[k-tail:], meta: slices.Clone(text.meta[k-tail:])}
		if len(rest.meta) > 0 {
			rest.meta[0].cont = false // it follows the gap, not the line it was wrapped from
		}
		out.join(rest)
	}
	out.plain("") // apart from the transcript under it
	return out
}

// headerLocked is the title and the agent's state, with how far the
// transcript is scrolled up; mu must be held.
func (p *Pane) headerLocked(w, scroll int) string {
	if p.closable && w > headerCloseW+4 {
		text := p.headerTextLocked(w-headerCloseW, scroll)
		pad := max(0, w-headerCloseW-ansi.StringWidth(text))
		return text + strings.Repeat(" ", pad) + " " + p.styles.muted.Render(headerClose)
	}
	return p.headerTextLocked(w, scroll)
}

// The header's close button: × in its last column, a blank before it.
const (
	headerClose  = "×"
	headerCloseW = 2
)

// onHeaderClose reports whether a click at (x, y) lands on the header's ×,
// as the last frame drew it.
func (p *Pane) onHeaderClose(x, y int) bool {
	f := p.frame
	return p.closable && f.valid && f.layout.headerH > 0 && y == 0 && p.width > headerCloseW+4 && x >= p.width-1
}

// statusRowLocked is the row above the composer: what the agent does and,
// while the view is scrolled up, what is below it, in the warning color:
// the messages that arrived since the view left the end, else how many
// lines lie below, so a transcript that grew past the view does not read
// as its end (the header's ↑N alone went unseen). A click on it goes to
// the end (SendMouse). At the end, everything shown counts as read. mu
// must be held.
func (p *Pane) statusRowLocked(w int) string {
	status := inset + p.activityStatusLocked()
	if p.scroll <= 0 {
		p.readEntries = len(p.conv.Entries)
		return p.styles.muted.Render(ansi.Truncate(status, w, "…"))
	}
	more := fmt.Sprintf(" · ↓ %d lines below · End", p.scroll)
	if n := p.newMessagesLocked(); n > 0 {
		more = fmt.Sprintf(" · ↓ %d new message%s · End", n, plural(n))
	}
	room := max(0, w-ansi.StringWidth(more))
	return p.styles.muted.Render(ansi.Truncate(status, room, "…")) + p.styles.warn.Render(ansi.Truncate(more, w-min(room, ansi.StringWidth(status)), ""))
}

// newMessagesLocked counts what arrived since the view left the end: the
// agent's messages, its questions and approvals, failures; its work steps
// are not messages. mu must be held.
func (p *Pane) newMessagesLocked() int {
	n := 0
	for _, e := range p.conv.Entries[min(p.readEntries, len(p.conv.Entries)):] {
		switch e.(type) {
		case AgentMessage, Plan, Questions, Approval, SecretRequest, Failure, UserMessage:
			n++
		}
	}
	return n
}

// headerTextLocked is the header without its close button: the title and
// what the agent does, in w cells; mu must be held.
func (p *Pane) headerTextLocked(w, scroll int) string {
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
	badge := ""
	if p.badge != "" && room > 12 {
		label := ansi.Truncate(" "+p.badge+" ", min(room/2, ansi.StringWidth(p.badge)+2), "…")
		room -= ansi.StringWidth(label) + 1
		badge = " " + p.styles.badge.Render(label)
	}
	return p.styles.bold.Render(ansi.Truncate(title, room, "…")) + badge + " · " + p.styles.muted.Render(state)
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
