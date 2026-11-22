package t3chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

func TestBackend_ShowsTheThread(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	c := s.v.latest()
	assert.Equal(t, "Fix login", c.Title)
	assert.Equal(t, chat.AgentRunning, c.State)
	assert.Equal(t, []string{"run-2"}, c.Running)
	assert.Equal(t, []chat.Entry{
		chat.UserMessage{Text: "please fix the login page"},
		chat.Step{Kind: chat.StepThought, Text: "secret thoughts"},
		chat.Step{Kind: chat.StepCommand, Text: "go test ./...", State: chat.Failed, Why: "exit 1"},
		chat.Step{Kind: chat.StepEdit, Text: "login.go +3 −1"},
		chat.AgentMessage{Markdown: "Looking at the handler", Streaming: true},
	}, c.Entries, "items follow their position; T3's checkpoints are left out")
	assert.Equal(t, []string{"m1"}, c.Delivered)

	s.b.events(s.b.sub).TurnItem(t3client.TurnItem{ID: "i5", Type: "assistant_message", Status: "completed", Text: "Found it"})
	s.b.events(s.b.sub).Run(t3client.Run{ID: "run-2", Ordinal: 2, Status: "completed"})
	c = s.v.latest()
	assert.Equal(t, chat.AgentMessage{Markdown: "Found it"}, c.Entries[4], "an item is replaced in place")
	assert.Equal(t, chat.AgentIdle, c.State)
	assert.Empty(t, c.Running)

	f.live().Run(t3client.Run{ID: "run-3", Ordinal: 3, Status: "interrupted"})
	assert.Equal(t, chat.AgentState("interrupted"), s.v.latest().State, "a run that ended otherwise names how")
	f.live().Request(t3client.RuntimeRequest{ID: "req", Status: "pending"})
	assert.Equal(t, chat.AgentWaiting, s.v.latest().State, "an open request waits for the user")
}

func TestBackend_StepsOfEveryKind(t *testing.T) {
	for _, tc := range []struct {
		item t3client.TurnItem
		want chat.Step
	}{
		{t3client.TurnItem{Type: "reasoning", Status: "running", Text: "pondering"},
			chat.Step{Kind: chat.StepThought, Text: "pondering", State: chat.Running}},
		{t3client.TurnItem{Type: "reasoning", Status: "completed", Streaming: true, Text: "still"},
			chat.Step{Kind: chat.StepThought, Text: "still", State: chat.Running}},
		{t3client.TurnItem{Type: "command_execution", Status: "pending", Input: json.RawMessage(`"make"`)},
			chat.Step{Kind: chat.StepCommand, Text: "make", State: chat.Pending}},
		{t3client.TurnItem{Type: "command_execution", Status: "failed", Input: json.RawMessage(`"make"`), ExitCode: new(2)},
			chat.Step{Kind: chat.StepCommand, Text: "make", State: chat.Failed}},
		{t3client.TurnItem{Type: "file_change", Status: "completed", FileName: "a.go"},
			chat.Step{Kind: chat.StepEdit, Text: "a.go"}},
		{t3client.TurnItem{Type: "file_search", Status: "waiting", Pattern: "*.go"},
			chat.Step{Kind: chat.StepSearch, Text: "*.go", State: chat.Waiting}},
		{t3client.TurnItem{Type: "web_search", Status: "completed", Patterns: []string{"go", "modules"}},
			chat.Step{Kind: chat.StepWeb, Text: "go, modules"}},
		{t3client.TurnItem{Type: "dynamic_tool", Status: "idle", ToolName: new("lint"), Input: json.RawMessage(`{"path":"."}`)},
			chat.Step{Kind: chat.StepTool, Text: `lint {"path":"."}`}},
		{t3client.TurnItem{Type: "dynamic_tool", Status: "completed"},
			chat.Step{Kind: chat.StepTool, Text: "tool "}},
		{t3client.TurnItem{Type: "subagent", Status: "running", Prompt: "review it"},
			chat.Step{Kind: chat.StepSubagent, Text: "review it", State: chat.Running}},
		{t3client.TurnItem{Type: "system_notice", Status: "completed", Message: "context is full"},
			chat.Step{Kind: chat.StepNotice, Text: "context is full"}},
		{t3client.TurnItem{Type: "run_interrupt_result", Status: "completed", Message: "stopped"},
			chat.Step{Kind: chat.StepInterrupt, Text: "stopped"}},
		{t3client.TurnItem{Type: "compaction", Status: "completed"},
			chat.Step{Kind: chat.StepCompaction, Text: "context compacted"}},
		{t3client.TurnItem{Type: "image_generation", Status: "", Title: new("logo")},
			chat.Step{Kind: chat.StepOther, Text: "image generation: logo", State: chat.Waiting}},
	} {
		assert.Equal(t, tc.want, step(tc.item), "%s %s", tc.item.Type, tc.item.Status)
	}
}

