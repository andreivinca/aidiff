package t3chat

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

func TestBackend_LiveItemsKeepTheSnapshotsOrder(t *testing.T) {
	run2, run3, run4 := "run-2", "run-3", "run-4"
	items := []t3client.TurnItem{
		{ID: "i1", Ordinal: 1, Type: "user_message", MessageID: "m1", Status: "completed", Text: "first question"},
		{ID: "a1", Ordinal: 2, RunID: &run2, Type: "assistant_message", Status: "running", Text: "working"},
		{ID: "q", Ordinal: 3, RunID: &run3, Type: "user_message", MessageID: "mq", InputIntent: "queued_turn", Status: "pending",
			Text: "queued follow-up"},
	}
	snap := t3client.ThreadProjection{
		Thread:   t3client.Thread{ID: "th-1", Title: "Order"},
		Messages: []t3client.Message{{ID: "mq", Role: "user", Text: "queued follow-up"}},
		Runs: []t3client.Run{{ID: run2, Ordinal: 2, Status: "running"},
			{ID: run3, Ordinal: 3, Status: "queued", UserMessageID: "mq"}},
	}
	for i, it := range items {
		snap.VisibleTurnItems = append(snap.VisibleTurnItems, t3client.ProjectedTurnItem{Position: i, Item: it})
	}
	f := newFakeServer(snap)
	s := subscribe(t, f)
	assert.Contains(t, s.v.latest().Queued, chat.Queued{ID: "mq", Text: "queued follow-up"})

	ev := s.b.events(s.b.sub)
	ev.TurnItem(t3client.TurnItem{ID: "a2", Ordinal: 4, RunID: &run2, Type: "assistant_message", Status: "completed",
		Text: "the rest of the first answer"})
	ev.Run(t3client.Run{ID: run2, Ordinal: 2, Status: "completed"})
	ev.Run(t3client.Run{ID: run3, Ordinal: 3, Status: "running", UserMessageID: "mq"})
	promoted := t3client.TurnItem{ID: "q", Ordinal: 5, RunID: &run3, Type: "user_message", MessageID: "mq",
		InputIntent: "queued_turn", Status: "completed", Text: "queued follow-up"}
	ev.TurnItem(promoted)
	ev.TurnItem(t3client.TurnItem{ID: "a3", Ordinal: 6, RunID: &run3, Type: "assistant_message", Status: "completed",
		Text: "answer to the follow-up"})
	want := []chat.Entry{
		chat.UserMessage{Text: "first question"},
		chat.AgentMessage{Markdown: "working"},
		chat.AgentMessage{Markdown: "the rest of the first answer"},
		chat.UserMessage{Text: "queued follow-up"},
		chat.AgentMessage{Markdown: "answer to the follow-up"},
	}
	live := s.v.latest().Entries
	live[1] = chat.AgentMessage{Markdown: "working"} // still marked as streaming: no update came for it
	assert.Equal(t, want, live, "a queued message promoted later comes after the earlier run's later output")

	ev.TurnItem(t3client.TurnItem{ID: "x", Ordinal: 7, RunID: &run4, Type: "assistant_message", Status: "completed", Text: "undone"})
	assert.Contains(t, s.v.latest().Entries, chat.Entry(chat.AgentMessage{Markdown: "undone"}))
	ev.Run(t3client.Run{ID: run4, Ordinal: 4, Status: "rolled_back"})
	assert.NotContains(t, s.v.latest().Entries, chat.Entry(chat.AgentMessage{Markdown: "undone"}),
		"what a rolled-back run wrote goes, as T3's snapshot leaves it out")

	var th thread
	th = newThread("th-1")
	reconnect := t3client.ThreadProjection{Thread: snap.Thread, Runs: []t3client.Run{{ID: run2, Ordinal: 2, Status: "completed"},
		{ID: run3, Ordinal: 3, Status: "running", UserMessageID: "mq"}}}
	for i, it := range []t3client.TurnItem{items[0], items[1], {ID: "a2", Ordinal: 4, RunID: &run2, Type: "assistant_message",
		Status: "completed", Text: "the rest of the first answer"}, promoted, {ID: "a3", Ordinal: 6, RunID: &run3,
		Type: "assistant_message", Status: "completed", Text: "answer to the follow-up"}} {
		reconnect.VisibleTurnItems = append(reconnect.VisibleTurnItems, t3client.ProjectedTurnItem{Position: i, Item: it})
	}
	th.load(reconnect)
	again := th.entries(nil)
	again[1] = chat.AgentMessage{Markdown: "working"}
	assert.Equal(t, want, again, "a reconnect shows the same order")
}

func TestThread_SnapshotOrderStandsAsGiven(t *testing.T) {
	th := newThread("th-1")
	th.load(t3client.ThreadProjection{VisibleTurnItems: []t3client.ProjectedTurnItem{
		{Position: 0, Item: t3client.TurnItem{ID: "inherited", Ordinal: 90, Type: "user_message", Text: "from the source thread"}},
		{Position: 1, Item: t3client.TurnItem{ID: "local", Ordinal: 1, Type: "user_message", Text: "here"}},
	}})
	assert.Equal(t, []chat.Entry{chat.UserMessage{Text: "from the source thread"}, chat.UserMessage{Text: "here"}}, th.entries(nil),
		"a fork lists what it inherits first, whatever the ordinals")
	th.upsert(t3client.TurnItem{ID: "next", Ordinal: 2, Type: "user_message", Text: "live"})
	assert.Equal(t, chat.UserMessage{Text: "live"}, th.entries(nil)[2], "a live item goes after the last item not later than it")
	assert.Equal(t, map[string]int{"inherited": 0, "local": 1, "next": 2}, th.index)
}

func TestBackend_PreparingRunIsQueued(t *testing.T) {
	snap := fixture()
	snap.Messages = []t3client.Message{{ID: "prep-msg", Role: "user", Text: "next, the signup page"}}
	snap.Runs = append(snap.Runs, t3client.Run{ID: "prep-run", Ordinal: 3, Status: "preparing", UserMessageID: "prep-msg"})
	f := newFakeServer(snap)
	s := subscribe(t, f)
	c := s.v.latest()
	assert.Equal(t, []chat.Queued{{ID: "prep-msg", Text: "next, the signup page"}}, c.Queued)
	assert.Equal(t, []string{"run-2", "prep-run"}, c.Running, "a run not started yet comes after the one running, as queued ones do")
	require.NoError(t, s.b.Unqueue(context.Background(), "prep-msg"), "↑ on its card takes it back")
	assert.Equal(t, t3client.QueuedRunCancelCommand("th-1", "prep-run"), nextCommand(t, f))
}
