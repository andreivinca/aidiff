package chat

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/clipperhouse/uax29/v2/graphemes"
)

// maxPendingRows bounds the queued messages above the composer.
const maxPendingRows = 8

// queuedMessage is a card above the composer: a message sent from here that
// the backend has not taken over yet, or one the backend holds queued.
type queuedMessage struct {
	id, text        string
	sending, queued bool
}

func (p *Pane) removePendingLocked(id string) {
	p.pending = slices.DeleteFunc(p.pending, func(m queuedMessage) bool {
		return m.id == id
	})
}

// prunePendingLocked drops the cards of sent messages the backend took
// over. A card is matched by its message id, never by text. mu must be
// held.
func (p *Pane) prunePendingLocked() {
	if len(p.pending) == 0 {
		return
	}
	p.pending = slices.DeleteFunc(p.pending, func(m queuedMessage) bool {
		return slices.Contains(p.conv.Delivered, m.id)
	})
}

// queuedMessagesLocked merges the local cards with the backend's queue by
// message id. The backend's queue outlives the pane; a local card covers
// the interval before the backend's first word about its message. mu must
// be held.
func (p *Pane) queuedMessagesLocked() []queuedMessage {
	out := slices.Clone(p.pending)
	for _, m := range p.conv.Queued {
		card := queuedMessage{id: m.ID, text: m.Text, queued: true}
		i := slices.IndexFunc(out, func(q queuedMessage) bool {
			return q.id == m.ID
		})
		if i < 0 {
			out = append(out, card)
			continue
		}
		out[i] = card
	}
	return out
}

// pendingRowsLocked shows the queued cards above the composer, where they
// stay even as the agent streams or the user scrolls back. Their height is
// bounded so the input remains accessible.
func (p *Pane) pendingRowsLocked(queued []queuedMessage, w, budget int) []string {
	if budget == 0 {
		return nil
	}
	padded := budget >= 3 && len(queued) > 0
	if padded {
		budget -= 2
	}
	var rows []string
	for i, m := range queued {
		if i > 0 {
			rows = append(rows, "") // a blank row under each card's state and above the next card
		}
		rows = append(rows, p.pendingCard(m, w)...)
	}
	if len(rows) > budget {
		// Keep a label and the latest text, making truncation explicit.
		label := fmt.Sprintf("%d pending message%s", len(queued), plural(len(queued)))
		rows = append([]string{p.styles.muted.Render(ansi.Truncate(inset+label, w, "…"))}, rows[len(rows)-(budget-1):]...)
	}
	if padded {
		rows = append([]string{""}, rows...)
		rows = append(rows, "")
	}
	return rows
}

// pendingCard is one queued message: its first row of text and its state.
func (p *Pane) pendingCard(m queuedMessage, w int) []string {
	label := "Sent"
	if m.queued {
		label = "Queued"
	}
	if m.sending {
		label = "Sending…"
	}
	lines := panelLines(m.text, w)
	lines = lines[1 : len(lines)-1] // compact cards omit transcript padding
	var panel []string
	for _, l := range lines[:min(len(lines), 2)] {
		panel = append(panel, p.styles.rail.Render(rail)+stripedPanel(panelText(l, w), p.styles))
	}
	if len(lines) > 2 {
		panel = append(panel[:1], p.styles.muted.Render(inset+"…"))
	}
	return append(panel, p.styles.muted.Render(inset+label))
}

// stripedPanel paints a queued message's panel row: its text dimmed, in
// vertical stripes a cell wide, the panel's background then a shade lighter,
// from the panel's first cell. A wide character takes the shade of the cell
// it starts on.
func stripedPanel(row string, st styles) string {
	var b strings.Builder
	col := 0
	g := graphemes.FromString(row)
	for g.Next() {
		cell := g.Value()
		style := st.queued
		if col%2 == 1 {
			style = st.queuedStripe
		}
		b.WriteString(style.Render(cell))
		col += max(1, ansi.StringWidth(cell))
	}
	return b.String()
}