func TestBackend_ShowsTheConversationInFull(t *testing.T) {
	th := newThread("th-1")
	th.load(t3client.ThreadProjection{
		RuntimeRequests: []t3client.RuntimeRequest{{ID: "settled", Status: "resolved"}},
		VisibleTurnItems: []t3client.ProjectedTurnItem{
			{Position: 1, Item: t3client.TurnItem{ID: "p", Type: "proposed_plan", Markdown: "1. read"}},
			{Position: 2, Item: t3client.TurnItem{ID: "t", Type: "todo_list", Steps: []t3client.TodoStep{
				{Text: "read", Status: "completed"}, {Text: "fix", Status: "running"}, {Text: "test", Status: "pending"}}}},
			{Position: 3, Item: t3client.TurnItem{ID: "e", Type: "error", Failure: json.RawMessage(`{"message":"rate limited"}`)}},
			{Position: 4, Item: t3client.TurnItem{ID: "a1", Type: "approval_request", Status: "waiting", RequestID: "open",
				Prompt: "Run rm?", Reason: "cleanup"}},
			{Position: 5, Item: t3client.TurnItem{ID: "a2", Type: "approval_request", Status: "waiting", RequestID: "settled"}},
			{Position: 6, Item: t3client.TurnItem{ID: "a3", Type: "approval_request", Status: "completed", RequestID: "done"}},
			{Position: 7, Item: t3client.TurnItem{ID: "q", Type: "user_input_request", Status: "waiting", RequestID: "ask",
				Questions: []t3client.Question{{ID: "schema", Question: "Strict?", MultiSelect: true, AllowCustomAnswer: true,
					Options: []t3client.QuestionOption{{Label: "Strict", Description: "precise", Value: "strict"}, {Label: "Loose"}}}}}},
		},
	})
	assert.Equal(t, []chat.Entry{
		chat.Plan{Markdown: "1. read"},
		chat.TodoList{Steps: []chat.TodoStep{{Text: "read", State: chat.Done}, {Text: "fix", State: chat.Running}, {Text: "test", State: chat.Pending}}},
		chat.Failure{Text: "rate limited"},
		chat.Approval{ID: "open", Prompt: "Run rm?", Reason: "cleanup", Pending: true},
		chat.Approval{ID: "settled"},
		chat.Approval{ID: "done"},
		chat.Questions{ID: "ask", Pending: true, Questions: []chat.Question{{ID: "schema", Text: "Strict?", Multiple: true, OwnWords: true,
			Options: []chat.Option{{Label: "Strict", Description: "precise", Value: "strict"}, {Label: "Loose", Value: "Loose"}}}}},
	}, th.entries(nil), "an option without a value answers with its label")
}

