package t3chat

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

func names(cmds []chat.Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		label := c.Name
		if c.Args != "" {
			label += " " + c.Args
		}
		out = append(out, label)
	}
	return out
}

func labels(sugs []chat.Suggestion) []string {
	out := make([]string, 0, len(sugs))
	for _, s := range sugs {
		out = append(out, s.Label)
	}
	return out
}

// withModel is the fixture on the model of instance and slug.
// idle is snap with every run of it finished: settings change at once.
func idle(snap t3client.ThreadProjection) t3client.ThreadProjection {
	snap.Runs = slices.Clone(snap.Runs)
	for i := range snap.Runs {
		snap.Runs[i].Status = "completed"
	}
	return snap
}

func withModel(instance, slug string) t3client.ThreadProjection {
	snap := fixture()
	snap.Thread.ModelSelection = &t3client.ModelSelection{InstanceID: instance, Model: slug}
	return snap
}

func TestBackend_Commands(t *testing.T) {
	s := subscribe(t, newFakeServer(fixture()))
	assert.Equal(t, []string{"/model <model>", "/effort [level]", "/plan", "/build", "/mode <runtime-mode>", "/clear",
		"/side <question>", "/compact", "/review", "/goal <Objective, or pause, resume, clear>"}, names(s.b.Commands()),
		"T3's own, then every provider's while the thread's is unknown")
	for _, c := range s.b.Commands()[7:] {
		assert.Nil(t, c.Run, "%s goes to the agent as a message", c.Name)
	}
	assert.Equal(t, "Summarize the conversation and reduce context usage", s.b.Commands()[7].Description)
	assert.Equal(t, "Objective, or pause, resume, clear", s.b.Commands()[9].Description, "a hint describes a command with nothing else")

	s = subscribe(t, newFakeServer(withModel("codex", "gpt-5")))
	assert.Equal(t, []string{"/goal <Objective, or pause, resume, clear>"}, names(s.b.Commands()[7:]), "only the thread's provider's")
}

func TestBackend_ModeCommands(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	_, err := s.run(t, "/plan", "")
	require.NoError(t, err)
	assert.Equal(t, t3client.InteractionModeCommand("th-1", "plan"), nextCommand(t, f))
	_, err = s.run(t, "/build", "")
	require.NoError(t, err)
	assert.Equal(t, "default", nextCommand(t, f)["interactionMode"])

	_, err = s.run(t, "/mode", " Auto ")
	require.NoError(t, err)
	assert.Equal(t, t3client.RuntimeModeCommand("th-1", "auto"), nextCommand(t, f))
	_, err = s.run(t, "/mode", "bogus")
	require.EqualError(t, err, "/mode approval-required | auto-accept-edits | auto | full-access")
	assert.Equal(t, []string{"auto-accept-edits", "auto"}, labels(s.command(t, "/mode").Complete("auto")))

	f.set(errors.New("offline"), nil)
	_, err = s.run(t, "/plan", "")
	require.EqualError(t, err, "plan mode failed: offline")
	nextCommand(t, f)
}

func TestBackend_ModelCommand(t *testing.T) {
	f := newFakeServer(idle(withModel("claude", "claude-sonnet-4")))
	s := subscribe(t, f)
	assert.Equal(t, chat.Settings{Mode: "Build", Model: "Sonnet 4 · medium", Details: []string{"Claude Code"}}, s.v.latest().Settings,
		"the composer names the model, its effort and its provider")

	model := s.command(t, "/model")
	assert.Equal(t, []string{"claude-opus-4", "claude-sonnet-4", "gpt-5"}, labels(model.Complete("")))
	assert.Equal(t, "Sonnet 4 · Claude Code · current", model.Complete("")[1].Description)
	assert.Equal(t, []chat.Suggestion{{Insert: "/model gpt-5", Label: "gpt-5", Description: "GPT-5 · Codex", Runs: true}}, model.Complete("gpt"))
	assert.Equal(t, []string{"no model matches zzz"}, labels(model.Complete("zzz")))

	_, err := s.run(t, "/model", "gpt")
	require.NoError(t, err)
	assert.Equal(t, t3client.ModelSelectionCommand("th-1", t3client.ModelSelection{InstanceID: "codex", Model: "gpt-5"}), nextCommand(t, f))
	_, err = s.run(t, "/model", "opus")
	require.NoError(t, err)
	assert.Equal(t, t3client.ModelSelection{InstanceID: "claude", Model: "claude-opus-4"}, nextCommand(t, f)["modelSelection"],
		"a unique part of a slug or name is enough")
	_, err = s.run(t, "/model", "claude")
	require.EqualError(t, err, "2 models match claude; be more specific")
	_, err = s.run(t, "/model", "nope")
	require.EqualError(t, err, "no model matches nope; /model and tab lists them")
	reply, err := s.run(t, "/model", "")
	require.NoError(t, err)
	assert.Equal(t, "model: claude-opus-4 (Claude Code) · /model <name> switches it", reply.Note,
		"the model chosen here is the current one, before the thread reports it")
	assert.Empty(t, f.cmds)
}

