package t3chat

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/t3client"
)

func labels(sugs []suggestion) []string {
	out := make([]string, 0, len(sugs))
	for _, s := range sugs {
		out = append(out, s.label)
	}
	return out
}

// withCatalog opens a pane and waits for the server's catalog.
func withCatalog(t *testing.T) (*Pane, *fakeServer) {
	t.Helper()
	p, f := newTestPane(t, 90, 20, fixture())
	p.mu.Lock()
	p.ensureCatalogLocked()
	p.mu.Unlock()
	waitFor(t, p, func() bool { return p.catalog != nil })
	return p, f
}

func TestCommandSuggestions(t *testing.T) {
	p, _ := withCatalog(t)
	p.mu.Lock()
	defer p.mu.Unlock()
	all := labels(p.commandSuggestionsLocked("/"))
	assert.Len(t, all, len(commands)+3, "a bare slash lists the pane's commands and the providers'")
	assert.Equal(t, "/help", all[0])
	assert.Contains(t, all, "/compact")
	assert.Contains(t, all, "/goal <Objective, or pause, resume, clear>")
	assert.Equal(t, []string{"/model <model>", "/mode <runtime-mode>"}, labels(p.commandSuggestionsLocked("/mo")))
	assert.Equal(t, []string{"/accept"}, labels(p.commandSuggestionsLocked("/ye")), "aliases match too")
	assert.Equal(t, []string{"/dismiss"}, labels(p.commandSuggestionsLocked("/miss")), "so does a part of the name")
	assert.Equal(t, []string{"/compact"}, labels(p.commandSuggestionsLocked("/summar")), "and of a description")
	assert.Empty(t, p.commandSuggestionsLocked("/zzz"))
	assert.Equal(t, findCommand("/accept"), findCommand("/y"))
	assert.Nil(t, findCommand("/compact"), "provider commands are not the pane's")

	p.current = &t3client.ModelSelection{InstanceID: "codex", Model: "gpt-5"}
	names := labels(p.commandSuggestionsLocked("/"))
	assert.Contains(t, names, "/goal <Objective, or pause, resume, clear>")
	assert.NotContains(t, names, "/compact", "only the thread's provider's commands")
}

func TestPane_PopupCompletesAndRuns(t *testing.T) {
	p, f := withCatalog(t)
	p.SendText("/")
	v := view(p)
	assert.Contains(t, v, "/help")
	assert.Contains(t, v, "how the commands work")
	assert.Contains(t, labels(p.suggestionsLocked()), "/model <model>")
	assert.NotContains(t, v, "/model <model>", "the popup shows a window of rows")
	for range 7 {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	}
	assert.Contains(t, view(p), "/model <model>", "moving down scrolls the window")

	p.SendText("st")
	assert.Equal(t, []string{"/stop"}, labels(p.suggestionsLocked()))
	p.SendText("\t")
	assert.Equal(t, "/stop", p.input.text(), "tab completes the command")
	p.SendText("\r")
	assert.Contains(t, view(p), "Stop agent?", "the stop command asks before interrupting")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendText("\r")
	assert.Equal(t, "run.interrupt", nextCommand(t, f)["type"], "enter on a complete command runs it")
	waitUpdate(t, p)
	assert.Empty(t, p.input.text())

	p.SendText("/mo")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	p.SendText("\r")
	assert.Equal(t, "/mode ", p.input.text(), "enter on a command with an argument only completes it")
	assert.Equal(t, []string{"approval-required", "auto-accept-edits", "auto", "full-access"}, labels(p.suggestionsLocked()))
	p.SendText("au")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	assert.Equal(t, 1, p.sug, "up from the top wraps to the bottom")
	p.SendText("\r")
	cmd := nextCommand(t, f)
	assert.Equal(t, "thread.runtime-mode.set", cmd["type"])
	assert.Equal(t, "auto", cmd["runtimeMode"], "the selected argument is filled in and run")
	waitUpdate(t, p)

	p.SendText("/comp")
	assert.Equal(t, []string{"/compact"}, labels(p.suggestionsLocked()))
	p.SendText("\r")
	cmd = nextCommand(t, f)
	assert.Equal(t, "message.dispatch", cmd["type"])
	assert.Equal(t, "/compact", cmd["text"], "a provider command is completed and sent as a message")
	waitUpdate(t, p)

	p.SendText("/he")
	assert.NotEmpty(t, p.suggestionsLocked())
	p.SendText("\x1b")
	assert.Empty(t, p.suggestionsLocked(), "esc closes the popup")
	assert.NotContains(t, view(p), "how the commands work")
	p.SendText("l")
	assert.NotEmpty(t, p.suggestionsLocked(), "typing brings it back")
	p.SendText("\x15")
	assert.Empty(t, p.suggestionsLocked())

	p.SendText("hello /not a command")
	assert.Empty(t, p.suggestionsLocked(), "only a leading slash opens it")
	p.SendText("\x15/plan\r")
	assert.Equal(t, "plan", nextCommand(t, f)["interactionMode"])
}

func TestPane_ModelCommand(t *testing.T) {
	p, f := newTestPane(t, 90, 20, fixture())
	f.live().Thread(t3client.Thread{ID: "th-1", ModelSelection: &t3client.ModelSelection{InstanceID: "claude", Model: "claude-sonnet-4"}})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "Build  Sonnet 4 Claude Code", "the composer names the model and provider")

	p.SendText("/model ")
	waitFor(t, p, func() bool { return p.catalog != nil })
	assert.Equal(t, []string{"claude-opus-4", "claude-sonnet-4", "gpt-5"}, labels(p.suggestionsLocked()))
	assert.Contains(t, view(p), "Sonnet 4 · Claude Code · current")

	p.SendText("gpt")
	assert.Equal(t, []string{"gpt-5"}, labels(p.suggestionsLocked()))
	p.SendText("\r")
	cmd := nextCommand(t, f)
	assert.Equal(t, "thread.model-selection.set", cmd["type"])
	assert.Equal(t, t3client.ModelSelection{InstanceID: "codex", Model: "gpt-5"}, cmd["modelSelection"])
	waitUpdate(t, p)

	p.SendText("/model opus\r")
	cmd = nextCommand(t, f)
	assert.Equal(t, t3client.ModelSelection{InstanceID: "claude", Model: "claude-opus-4"}, cmd["modelSelection"],
		"a unique part of a slug or name is enough")
	waitUpdate(t, p)

	p.SendText("/model claude")
	assert.Equal(t, []string{"claude-opus-4", "claude-sonnet-4"}, labels(p.suggestionsLocked()))
	p.SendText("\x1b")
	p.SendText("\r")
	assert.Contains(t, view(p), "2 models match claude", "with the popup closed the typed text is taken as is")
	p.SendText("/model\r")
	assert.Contains(t, view(p), "model: claude-sonnet-4 (Claude Code)")
	assert.Contains(t, strings.ToLower(view(p)), "switches it")
	p.SendText("/model nope\r")
	assert.Contains(t, view(p), "no model matches nope")
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