func TestBackend_QueuedMessages(t *testing.T) {
	snap := fixture()
	snap.Messages = []t3client.Message{{ID: "queued-msg", Role: "user", Text: "Please check tests too"}, {ID: "x", Role: "assistant"}}
	snap.Runs = append(snap.Runs, t3client.Run{ID: "queued-run", Ordinal: 3, Status: "queued", UserMessageID: "queued-msg"})
	f := newFakeServer(snap)
	s := subscribe(t, f)
	assert.Equal(t, []chat.Queued{{ID: "queued-msg", Text: "Please check tests too"}}, s.v.latest().Queued)

	f.live().Message(t3client.Message{ID: "queued-msg", Role: "user", Text: "Check the updated tests"})
	assert.Equal(t, "Check the updated tests", s.v.latest().Queued[0].Text, "a queue update from another client shows here too")
	f.live().TurnItem(t3client.TurnItem{ID: "queued-row", MessageID: "queued-msg", RunID: new("queued-run"), Type: "user_message",
		InputIntent: "queued_turn", Status: "pending", Text: "Check the updated tests"})
	c := s.v.latest()
	assert.Len(t, c.Queued, 2, "the run's message and its queued turn, merged by id in the pane")
	assert.NotContains(t, c.Entries, chat.UserMessage{Text: "Check the updated tests"}, "queued messages are not in the transcript")
	assert.Contains(t, c.Delivered, "queued-msg", "the server holds it: the pane's own card goes")

	f.live().Run(t3client.Run{ID: "queued-run", Ordinal: 3, Status: "running", UserMessageID: "queued-msg"})
	f.live().TurnItem(t3client.TurnItem{ID: "queued-row", MessageID: "queued-msg", Type: "user_message", InputIntent: "turn_start",
		Status: "completed", Text: "Check the updated tests"})
	c = s.v.latest()
	assert.Empty(t, c.Queued)
	assert.Contains(t, c.Entries, chat.UserMessage{Text: "Check the updated tests"})

	f.live().Run(t3client.Run{ID: "cancelled-run", Ordinal: 4, Status: "cancelled", UserMessageID: "sent-here"}) //nolint:misspell // T3's spelling
	assert.Contains(t, s.v.latest().Delivered, "sent-here", "a message whose run ended is done with")
	assert.Empty(t, f.cmds, "showing the queue sends nothing")
}

func TestBackend_Activity(t *testing.T) {
	snap := fixture()
	snap.Thread.ActiveProviderThreadID = new("provider-1")
	snap.Subagents = []t3client.Subagent{{ID: "a1", Title: "Security review", Prompt: "Review login.go for security issues",
		Status: "running", Progress: "Checking authentication", ChildThreadID: "child-1", Model: "review-model", UpdatedAt: "2026-01-02",
		NativeTaskRef: &t3client.ProviderRef{NativeID: "agent-native-1"}}}
	snap.TurnItems = []t3client.TurnItem{
		{ID: "agent-row", Type: "subagent", SubagentID: "a1", ChildThreadID: "child-1", Status: "running", Prompt: "Review login.go"},
		{ID: "cmd-1", Type: "command_execution", Status: "completed", Input: json.RawMessage(`"npm run dev"`), UpdatedAt: "2026-01-01",
			NativeItemRef: &t3client.ProviderRef{NativeID: "native-cmd-1"}, Output: json.RawMessage(`"Ready on localhost:3000"`), ExitCode: new(0)},
		{ID: "cmd-2", Type: "command_execution", Status: "completed", Input: json.RawMessage(`"ls"`), OutputOmitted: true, Ordinal: 3},
	}
	snap.ProviderThreads = []t3client.ProviderThread{
		{ID: "provider-1", PendingBackgroundTasks: []t3client.BackgroundTask{
			{TaskID: "native-cmd-1", Kind: "command", Awaited: true},
			{TaskID: "agent-native-1", Kind: "subagent", ChildThreadID: "child-1", Description: "Duplicate review task"},
			{TaskID: "monitor-1", Kind: "monitor", Description: "Watch test results"},
			{TaskID: "future-kind", Kind: "future_kind", Command: "./crawl"},
		}},
		{ID: "old-provider", PendingBackgroundTasks: []t3client.BackgroundTask{{TaskID: "old-task", Kind: "command", Description: "Old session process"}}},
	}
	snap.Runs = append(snap.Runs, t3client.Run{ID: "rollback", Ordinal: 3, Status: "rolled_back"})
	snap.Subagents = append(snap.Subagents, t3client.Subagent{ID: "rolled-back-agent", Status: "running", RunID: new("rollback")})
	f := newFakeServer(snap)
	s := subscribe(t, f)
	assert.Equal(t, []chat.Activity{
		{ID: "agent:a1", Agent: true, Title: "Security review", State: chat.Running, Status: "running", Progress: "Checking authentication",
			Details: []string{"Review login.go for security issues", "Checking authentication", "Model: review-model", "Thread: child-1"}},
		{ID: "item:cmd-1", Title: "npm run dev", State: chat.Waiting, Status: "waiting",
			Details: []string{"npm run dev", "Ready on localhost:3000", "Exit code: 0"}},
		{ID: "item:cmd-2", Title: "ls", Status: "completed", Details: []string{"ls", "Output is not included in the live activity list."}},
		{ID: "item:i3", Title: "go test ./...", Status: "completed", Details: []string{"go test ./...", "Exit code: 1"}},
		{ID: "task:provider-1:future-kind", Title: "./crawl", State: chat.Running, Status: "running", Details: []string{"./crawl"}},
		{ID: "task:provider-1:monitor-1", Title: "monitor · Watch test results", State: chat.Running, Status: "running",
			Details: []string{"Watch test results"}},
	}, s.v.latest().Activity, "joined by the server's ids, the transcript's commands included, most recent first; old sessions and rolled-back runs are left out")

	f.live().Subagent(t3client.Subagent{ID: "a1", Title: "Security review", Status: "completed", PromptOmitted: true,
		Result: "No issues found", UpdatedAt: "2026-01-03"})
	agent := s.v.latest().Activity[0]
	assert.Equal(t, chat.Done, agent.State)
	assert.Equal(t, []string{"Review login.go for security issues", "No issues found"}, agent.Details, "an update keeps the prompt it left out")

	f.live().ProviderThread(t3client.ProviderThread{ID: "provider-1"})
	for _, a := range s.v.latest().Activity {
		assert.NotEqual(t, chat.Running, a.State, "an empty task list ends the processes: %s", a.Title)
	}
}