func TestBackend_EffortWaitsItsTurn(t *testing.T) {
	snap := withModel("claude", "claude-sonnet-4") // the agent works on run-2
	snap.Runs = append(snap.Runs, t3client.Run{ID: "run-3", Ordinal: 3, Status: "queued", UserMessageID: "m3"})
	snap.Messages = append(snap.Messages, t3client.Message{ID: "m3", Role: "user", Text: "queued before"})
	f := newFakeServer(snap)
	s := subscribe(t, f)

	reply, err := s.run(t, "/effort", "high")
	require.NoError(t, err)
	assert.Equal(t, "effort: high after the agent's current work; messages sent from now run with it", reply.Note)
	assert.Empty(t, f.cmds, "nothing changes on the thread while the agent works")
	assert.Equal(t, []chat.Queued{{ID: "m3", Text: "queued before"}, {ID: pendingSelectionID, Text: "/effort high"}},
		s.v.latest().Queued, "it waits in the queue after the message queued before it")
	assert.Equal(t, "Sonnet 4 · high", s.v.latest().Settings.Model, "the composer shows what messages from here run with")

	require.NoError(t, s.b.Send(context.Background(), chat.Outgoing{ID: "m4", Text: "after"}))
	sent := nextCommand(t, f)
	high, ok := sent["modelSelection"].(t3client.ModelSelection)
	require.True(t, ok, "a message sent after it carries it: %v", sent["modelSelection"])
	assert.Contains(t, string(high.Options), `"high"`)

	// run-2 ends; the queued message starts with its own selection, and T3 sets it back on the thread
	f.live().Run(t3client.Run{ID: "run-2", Ordinal: 2, Status: "completed"})
	f.live().Run(t3client.Run{ID: "run-3", Ordinal: 3, Status: "running", UserMessageID: "m3"})
	f.live().Thread(t3client.Thread{ID: "th-1", ModelSelection: &t3client.ModelSelection{InstanceID: "claude", Model: "claude-sonnet-4"}})
	assert.Empty(t, f.cmds, "its turn has not come while the message before it runs")
	f.live().Run(t3client.Run{ID: "run-3", Ordinal: 3, Status: "completed", UserMessageID: "m3"})
	assert.Equal(t, "thread.model-selection.set", nextCommand(t, f)["type"], "its turn: the thread takes it")
	assert.Empty(t, s.v.latest().Queued)
}

func TestBackend_PendingEffortTakenBack(t *testing.T) {
	f := newFakeServer(withModel("claude", "claude-sonnet-4"))
	s := subscribe(t, f)
	_, err := s.run(t, "/effort", "high")
	require.NoError(t, err)
	require.NoError(t, s.b.Unqueue(context.Background(), pendingSelectionID))
	assert.Empty(t, s.v.latest().Queued, "↑ takes it back like a queued message")
	assert.Equal(t, "Sonnet 4 · medium", s.v.latest().Settings.Model, "and the earlier effort stands")
	f.live().Run(t3client.Run{ID: "run-2", Ordinal: 2, Status: "completed"})
	assert.Empty(t, f.cmds, "nothing is applied later")
}

