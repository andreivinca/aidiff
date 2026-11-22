package t3chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// paneOn opens a chat pane on the thread th-1 of f, whose catalog is
// already known, and takes the snapshot.
func paneOn(t *testing.T, f *fakeServer) *chat.Pane {
	t.Helper()
	chats := newChats(f)
	cat, err := f.FetchCatalog(context.Background())
	require.NoError(t, err)
	chats.catalog.fetched(cat, nil)
	p := chat.New(chats.Thread("th-1"), "", 90, 30, chat.Config{NoColor: true})
	t.Cleanup(p.Close)
	select {
	case <-f.subscribed:
	case <-time.After(waitLimit):
		t.Fatal("never subscribed")
	}
	drain(p)
	return p
}

// drain takes the redraw a pane asked for, so the next update a test waits
// on is the next change.
func drain(p *chat.Pane) {
	select {
	case <-p.Updates():
	default:
	}
}

// send types into the pane and returns what reached the server once the
// pane settled the request.
func send(t *testing.T, p *chat.Pane, f *fakeServer, typed string) t3client.Command {
	t.Helper()
	p.SendText(typed + "\r")
	cmd := nextCommand(t, f)
	settle(t, p)
	return cmd
}

// settle waits until the pane no longer waits on a request. Any redraw
// will not do: one left over from an earlier change, such as a command
// showing the conversation, comes before the request settles, and a send
// typed then is turned down as busy. The hint row says "sending…" until
// the request settled, and settling wakes the pane, so this needs no
// timing.
func settle(t *testing.T, p *chat.Pane) {
	t.Helper()
	deadline := time.After(waitLimit)
	for strings.Contains(view(p), "sending…") {
		select {
		case <-p.Updates():
		case <-deadline:
			t.Fatal("the pane never settled the request")
		}
	}
}

func view(p *chat.Pane) string {
	return ansi.Strip(p.Render(true))
}

func TestPane_OnAThread(t *testing.T) {
	f := newFakeServer(fixture())
	p := paneOn(t, f)
	v := view(p)
	assert.Contains(t, v, "Fix login · running")
	assert.Contains(t, v, "┃  please fix the login page")
	assert.Contains(t, v, "+ Ran 1 command and changed 1 file")
	assert.Contains(t, v, "Looking at the handler▍")
	assert.Contains(t, v, "│   Build  T3 Code")

	cmd := send(t, p, f, "please also add tests")
	assert.Equal(t, "message.dispatch", cmd["type"])
	assert.Equal(t, "please also add tests", cmd["text"])
	id, ok := cmd["messageId"].(string)
	require.True(t, ok)
	assert.Contains(t, view(p), "Queued", "the message waits for the running turn")
	f.live().TurnItem(t3client.TurnItem{ID: "u2", MessageID: id, Type: "user_message", Status: "completed", Text: "please also add tests"})
	drain(p)
	assert.NotContains(t, view(p), "Queued", "the transcript took it over")
	assert.Equal(t, 1, strings.Count(view(p), "please also add tests"))

	f.live().TurnItem(t3client.TurnItem{ID: "a", Type: "approval_request", Status: "waiting", RequestID: "req-1", Prompt: "Run rm -rf build?"})
	f.live().Request(t3client.RuntimeRequest{ID: "req-1", Status: "pending"})
	drain(p)
	assert.Contains(t, view(p), "⚠ approval needed: Run rm -rf build?")
	assert.Contains(t, view(p), "waiting for you")
	assert.Equal(t, t3client.RespondCommand("th-1", "req-1", "accept"), send(t, p, f, "/accept"))

	f.live().TurnItem(t3client.TurnItem{ID: "q", Type: "user_input_request", Status: "waiting", RequestID: "req-9",
		Questions: []t3client.Question{{ID: "areas", Question: "Which areas?", MultiSelect: true,
			Options: []t3client.QuestionOption{{Label: "API"}, {Label: "UI"}, {Label: "Docs"}}}}})
	drain(p)
	assert.Equal(t, map[string]any{"areas": []string{"API", "Docs"}}, send(t, p, f, "/answer 1,3")["answers"])

	p.SendText("/model gpt\r")
	assert.Contains(t, view(p), "/model gpt-5", "while the agent works the model change waits its turn in the queue")
	compact := send(t, p, f, "/compact")
	assert.Equal(t, "/compact", compact["text"], "the provider's command goes to the agent")
	assert.Equal(t, t3client.ModelSelection{InstanceID: "codex", Model: "gpt-5"}, compact["modelSelection"], "with the model chosen")
	assert.Equal(t, "plan", send(t, p, f, "/plan")["interactionMode"])
	p.SendText("/stop\r")
	assert.Contains(t, view(p), "Stop agent?", "the running turn can be stopped")
}

func TestPane_OnAThreadAfterClear(t *testing.T) {
	f := newFakeServer(fixture())
	p := paneOn(t, f)
	p.SendText("/clear\r")
	assert.Contains(t, view(p), "fresh context", "the backend's settings follow its command at once")
	assert.Contains(t, view(p), "the next message starts a fresh session")
	assert.Equal(t, true, send(t, p, f, "start over")["freshContext"])
	assert.NotContains(t, view(p), "fresh context")
}
