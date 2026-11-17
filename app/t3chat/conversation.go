package t3chat

import (
	"cmp"
	"slices"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// conversationLocked is the thread as the pane shows it; mu must be held.
func (b *Backend) conversationLocked() chat.Conversation {
	t := &b.thread
	queued := b.withPendingLocked(t.queue())
	return chat.Conversation{
		Title:     t.title,
		State:     t.state(),
		Running:   t.activeRuns(),
		Entries:   t.entries(queued),
		Queued:    queued,
		Delivered: t.delivered(),
		Activity:  t.activity(),
		Settings:  b.settingsLocked(),
	}
}

// state derives what the agent does from its runs and open requests.
func (t *thread) state() chat.AgentState {
	for _, r := range t.requests {
		if r.Pending() {
			return chat.AgentWaiting
		}
	}
	var latest *t3client.Run
	for id := range t.runs {
		r := t.runs[id]
		if r.Status == "cancelled" {
			continue // a queued message taken back: it never ran, so it says nothing of the agent
		}
		if latest == nil || r.Ordinal > latest.Ordinal {
			latest = &r
		}
	}
	switch {
	case latest == nil:
		return chat.AgentIdle
	case latest.Status == "waiting":
		return chat.AgentWaiting
	case latest.Active():
		return chat.AgentRunning
	case latest.Status == "completed":
		return chat.AgentIdle
	}
	return chat.AgentState(latest.Status)
}

// activeRuns are the runs the stop dialog may stop, the one to stop first:
// the runs actually running, newest first, then those still queued. While
// no run runs but work it launched goes on in the background (subagents,
// background commands), the latest run that started stands for it, so the
// user can still stop the agent.
func (t *thread) activeRuns() []string {
	var running, queued []t3client.Run
	var latest *t3client.Run
	for id := range t.runs {
		r := t.runs[id]
		switch {
		case r.Status == "queued":
			queued = append(queued, r)
		case r.Active():
			running = append(running, r)
		}
		if r.Status != "queued" && (latest == nil || r.Ordinal > latest.Ordinal) {
			latest = &r
		}
	}
	newest := func(a, b t3client.Run) int {
		return cmp.Compare(b.Ordinal, a.Ordinal)
	}
	slices.SortFunc(running, newest)
	slices.SortFunc(queued, newest)
	if len(running) == 0 && latest != nil && t.backgroundWork() {
		running = append(running, *latest)
	}
	ids := make([]string, 0, len(running)+len(queued))
	for _, r := range append(running, queued...) {
		ids = append(ids, r.ID)
	}
	return ids
}

// backgroundWork reports whether work of the agent's goes on outside a run:
// a subagent, a background command or a monitor not done yet.
func (t *thread) backgroundWork() bool {
	return slices.ContainsFunc(t.activity(), func(a chat.Activity) bool {
		return a.State == chat.Pending || a.State == chat.Running || a.State == chat.Waiting
	})
}

// queue is what the server holds queued: the messages of runs not started
// yet, in their order, then the queued turns of the transcript.
func (t *thread) queue() []chat.Queued {
	var runs []t3client.Run
	for _, r := range t.runs {
		if r.Status == "queued" || r.Status == "preparing" {
			runs = append(runs, r)
		}
	}
	slices.SortFunc(runs, func(a, b t3client.Run) int {
		return cmp.Compare(a.Ordinal, b.Ordinal)
	})
	var out []chat.Queued
	for _, r := range runs {
		if msg, ok := t.userMessages[r.UserMessageID]; ok {
			out = append(out, chat.Queued{ID: msg.ID, Text: msg.Text, Images: len(msg.Attachments)})
		}
	}
	for _, it := range t.items {
		if t.queuedTurn(it) {
			out = append(out, chat.Queued{ID: it.MessageID, Text: it.Text})
		}
	}
	return out
}

// holdsQueue reports any queued message, its text known or not.
func (t *thread) holdsQueue() bool {
	for _, r := range t.runs {
		if r.Status == "queued" || r.Status == "preparing" {
			return true
		}
	}
	return slices.ContainsFunc(t.items, t.queuedTurn)
}

// queuedRunOf is the run of the queued message messageID that has not
// started, "" when there is none.
func (t *thread) queuedRunOf(messageID string) string {
	for _, r := range t.runs {
		if r.UserMessageID == messageID && r.Status == "queued" {
			return r.ID
		}
	}
	for _, it := range t.items {
		if it.MessageID == messageID && t.queuedTurn(it) && it.RunID != nil {
			if r, ok := t.runs[*it.RunID]; !ok || r.Status == "queued" {
				return *it.RunID
			}
		}
	}
	return ""
}

// cancelledBeforeStart reports a row of a message taken back from the queue:
// its run was cancelled, so it never went to the agent.
func (t *thread) cancelledBeforeStart(it t3client.TurnItem) bool {
	if it.RunID == nil {
		return false
	}
	r, ok := t.runs[*it.RunID]
	return ok && r.Status == "cancelled"
}

// queuedTurn reports a transcript row of a message waiting for its turn.
func (t *thread) queuedTurn(it t3client.TurnItem) bool {
	if it.Type != "user_message" || it.InputIntent != "queued_turn" || it.Status != "pending" || it.MessageID == "" {
		return false
	}
	if it.RunID == nil {
		return true
	}
	run, ok := t.runs[*it.RunID]
	return !ok || run.Active()
}

// delivered are the messages the server took over: the transcript shows
// them, or their run ended. A message is known by its id, never by its text.
func (t *thread) delivered() []string {
	var out []string
	for _, it := range t.items {
		if it.Type == "user_message" && it.MessageID != "" {
			out = append(out, it.MessageID)
		}
	}
	for _, r := range t.runs {
		if !r.Active() && r.UserMessageID != "" {
			out = append(out, r.UserMessageID)
		}
	}
	return out
}

// entries is the transcript as the pane shows it, without the queued
// messages, which it shows apart, and without T3's own turn markers.
func (t *thread) entries(queued []chat.Queued) []chat.Entry {
	waiting := make(map[string]bool, len(queued))
	for _, q := range queued {
		waiting[q.ID] = true
	}
	out := make([]chat.Entry, 0, len(t.items))
	for _, it := range t.items {
		if it.Type == "checkpoint" || (it.Type == "user_message" && (waiting[it.MessageID] || t.cancelledBeforeStart(it))) {
			continue
		}
		out = append(out, t.entry(it))
	}
	return out
}