func TestBackend_DropsOtherThreadsAndOldSubscriptions(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	old := s.b.events(s.b.sub)
	f.live().Thread(t3client.Thread{ID: "other", Title: "Not this one"})
	assert.Equal(t, "Fix login", s.v.latest().Title, "another thread's metadata changes nothing")

	again := newFakeViewer()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = s.b.Subscribe(ctx, again)
	}()
	<-f.subscribed
	again.next(t)
	old.TurnItem(t3client.TurnItem{ID: "stale", Type: "user_message", Text: "from the old subscription"})
	assert.NotContains(t, again.latest().Entries, chat.UserMessage{Text: "from the old subscription"},
		"events of a replaced subscription are dropped")
	require.Equal(t, 1, f.fetches(), "a new subscription on the same server fetches no catalog")
}

func TestThread_RunsTheStopDialogOffers(t *testing.T) {
	th := newThread("th-1")
	th.runs["run-1"] = t3client.Run{ID: "run-1", Ordinal: 1, Status: "completed"}
	assert.Empty(t, th.activeRuns(), "nothing goes on")

	th.runs["run-2"] = t3client.Run{ID: "run-2", Ordinal: 2, Status: "running"}
	th.runs["run-3"] = t3client.Run{ID: "run-3", Ordinal: 3, Status: "queued"}
	assert.Equal(t, []string{"run-2", "run-3"}, th.activeRuns(), "the running run comes before a newer queued one")

	th.runs["run-2"] = t3client.Run{ID: "run-2", Ordinal: 2, Status: "completed"}
	assert.Equal(t, []string{"run-3"}, th.activeRuns(), "only a queued run left")

	delete(th.runs, "run-3")
	th.subagents["a1"] = t3client.Subagent{ID: "a1", Title: "review", Status: "running"}
	assert.Equal(t, []string{"run-2"}, th.activeRuns(), "a subagent still working keeps the agent stoppable")

	th.subagents["a1"] = t3client.Subagent{ID: "a1", Title: "review", Status: "completed"}
	assert.Empty(t, th.activeRuns())
}

