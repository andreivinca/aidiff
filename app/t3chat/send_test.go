package t3chat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

func TestBackend_SendPersistsImagesFirst(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	png := chat.Image{Name: "clipboard.png", MIME: "image/png", Data: []byte("png")}
	jpg := chat.Image{Name: "shot.jpg", MIME: "image/jpeg", Data: []byte("jpegdata")}
	m := chat.Outgoing{ID: "msg-1", Text: "look [Image #1] and [Image #2]", Images: []chat.Image{png, jpg}}
	require.NoError(t, s.b.Send(context.Background(), m))
	cmd := nextCommand(t, f)
	assert.Equal(t, "message.dispatch", cmd["type"])
	assert.Equal(t, "msg-1", cmd["messageId"], "the pane's id names the message")
	assert.Equal(t, "look [Image #1] and [Image #2]", cmd["text"])
	assert.Len(t, cmd["attachments"], 2, "the message carries what the server stored")
	assert.Nil(t, cmd["titleSeed"], "a thread with messages keeps its name")
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.persisted, 1, "the images are stored before the message goes out")
	assert.Equal(t, persistCall{threadID: "th-1", messageID: "msg-1", images: []t3client.ImageUpload{
		{Name: "clipboard.png", MimeType: "image/png", Data: []byte("png")},
		{Name: "shot.jpg", MimeType: "image/jpeg", Data: []byte("jpegdata")},
	}}, f.persisted[0])
}

func TestBackend_FirstMessageSeedsTheTitle(t *testing.T) {
	snap := fixture()
	snap.VisibleTurnItems = nil // an empty, freshly started thread
	f := newFakeServer(snap)
	s := subscribe(t, f)
	f.set(errors.New("offline"), nil)
	require.Error(t, s.b.Send(context.Background(), chat.Outgoing{ID: "a", Text: "Fix the login page\nIt breaks"}))
	assert.Equal(t, "Fix the login page", nextCommand(t, f)["titleSeed"], "the first line names the thread")
	f.set(nil, nil)
	require.NoError(t, s.b.Send(context.Background(), chat.Outgoing{ID: "b", Text: "[Image #1]", Images: []chat.Image{{Name: "x.png"}}}))
	assert.Equal(t, "Attached image", nextCommand(t, f)["titleSeed"], "a message that failed named nothing; images only say so")
	require.NoError(t, s.b.Send(context.Background(), chat.Outgoing{ID: "c", Text: "and the signup page"}))
	assert.Nil(t, nextCommand(t, f)["titleSeed"], "later messages leave the name alone")
}

func TestBackend_Prompts(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	ctx := context.Background()
	require.NoError(t, s.b.Decide(ctx, "req-1", chat.Decline))
	assert.Equal(t, t3client.RespondCommand("th-1", "req-1", "decline"), nextCommand(t, f))
	require.NoError(t, s.b.Answer(ctx, "req-9", map[string]chat.Answer{
		"schema": {Values: []string{"strict"}},
		"areas":  {Values: []string{"API", "Docs"}, Multiple: true},
	}))
	assert.Equal(t, map[string]any{"schema": "strict", "areas": []string{"API", "Docs"}}, nextCommand(t, f)["answers"],
		"one string per question, a list for a question that takes several")
	require.NoError(t, s.b.Dismiss(ctx, "req-11"))
	assert.Equal(t, t3client.DismissQuestionCommand("th-1", "req-11"), nextCommand(t, f))
	require.NoError(t, s.b.Interrupt(ctx, "run-2"))
	assert.Equal(t, t3client.ThreadStopCommand("th-1"), nextCommand(t, f), "a stop stops the whole thread, subagents included")

	require.NoError(t, s.b.CheckImage("small.png", 1024))
	require.EqualError(t, s.b.CheckImage("clipboard.png", t3client.MaxImageBytes+1), "clipboard.png is 10.0 MB; T3 takes images up to 10 MB")
}

func TestBackend_InterruptFallsBackToTheRun(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	f.mu.Lock()
	f.refuse = "thread.stop"
	f.mu.Unlock()
	require.NoError(t, s.b.Interrupt(context.Background(), "run-2"), "the run's interrupt went through")
	assert.Equal(t, "thread.stop", nextCommand(t, f)["type"])
	assert.Equal(t, t3client.Command{"type": "run.interrupt", "threadId": "th-1", "runId": "run-2"}, nextCommand(t, f),
		"a server without thread.stop interrupts the run, and its queue goes on")

	f.mu.Lock()
	f.refuse = ""
	f.mu.Unlock()
	f.set(errors.New("connection lost"), nil)
	require.EqualError(t, s.b.Interrupt(context.Background(), "run-2"), "connection lost", "the stop's own failure is reported")
}

