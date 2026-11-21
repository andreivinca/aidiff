package t3chat

import (
	"cmp"
	"encoding/json"
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
	decoded      map[string]*itemTexts    // what items' JSON fields read as, by item id (texts)
	secrets      map[string]bool          // the ids of the secret requests among allItems, which pendingSecret reads
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
		decoded:      map[string]*itemTexts{},
		secrets:      map[string]bool{},
	}
}

// itemTexts is what an item's JSON fields read as, each decoded once. The
// conversation is built again for every event, a streamed token included,
// and decoding every command's input and whole output each time made a long
// thread slow to stream into. The texts hold while the item keeps the same
// raw fields: an update decodes into new ones.
type itemTexts struct {
	input, output json.RawMessage // the fields the texts were decoded from
	in, out       string
	outDecoded    bool
}

// texts is the decoded texts of it, as cached for its id.
func (t *thread) texts(it t3client.TurnItem) *itemTexts {
	c := t.decoded[it.ID]
	if c == nil || !sameBytes(c.input, it.Input) || !sameBytes(c.output, it.Output) {
		c = &itemTexts{input: it.Input, output: it.Output, in: it.InputText()}
		t.decoded[it.ID] = c
	}
	return c
}

// outputText is the item's output as text, decoded the first time it is
// asked for.
func (c *itemTexts) outputText() string {
	if !c.outDecoded {
		c.out, c.outDecoded = t3client.JSONText(c.output), true
	}
	return c.out
}

// sameBytes reports whether a and b are the very same bytes, not merely
// equal ones: what an item kept since its last update.
func sameBytes(a, b []byte) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// load takes a snapshot: it replaces everything known of the thread.
func (t *thread) load(pr t3client.ThreadProjection) {
	*t = newThread(t.id)
	for _, m := range pr.Messages {
		t.message(m)
	}
	for _, it := range pr.TurnItems {
		t.merge(it)
	}
	ordered := slices.Clone(pr.VisibleTurnItems)
	slices.SortStableFunc(ordered, func(a, b t3client.ProjectedTurnItem) int {
		return cmp.Compare(a.Position, b.Position)
	})
	for _, row := range ordered {
		it := t.merge(row.Item)
		if i, ok := t.index[it.ID]; ok {
			t.items[i] = it
			continue
		}
		t.index[it.ID] = len(t.items) // the snapshot's own order, whatever the ordinals: a fork lists the items it inherits first
		t.items = append(t.items, it)
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

// upsert replaces an item by id or adds a new one. Fields the server left
// out of an update keep their last value. The transcript stays in T3's
// order, by ordinal (its snapshot lists items by ordinal, then id): a new
// item goes after the last one whose ordinal is not greater, and an item
// whose ordinal changed, as a queued message's does once it is promoted to
// run, moves, so the live transcript reads as a reconnect would show it.
// An item without an ordinal stays where it arrived.
func (t *thread) upsert(it t3client.TurnItem) {
	it = t.merge(it)
	i, known := t.index[it.ID]
	if known {
		moved := it.Ordinal > 0 && it.Ordinal != t.items[i].Ordinal
		t.items[i] = it
		if !moved {
			return
		}
		t.items = slices.Delete(t.items, i, i+1)
	}
	at := len(t.items)
	for it.Ordinal > 0 && at > 0 && t.items[at-1].Ordinal > it.Ordinal {
		at--
	}
	t.items = slices.Insert(t.items, at, it)
	if known {
		at = min(at, i)
	}
	for j := at; j < len(t.items); j++ {
		t.index[t.items[j].ID] = j
	}
}

// merge keeps the item among every item of the thread, with the fields an
// update left out at their last value, and returns it so.
func (t *thread) merge(it t3client.TurnItem) t3client.TurnItem {
	if previous, ok := t.allItems[it.ID]; ok {
		if it.PromptOmitted {
			it.Prompt = previous.Prompt
		}
		if it.ResultOmitted {
			it.Result = previous.Result
		}
	}
	t.allItems[it.ID] = it
	if it.Type == "secret_request" {
		t.secrets[it.ID] = true
	}
	return it
}

// visible applies the rule by which T3's snapshot picks the transcript's
// items (visibleTurnItems) to items that arrive live as well, and to items
// whose run ended since: nothing of a rolled-back run. A queued message
// taken back before its run started is left out by entries itself
// (cancelledBeforeStart).
func (t *thread) visible(it t3client.TurnItem) bool {
	return it.RunID == nil || t.runs[*it.RunID].Status != "rolled_back"
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
