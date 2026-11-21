package t3chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// discardViewer takes what a backend shows and keeps none of it.
type discardViewer struct{}

func (discardViewer) Show(chat.Conversation) {}
func (discardViewer) Note(string)            {}

// longThread is a thread of n items, as a long session has them: the
// user's messages and steers, commands with their output, thoughts and
// replies, a quarter of them each.
func longThread(n int) t3client.ThreadProjection {
	run := "run-1"
	pr := t3client.ThreadProjection{Thread: t3client.Thread{ID: "th-1", Title: "Long"},
		Runs: []t3client.Run{{ID: run, Ordinal: 1, Status: "running"}}}
	output, _ := json.Marshal(strings.Repeat("ok  \tgithub.com/x/y\t0.123s\n", 40))
	for i := range n {
		it := t3client.TurnItem{ID: fmt.Sprintf("i%d", i), Ordinal: i + 1, RunID: &run, Status: "completed"}
		switch i % 4 {
		case 0:
			it.Type, it.MessageID, it.Text = "user_message", fmt.Sprintf("m%d", i), "please look at this"
			if i%8 == 0 {
				it.InputIntent = "steer"
			}
		case 1:
			it.Type, it.Input, it.Output = "command_execution", json.RawMessage(`"go test ./..."`), output
		case 2:
			it.Type, it.Text = "reasoning", "thinking about it"
		default:
			it.Type, it.Text = "assistant_message", "here is what I found"
		}
		pr.TurnItems = append(pr.TurnItems, it)
		pr.VisibleTurnItems = append(pr.VisibleTurnItems, t3client.ProjectedTurnItem{Position: i, Item: it})
	}
	return pr
}

// BenchmarkBackend_StreamedToken is one token of a streaming reply on a
// thread of 1000 and 3000 items: the event, and the conversation built and
// shown after it, on the connection's reader with the backend's lock held.
func BenchmarkBackend_StreamedToken(b *testing.B) {
	for _, n := range []int{1000, 3000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			f := newFakeServer(longThread(n))
			chats := newChats(f)
			chats.catalog.fetched(t3client.Catalog{}, nil)
			be, _ := chats.Thread("th-1").(*Backend)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { _ = be.Subscribe(ctx, discardViewer{}) }()
			<-f.subscribed
			ev := be.events(be.sub)
			run := "run-1"
			reply := t3client.TurnItem{ID: "streaming", Ordinal: n + 1, RunID: &run, Type: "assistant_message", Status: "running", Streaming: true}
			b.ResetTimer()
			for i := range b.N {
				reply.Text = strings.Repeat("x", i%200)
				ev.TurnItem(reply)
			}
		})
	}
}

func TestBackend_DecodedTextsFollowUpdates(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	ev := s.b.events(s.b.sub)
	cmd := t3client.TurnItem{ID: "c1", Ordinal: 9, Type: "command_execution", Status: "running",
		Input: json.RawMessage(`"make test"`), Output: json.RawMessage(`"compiling"`)}
	ev.TurnItem(cmd)
	details := func() []string {
		for _, a := range s.v.latest().Activity {
			if a.ID == "item:c1" {
				return a.Details
			}
		}
		return nil
	}
	assert.Equal(t, []string{"make test", "compiling"}, details())
	ev.TurnItem(t3client.TurnItem{ID: "x", Type: "assistant_message", Status: "running", Text: "token"})
	assert.Equal(t, []string{"make test", "compiling"}, details(), "an item left alone keeps its texts")
	cmd.Status, cmd.Output = "completed", json.RawMessage(`"ok\nPASS"`)
	ev.TurnItem(cmd)
	assert.Equal(t, []string{"make test", "ok\nPASS"}, details(), "an update decodes its fields again")
}

func TestBackend_CommandsDoNotWaitOnTheThread(t *testing.T) {
	f := newFakeServer(withModel("codex", "gpt-5"))
	s := subscribe(t, f)
	s.b.mu.Lock() // the connection's reader building a conversation
	defer s.b.mu.Unlock()
	done := make(chan []chat.Command)
	go func() { done <- s.b.Commands() }()
	select {
	case cmds := <-done:
		assert.Equal(t, "/goal", cmds[len(cmds)-1].Name, "the thread's provider's commands, as last shown")
		assert.Len(t, cmds, 8)
	case <-time.After(waitLimit):
		t.Fatal("Commands waited on the backend's lock")
	}
}