func TestBackend_InterruptHoldsTheQueue(t *testing.T) {
	snap := fixture()
	snap.Runs = append(snap.Runs, t3client.Run{ID: "run-3", Ordinal: 3, Status: "queued", UserMessageID: "m3"})
	f := newFakeServer(snap)
	s := subscribe(t, f)
	require.NoError(t, s.b.Interrupt(context.Background(), "run-2"))
	assert.Equal(t, t3client.ThreadStopCommand("th-1"), nextCommand(t, f))
	select {
	case cmd := <-f.cmds:
		t.Fatalf("the queue waits for /resume; got %v", cmd)
	default:
	}
	reply, err := s.run(t, "/resume", "")
	require.NoError(t, err)
	assert.Equal(t, "the queued messages go on", reply.Note)
	assert.Equal(t, t3client.QueueResumeCommand("th-1"), nextCommand(t, f))
}

func TestBackend_Settings(t *testing.T) {
	snap := fixture()
	snap.Thread.InteractionMode, snap.Thread.RuntimeMode = "plan", "approval-required"
	snap.Thread.ModelSelection = &t3client.ModelSelection{InstanceID: "claude", Model: "claude-opus-4"}
	f := newFakeServer(snap)
	s := subscribe(t, f)
	assert.Equal(t, chat.Settings{Mode: "Plan", Attention: true, Model: "Opus 4", Details: []string{"Claude Code", "approval required"}},
		s.v.latest().Settings)

	f.live().Thread(t3client.Thread{ID: "th-1", InteractionMode: "default", RuntimeMode: "full-access",
		ModelSelection: &t3client.ModelSelection{InstanceID: "local", Model: "llama"}})
	assert.Equal(t, chat.Settings{Mode: "Build", Model: "llama", Details: []string{"local", "full access"}}, s.v.latest().Settings,
		"a model the catalog does not know shows as the thread names it")
	f.live().Thread(t3client.Thread{ID: "th-1", Title: "A new title"})
	assert.Equal(t, "A new title", s.v.latest().Title)
	assert.Equal(t, "llama", s.v.latest().Settings.Model, "partial metadata keeps the settings")

	s = subscribe(t, newFakeServer(fixture()))
	assert.Equal(t, chat.Settings{Mode: "Build", Model: "T3 Code"}, s.v.latest().Settings, "a thread without a model yet")
}

func TestChats_WithoutAConnection(t *testing.T) {
	assert.Nil(t, NewChats(nil).Thread("th-1"), "no connection, no backend")
	assert.NotNil(t, NewChats(&t3client.Conn{}).Thread("th-1"))
}

func TestBackend_Unqueue(t *testing.T) {
	snap := fixture()
	snap.Runs = append(snap.Runs, t3client.Run{ID: "run-3", Ordinal: 3, Status: "queued", UserMessageID: "m3"})
	snap.Messages = append(snap.Messages, t3client.Message{ID: "m3", Role: "user", Text: "queued", Attachments: []json.RawMessage{json.RawMessage(`{"id":"a"}`)}})
	f := newFakeServer(snap)
	s := subscribe(t, f)
	assert.Equal(t, []chat.Queued{{ID: "m3", Text: "queued", Images: 1}}, s.v.latest().Queued, "the pane learns its images")

	require.NoError(t, s.b.Unqueue(context.Background(), "m3"))
	assert.Equal(t, t3client.QueuedRunCancelCommand("th-1", "run-3"), nextCommand(t, f))

	run3 := "run-3"
	f.live().TurnItem(t3client.TurnItem{ID: "i9", Type: "user_message", Status: "pending", InputIntent: "queued_turn",
		MessageID: "m3", RunID: &run3, Text: "queued"})
	f.live().Run(t3client.Run{ID: "run-3", Ordinal: 3, Status: "cancelled", UserMessageID: "m3"})
	c := s.v.latest()
	assert.Empty(t, c.Queued, "taken back, it leaves the queue")
	assert.NotContains(t, c.Entries, chat.UserMessage{Text: "queued"}, "and never shows as sent")
	assert.Equal(t, chat.AgentRunning, c.State, "nor as the agent's state: run-2 still runs")
	f.live().Run(t3client.Run{ID: "run-2", Ordinal: 2, Status: "interrupted"})
	assert.Equal(t, chat.AgentState("interrupted"), s.v.latest().State, "the last run that ran tells it")
	require.EqualError(t, s.b.Unqueue(context.Background(), "m3"), "it is no longer queued")
}