func TestBackend_SteerQueuedUntilRead(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	f.live().TurnItem(t3client.TurnItem{ID: "steer-row", MessageID: "steer-msg", RunID: new("run-2"), Type: "user_message",
		InputIntent: "steer", Status: "completed", Text: "also update the docs", Ordinal: 10})
	c := s.v.latest()
	assert.Equal(t, []chat.Queued{{ID: "steer-msg", Text: "also update the docs", Steer: true}}, c.Queued,
		"handed to the agent, not read yet: it waits as queued")
	assert.NotContains(t, c.Delivered, "steer-msg", "the pane keeps its card")
	assert.NotContains(t, c.Entries, chat.UserMessage{Text: "also update the docs"})

	f.live().TurnItem(t3client.TurnItem{ID: "tool-row", RunID: new("run-2"), Type: "command_execution", Status: "running", Ordinal: 11})
	assert.Len(t, s.v.latest().Queued, 1, "a tool call says nothing about the message being read")

	f.live().TurnItem(t3client.TurnItem{ID: "think-row", RunID: new("run-2"), Type: "reasoning", Status: "running", Ordinal: 12})
	c = s.v.latest()
	assert.Empty(t, c.Queued, "the agent's next step read it")
	assert.Contains(t, c.Delivered, "steer-msg")
	assert.Contains(t, c.Entries, chat.UserMessage{Text: "also update the docs"})

	f.live().TurnItem(t3client.TurnItem{ID: "pi-row", MessageID: "pi-msg", RunID: new("run-2"), Type: "user_message",
		InputIntent: "steer", Status: "pending", Text: "a provider that says when it reads", Ordinal: 13})
	f.live().TurnItem(t3client.TurnItem{ID: "late-row", RunID: new("run-2"), Type: "assistant_message", Status: "running", Ordinal: 14})
	assert.Len(t, s.v.latest().Queued, 1, "a pending row waits for the provider's word")

	f.live().TurnItem(t3client.TurnItem{ID: "pi-row", MessageID: "pi-msg", RunID: new("run-2"), Type: "user_message",
		InputIntent: "steer", Status: "completed", Text: "a provider that says when it reads", Ordinal: 13})
	assert.Empty(t, s.v.latest().Queued)

	f.live().TurnItem(t3client.TurnItem{ID: "end-row", MessageID: "end-msg", RunID: new("run-2"), Type: "user_message",
		InputIntent: "steer", Status: "completed", Text: "sent as the turn ends", Ordinal: 15})
	require.Len(t, s.v.latest().Queued, 1)
	f.live().Run(t3client.Run{ID: "run-2", Ordinal: 2, Status: "completed"})
	assert.Empty(t, s.v.latest().Queued, "a run that ended holds nothing unread")
}

func TestBackend_RunningSinceIsTheRunsStart(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	assert.True(t, s.v.latest().RunningSince.IsZero(), "a run without a start time gives none")

	s.b.events(s.b.sub).Run(t3client.Run{ID: "run-2", Ordinal: 2, Status: "running",
		StartedAt: "2026-11-21T10:00:00Z", WorkStartedAt: "2026-11-21T10:00:05.5Z"})
	assert.Equal(t, time.Date(2026, 11, 21, 10, 0, 5, 5e8, time.UTC), s.v.latest().RunningSince.UTC(),
		"the agent's start, past the run's preparation")

	s.b.events(s.b.sub).Run(t3client.Run{ID: "run-2", Ordinal: 2, Status: "running", StartedAt: "2026-11-21T10:00:00Z"})
	assert.Equal(t, time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC), s.v.latest().RunningSince.UTC(), "else the run's")

	s.b.events(s.b.sub).Run(t3client.Run{ID: "run-2", Ordinal: 2, Status: "completed", StartedAt: "2026-11-21T10:00:00Z"})
	assert.True(t, s.v.latest().RunningSince.IsZero(), "nothing runs, nothing counts")
}
