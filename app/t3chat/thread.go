package t3chat

import (
	"cmp"
	"slices"
	"strings"

	"github.com/umputun/revdiff/app/t3client"
)

// thread is what a subscription told of one thread: its transcript, runs,
// requests and background work, and its settings.
type thread struct {
	id           string
	title        string
	items        []t3client.TurnItem          // the transcript, in order
	index        map[string]int               // items by id
	allItems     map[string]t3client.TurnItem // every item, the hidden ones included, which the activity list reads
	userMessages map[string]t3client.Message  // the user's messages by id, for the queued ones' text
	runs         map[string]t3client.Run
	requests     map[string]t3client.RuntimeRequest
	subagents    map[string]t3client.Subagent
	providers    map[string]t3client.ProviderThread
	providerID   string                   // the active provider thread, excluding older sessions' pending work
	model        *t3client.ModelSelection // the thread's model
	interaction  string                   // default (build) or plan, as reported by the thread
	runtimeMode  string                   // the thread's permission level
}

func newThread(id string) thread {
	return thread{
		id:           id,
		index:        map[string]int{},
		allItems:     map[string]t3client.TurnItem{},
		userMessages: map[string]t3client.Message{},
		runs:         map[string]t3client.Run{},
		requests:     map[string]t3client.RuntimeRequest{},
		subagents:    map[string]t3client.Subagent{},
		providers:    map[string]t3client.ProviderThread{},
	}
}

// load takes a snapshot: it replaces everything known of the thread.
func (t *thread) load(pr t3client.ThreadProjection) {
	*t = newThread(t.id)
	for _, m := range pr.Messages {
		t.message(m)
	}
	for _, it := range pr.TurnItems {
		t.allItems[it.ID] = it
	}
	ordered := slices.Clone(pr.VisibleTurnItems)
	slices.SortStableFunc(ordered, func(a, b t3client.ProjectedTurnItem) int {
		return cmp.Compare(a.Position, b.Position)
	})
	for _, it := range ordered {
		t.upsert(it.Item)
	}
	for _, r := range pr.Runs {
		t.runs[r.ID] = r
	}
	for _, r := range pr.RuntimeRequests {
		t.requests[r.ID] = r
	}
	for _, a := range pr.Subagents {
		t.subagents[a.ID] = a
	}
	for _, th := range pr.ProviderThreads {
		t.providers[th.ID] = th
	}
	t.metadata(pr.Thread)
}

// message takes a message of the user's and reports whether it was one;
// the assistant's streaming arrives as turn items instead.
func (t *thread) message(m t3client.Message) bool {
	if m.Role != "user" {
		return false
	}
	t.userMessages[m.ID] = m
	return true
}

// upsert replaces an item by id or appends a new one. Fields the server
// left out of an update keep their last value.
func (t *thread) upsert(it t3client.TurnItem) {
	if previous, ok := t.allItems[it.ID]; ok {
		if it.PromptOmitted {
			it.Prompt = previous.Prompt
		}
		if it.ResultOmitted {
			it.Result = previous.Result
		}
	}
	t.allItems[it.ID] = it
	if i, ok := t.index[it.ID]; ok {
		t.items[i] = it
		return
	}
	t.index[it.ID] = len(t.items)
	t.items = append(t.items, it)
}

// subagent takes a subagent's update; a prompt or result it left out keeps
// its last value.
func (t *thread) subagent(a t3client.Subagent) {
	previous := t.subagents[a.ID]
	if a.PromptOmitted {
		a.Prompt = previous.Prompt
	}
	if a.ResultOmitted {
		a.Result = previous.Result
	}
	t.subagents[a.ID] = a
}

// update takes the metadata of a thread event and reports whether it was
// this thread's.
func (t *thread) update(th t3client.Thread) bool {
	if th.ID != t.id {
		return false
	}
	t.metadata(th)
	return true
}

// metadata takes the thread's title and settings, changed here or in
// another T3 client. Metadata events may leave out the fields that did not
// change.
func (t *thread) metadata(th t3client.Thread) {
	if title := strings.TrimSpace(th.Title); title != "" {
		t.title = title
	}
	if th.ActiveProviderThreadID != nil {
		t.providerID = *th.ActiveProviderThreadID
	}
	if th.ModelSelection != nil && th.ModelSelection.Model != "" {
		t.model = th.ModelSelection
	}
	if th.InteractionMode != "" {
		t.interaction = th.InteractionMode
	}
	if th.RuntimeMode != "" {
		t.runtimeMode = th.RuntimeMode
	}
}

// hasUserMessage reports whether the thread holds a message of the user's.
func (t *thread) hasUserMessage() bool {
	if len(t.userMessages) > 0 {
		return true
	}
	return slices.ContainsFunc(t.items, func(it t3client.TurnItem) bool {
		return it.Type == "user_message"
	})
}

// awaits reports whether the agent still waits on the prompt item it: not
// done, and not settled as a request.
func (t *thread) awaits(it t3client.TurnItem) bool {
	if it.Done() {
		return false
	}
	r, known := t.requests[it.RequestID]
	return !known || r.Pending()
}
