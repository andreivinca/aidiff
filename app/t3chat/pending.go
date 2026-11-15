package t3chat

import (
	"fmt"
	"slices"

	"github.com/charmbracelet/x/ansi"
	"github.com/umputun/revdiff/app/t3client"
)

type queuedMessage struct {
	id, text        string
	sending, queued bool
}

func (p *Pane) removePendingLocked(id string) {
	p.pending = slices.DeleteFunc(p.pending, func(m queuedMessage) bool { return m.id == id })
}

func (p *Pane) onMessage(subscription uint64, m t3client.Message) {
	if m.Role != "user" {
		return
	} // assistant streaming is already rendered by turn-item events
	p.mu.Lock()
	if subscription == p.subID {
		p.messages[m.ID] = m
		p.gen++
	}
	p.mu.Unlock()
	p.signal()
}

// queuedMessagesLocked merges local sends with server queues by message ID.
// The server's queue survives reopening the pane; optimistic text survives
// the interval before its first server event. Never match by text.
func (p *Pane) queuedMessagesLocked() []queuedMessage {
	out := append([]queuedMessage(nil), p.pending...)
	upsert := func(m queuedMessage) {
		for i := range out {
			if out[i].id == m.id {
				out[i] = m
				return
			}
		}
		out = append(out, m)
	}
	var runs []t3client.Run
	for _, run := range p.runs {
		if !run.Active() {
			out = slices.DeleteFunc(out, func(m queuedMessage) bool { return m.id == run.UserMessageID })
		}
		if run.Status == "queued" || run.Status == "preparing" {
			runs = append(runs, run)
		}
	}
	slices.SortFunc(runs, func(a, b t3client.Run) int { return a.Ordinal - b.Ordinal })
	for _, run := range runs {
		if msg, ok := p.messages[run.UserMessageID]; ok && msg.Role == "user" {
			upsert(queuedMessage{id: msg.ID, text: msg.Text, queued: true})
		}
	}
	for _, it := range p.items {
		if it.Type == "user_message" && it.InputIntent == "queued_turn" && it.Status == "pending" && it.MessageID != "" {
			if it.RunID != nil {
				if run, ok := p.runs[*it.RunID]; ok && !run.Active() {
					continue
				}
			}
			upsert(queuedMessage{id: it.MessageID, text: it.Text, queued: true})
		}
	}
	return out
}

// pendingRowsLocked stays above the composer even as the assistant streams
// or the user scrolls back. Bound its height so the input remains accessible.
func (p *Pane) pendingRowsLocked(w, budget int) []string {
	if budget == 0 {
		return nil
	}
	queued := p.queuedMessagesLocked()
	padded := budget >= 3 && len(queued) > 0
	if padded {
		budget -= 2
	}
	var rows []string
	for _, m := range queued {
		label := "Sent"
		if m.queued {
			label = "Queued"
		}
		if m.sending {
			label = "Sending…"
		}
		panel := userPanel(m.text, w, p.styles)
		panel = panel[1 : len(panel)-1] // compact cards omit transcript padding
		if len(panel) > 2 {
			panel = append(panel[:1], p.styles.muted.Render(inset+"…"))
		}
		rows = append(rows, panel...)
		rows = append(rows, p.styles.muted.Render(inset+label))
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