func TestBackend_EffortCommand(t *testing.T) {
	snap := fixture()
	snap.Thread.ModelSelection = &t3client.ModelSelection{InstanceID: "codex", Model: "gpt-5",
		Options: json.RawMessage(`[{"id":"serviceTier","value":"priority"}]`)}
	f := newFakeServer(idle(snap))
	s := subscribe(t, f)
	reply, err := s.run(t, "/effort", "")
	require.NoError(t, err)
	assert.Equal(t, "effort: medium · /effort low | medium | high", reply.Note)
	assert.Equal(t, []string{"low", "medium", "high"}, labels(s.command(t, "/effort").Complete("")))
	assert.Contains(t, s.command(t, "/effort").Complete("med")[0].Description, "· current")
	assert.Equal(t, []string{"no supported effort matches x"}, labels(s.command(t, "/effort").Complete("x")))

	_, err = s.run(t, "/effort", "high")
	require.NoError(t, err)
	cmd := nextCommand(t, f)
	assert.Equal(t, "thread.model-selection.set", cmd["type"])
	selection, ok := cmd["modelSelection"].(t3client.ModelSelection)
	require.True(t, ok)
	assert.JSONEq(t, `[{"id":"serviceTier","value":"priority"},{"id":"reasoningEffort","value":"high"}]`, string(selection.Options))
	f.live().Thread(t3client.Thread{ID: "th-1", ModelSelection: &selection})
	assert.Equal(t, "GPT-5 · high", s.v.latest().Settings.Model)

	_, err = s.run(t, "/effort", "invalid")
	require.ErrorContains(t, err, "unsupported effort")
	f.live().Thread(t3client.Thread{ID: "th-1", ModelSelection: &t3client.ModelSelection{InstanceID: "claude", Model: "claude-opus-4"}})
	_, err = s.run(t, "/effort", "high")
	require.EqualError(t, err, "this model does not advertise adjustable effort")
	assert.Empty(t, f.cmds, "invalid levels are not dispatched")

	s = subscribe(t, newFakeServer(fixture()))
	_, err = s.run(t, "/effort", "")
	require.EqualError(t, err, "the thread's model is not available yet")
}

func TestBackend_CatalogIsFetchedOncePerServer(t *testing.T) {
	f := newFakeServer(idle(fixture()))
	f.set(nil, errors.New("offline"))
	chats := newChats(f)
	s := subscribeOn(t, chats, f)
	select {
	case note := <-s.v.notes:
		assert.Equal(t, "server catalog: offline", note)
	default:
		t.Fatal("the failure was not told")
	}
	assert.Equal(t, []string{"the model list could not be loaded: offline"}, labels(s.command(t, "/model").Complete("")),
		"the popup says why there are no models")
	assert.Equal(t, 1, f.fetches(), "completing does not fetch again")

	s = subscribeOn(t, chats, f)
	assert.Equal(t, 1, f.fetches(), "nor does another thread on the same server")
	f.set(nil, nil)
	_, err := s.run(t, "/model", "gpt")
	require.EqualError(t, err, "the model list could not be loaded: offline")
	s.v.next(t) // the catalog, fetched again
	assert.Equal(t, 2, f.fetches(), "a command that needs the catalog fetches it again after a failure")
	_, err = s.run(t, "/model", "gpt")
	require.NoError(t, err)
	assert.Equal(t, "thread.model-selection.set", nextCommand(t, f)["type"])

	other := newFakeServer(fixture())
	subscribe(t, other)
	assert.Equal(t, 1, other.fetches(), "another server's threads fetch its own catalog")
}

func TestBackend_ClearStartsANewThread(t *testing.T) {
	snap := withModel("claude", "claude-opus-4")
	snap.Thread.InteractionMode, snap.Thread.RuntimeMode = "plan", "auto"
	f := newFakeServer(snap)
	chats := newChats(f)
	var got []NewThread
	chats.OnNewThread(func(req NewThread) {
		got = append(got, req)
	})
	s := subscribeOn(t, chats, f)
	assert.Equal(t, "start over in a new thread of this project", s.command(t, "/clear").Description)
	reply, err := s.run(t, "/clear", "")
	require.NoError(t, err)
	assert.Equal(t, "starting a new thread…", reply.Note)
	require.Len(t, got, 1)
	assert.Equal(t, NewThread{From: "th-1", Model: &t3client.ModelSelection{InstanceID: "claude", Model: "claude-opus-4"},
		RuntimeMode: "auto", InteractionMode: "plan"}, got[0], "the new thread takes this one's settings")
	assert.NotContains(t, s.v.latest().Settings.Details, "fresh context", "no fresh session is armed here")
}

