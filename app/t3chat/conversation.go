package t3chat

import (
	"cmp"
	"slices"
	"time"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// conversationLocked is the thread as the pane shows it. It runs for every
// event, a streamed token included, so what more than one part reads (the
// pending secret, the activity) is derived once; mu must be held.
func (b *Backend) conversationLocked() chat.Conversation {
	t := &b.thread
	queued := b.withPendingLocked(t.queue())
	secret := t.pendingSecret()
	activity := t.activity()
	running := t.activeRunsWith(activity)
	var since time.Time
	if len(running) > 0 {
		since = t.runs[running[0]].Since()
	}
	return chat.Conversation{
		Title:        t.title,
		State:        t.stateWith(secret),
		Running:      running,
		RunningSince: since,
		Entries:      t.entries(queued),
		Queued:       queued,
		Delivered:    t.delivered(),
		Activity:     activity,
		Settings:     b.settingsLocked(),
		Secret:       secret,
	}
}

// pendingSecret is the newest secret request T3 still waits for, among every
// item of the thread, nil when there is none.
func (t *thread) pendingSecret() *chat.SecretRequest {
	var latest *t3client.TurnItem
	for id := range t.secrets {
		it := t.allItems[id]
		if !secretPending(it) || (it.RunID != nil && t.runs[*it.RunID].Status == "rolled_back") {
			continue
		}
		if latest == nil || it.Ordinal > latest.Ordinal {
			latest = &it
		}
	}
	if latest == nil {
		return nil
	}
	r := secretRequest(*latest)
	return &r
}

// state derives what the agent does from its runs and open requests.
func (t *thread) state() chat.AgentState {
	return t.stateWith(t.pendingSecret())
}

// stateWith is state, given the pending secret request.
func (t *thread) stateWith(secret *chat.SecretRequest) chat.AgentState {
	if secret != nil {
		return chat.AgentWaiting // the agent's tool call blocks until the user answers
	}
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
	return t.activeRunsWith(nil)
}

// activeRunsWith is activeRuns, given the thread's activity when it is
// known already; nil derives it if it is needed.
func (t *thread) activeRunsWith(activity []chat.Activity) []string {
	var running, queued []t3client.Run
	var latest *t3client.Run
	for id := range t.runs {
		r := t.runs[id]
		switch {
		case notStarted(r):
			queued = append(queued, r)
		case r.Active():
			running = append(running, r)
		}
		if !notStarted(r) && (latest == nil || r.Ordinal > latest.Ordinal) {
			latest = &r
		}
	}
	newest := func(a, b t3client.Run) int {
		return cmp.Compare(b.Ordinal, a.Ordinal)
	}
	slices.SortFunc(running, newest)
	slices.SortFunc(queued, newest)
	if len(running) == 0 && latest != nil && t.backgroundWork(activity) {
		running = append(running, *latest)
	}
	ids := make([]string, 0, len(running)+len(queued))
	for _, r := range append(running, queued...) {
		ids = append(ids, r.ID)
	}
	return ids
}

// backgroundWork reports whether work of the agent's goes on outside a run:
// a subagent, a background command or a monitor not done yet. activity is
// the thread's, derived here when nil.
func (t *thread) backgroundWork(activity []chat.Activity) bool {
	if activity == nil {
		activity = t.activity()
	}
	return slices.ContainsFunc(activity, func(a chat.Activity) bool {
		return a.State == chat.Pending || a.State == chat.Running || a.State == chat.Waiting
	})
}

// notStarted reports a run that has not started: queued, or preparing to
// start. Its message is queued, the stop dialog lists it after the runs
// actually running, and ↑ can take it back.
func notStarted(r t3client.Run) bool {
	return r.Status == "queued" || r.Status == "preparing"
}

// queue is what the server holds queued: the messages of runs not started
// yet, in their order, then the queued turns of the transcript.
func (t *thread) queue() []chat.Queued {
	var runs []t3client.Run
	for _, r := range t.runs {
		if notStarted(r) {
			runs = append(runs, r)
		}
	}
	slices.SortFunc(runs, func(a, b t3client.Run) int {
		return cmp.Compare(a.Ordinal, b.Ordinal)
	})
	out := t.unreadSteers()
	for _, r := range runs {
		if msg, ok := t.userMessages[r.UserMessageID]; ok {
			images, files := countAttachments(msg.Attachments)
			out = append(out, chat.Queued{ID: msg.ID, Text: msg.Text, Images: images, Files: files})
		}
	}
	for _, it := range t.items {
		if t.queuedTurn(it) {
			out = append(out, chat.Queued{ID: it.MessageID, Text: it.Text})
		}
	}
	return out
}

// unreadSteers are the messages sent into the running turn that the agent
// has not read yet, oldest first.
func (t *thread) unreadSteers() []chat.Queued {
	var steers []t3client.TurnItem
	agent := t.agentOrdinal()
	for _, it := range t.items {
		if t.unreadSteer(it, agent) {
			steers = append(steers, it)
		}
	}
	slices.SortStableFunc(steers, func(a, b t3client.TurnItem) int {
		return cmp.Compare(a.Ordinal, b.Ordinal)
	})
	out := make([]chat.Queued, 0, len(steers))
	for _, it := range steers {
		out = append(out, chat.Queued{ID: it.MessageID, Text: it.Text, Steer: true})
	}
	return out
}

// agentOrdinal is the transcript position of the agent's latest thought or
// reply, 0 when there is none: a steer before it has been read. Derived
// once per pass, as the steers are checked one by one against it.
func (t *thread) agentOrdinal() int {
	latest := 0
	for _, o := range t.items {
		if o.Type == "assistant_message" || o.Type == "reasoning" {
			latest = max(latest, o.Ordinal)
		}
	}
	return latest
}

// unreadSteer reports a message sent into the running turn that the agent
// has not read yet. A provider that says when it reads one (Pi) keeps the
// row pending until then. Claude's says nothing: the row is complete as soon
// as the message is handed over, and the agent reads it at its next step,
// so the message counts as read once the agent starts thinking or writing
// after it (agent is agentOrdinal), or its run ends.
func (t *thread) unreadSteer(it t3client.TurnItem, agent int) bool {
	if it.Type != "user_message" || it.MessageID == "" || (it.InputIntent != "steer" && it.InputIntent != "promoted_queued_to_steer") {
		return false
	}
	switch it.Status {
	case "pending":
		return true
	case "completed":
	default:
		return false // cancelled or failed: it never reached the agent, nor will
	}
	if it.RunID != nil {
		if r, ok := t.runs[*it.RunID]; ok && !r.Active() {
			return false
		}
	}
	return agent <= it.Ordinal
}

// holdsQueue reports any queued message, its text known or not.
func (t *thread) holdsQueue() bool {
	for _, r := range t.runs {
		if notStarted(r) {
			return true
		}
	}
	return slices.ContainsFunc(t.items, t.queuedTurn)
}

// queuedRunOf is the run of the queued message messageID that has not
// started, "" when there is none.
func (t *thread) queuedRunOf(messageID string) string {
	for _, r := range t.runs {
		if r.UserMessageID == messageID && notStarted(r) {
			return r.ID
		}
	}
	for _, it := range t.items {
		if it.MessageID == messageID && t.queuedTurn(it) && it.RunID != nil {
			if r, ok := t.runs[*it.RunID]; !ok || notStarted(r) {
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
	agent := t.agentOrdinal()
	for _, it := range t.items {
		if it.Type == "user_message" && it.MessageID != "" && !t.unreadSteer(it, agent) {
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
		if it.Type == "checkpoint" || !t.visible(it) || (it.Type == "user_message" && (waiting[it.MessageID] || t.cancelledBeforeStart(it))) {
			continue
		}
		out = append(out, t.entry(it))
	}
	return out
}
