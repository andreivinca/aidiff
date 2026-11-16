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
func withModel(instance, slug string) t3client.ThreadProjection {
	snap := fixture()
	snap.Thread.ModelSelection = &t3client.ModelSelection{InstanceID: instance, Model: slug}
	return snap
}

func TestBackend_Commands(t *testing.T) {
	s := subscribe(t, newFakeServer(fixture()))
	assert.Equal(t, []string{"/model <model>", "/effort [level]", "/plan", "/build", "/mode <runtime-mode>", "/clear", "/side <question>",
		"/compact", "/review", "/goal <Objective, or pause, resume, clear>"}, names(s.b.Commands()),
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
	f := newFakeServer(withModel("claude", "claude-sonnet-4"))
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
	assert.Equal(t, "model: claude-sonnet-4 (Claude Code) · /model <name> switches it", reply.Note)
	assert.Empty(t, f.cmds)
}

func TestBackend_EffortCommand(t *testing.T) {
	snap := fixture()
	snap.Thread.ModelSelection = &t3client.ModelSelection{InstanceID: "codex", Model: "gpt-5",
		Options: json.RawMessage(`[{"id":"serviceTier","value":"priority"}]`)}
	f := newFakeServer(snap)
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
	f := newFakeServer(fixture())
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