func TestBackend_ClearAndSideChat(t *testing.T) {
	f := newFakeServer(withModel("claude", "claude-opus-4"))
	s := subscribe(t, f)
	reply, err := s.run(t, "/clear", "")
	require.NoError(t, err)
	assert.Equal(t, "the next message starts a fresh session; the transcript stays", reply.Note)
	assert.Contains(t, s.v.latest().Settings.Details, "fresh context")
	require.NoError(t, s.b.Send(context.Background(), chat.Outgoing{ID: "m-1", Text: "start over"}))
	assert.Equal(t, true, nextCommand(t, f)["freshContext"], "/clear marks the next message")
	assert.NotContains(t, s.v.latest().Settings.Details, "fresh context")
	require.NoError(t, s.b.Send(context.Background(), chat.Outgoing{ID: "m-2", Text: "again"}))
	assert.Nil(t, nextCommand(t, f)["freshContext"], "once")

	reply, err = s.run(t, "/side", "is this file used anywhere?")
	require.NoError(t, err)
	assert.Equal(t, "side chat started; ctrl+p lists it with the other threads", reply.Note)
	fork := nextCommand(t, f)
	assert.Equal(t, "thread.fork", fork["type"])
	assert.Equal(t, "th-1", fork["sourceThreadId"])
	assert.Equal(t, true, fork["sideChat"])
	question := nextCommand(t, f)
	assert.Equal(t, fork["targetThreadId"], question["threadId"], "the question goes to the side chat")
	assert.Equal(t, "is this file used anywhere?", question["text"])
	assert.Equal(t, t3client.ModelSelection{InstanceID: "claude", Model: "claude-opus-4"}, question["modelSelection"],
		"with the thread's model")
}

func TestPickModel(t *testing.T) {
	models := []t3client.ModelChoice{
		{InstanceID: "a", Slug: "x-1", Name: "One"},
		{InstanceID: "b", Slug: "x-1", Name: "One again"},
		{InstanceID: "a", Slug: "y-2", Name: "Two"},
	}
	m, err := pickModel(models, "b:x-1")
	require.NoError(t, err)
	assert.Equal(t, "b", m.InstanceID, "instance:slug picks a provider")
	m, err = pickModel(models, "X-1")
	require.NoError(t, err)
	assert.Equal(t, "a", m.InstanceID, "an exact slug takes the first provider")
	m, err = pickModel(models, "two")
	require.NoError(t, err)
	assert.Equal(t, "y-2", m.Slug)
	_, err = pickModel(models, "one")
	require.Error(t, err)
}

func TestBackend_CatalogFetchInFlightIsShared(t *testing.T) {
	f := newFakeServer(idle(withModel("claude", "claude-sonnet-4")))
	hold := make(chan struct{})
	f.mu.Lock()
	f.catalogNow = hold
	f.mu.Unlock()
	chats := newChats(f)
	open := func(thread string) *fakeViewer {
		b, _ := chats.Thread(thread).(*Backend)
		v := newFakeViewer()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = b.Subscribe(ctx, v)
		}()
		t.Cleanup(func() {
			cancel()
			<-done
		})
		<-f.subscribed
		v.next(t) // the snapshot, before the catalog
		return v
	}
	first := open("th-1")
	second := open("th-1") // a second pane, or the thread switched to, while the fetch is in flight
	assert.Equal(t, 1, f.fetches(), "the fetch in flight is joined, not started again")
	assert.Equal(t, "claude-sonnet-4", second.latest().Settings.Model, "the catalog is not there yet")
	close(hold)
	for _, v := range []*fakeViewer{first, second} {
		assert.Equal(t, "Sonnet 4 · medium", v.next(t).Settings.Model, "each backend shows the catalog once it came")
	}
	assert.Equal(t, 1, f.fetches())
}
