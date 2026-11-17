package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func labels(sugs []Suggestion) []string {
	out := make([]string, 0, len(sugs))
	for _, s := range sugs {
		out = append(out, s.Label)
	}
	return out
}

// backendCommands are a backend's own commands, which record what they
// were asked in ran, and two of the agent's.
func backendCommands(ran *[]string) []Command {
	record := func(arg string) (Reply, error) {
		*ran = append(*ran, arg)
		return Reply{}, nil
	}
	return []Command{
		{Name: "/model", Args: "<model>", Description: "switch the model", Run: record, Complete: modelChoices},
		{Name: "/mode", Args: "<mode>", Description: "what the agent may do on its own", Run: record},
		{Name: "/plan", Description: "plan mode", Run: record},
		{Name: "/help", Description: "the backend's own help", Run: record},
		{Name: "/compact", Description: "Summarize the conversation and reduce context usage"},
		{Name: "/goal", Args: "<objective>", Description: "set a goal"},
		{Name: "/yes", Description: "an agent command named like an alias of the pane's"},
	}
}

func modelChoices(prefix string) []Suggestion {
	var out []Suggestion
	for _, m := range []string{"opus-long", "opus", "sonnet"} {
		if strings.HasPrefix(m, prefix) {
			out = append(out, Suggestion{Insert: "/model " + m, Label: m, Runs: true})
		}
	}
	return out
}

func TestCommandSuggestions(t *testing.T) {
	p, f := newTestPane(t, 90, 20, fixture())
	var ran []string
	f.setCommands(backendCommands(&ran)...)
	locked(p, func() {
		all := labels(p.commandSuggestionsLocked("/"))
		assert.Len(t, all, len(p.builtins)+5, "a bare slash lists the pane's commands, the backend's and the agent's")
		assert.Equal(t, "/help", all[0])
		assert.Equal(t, 1, strings.Count(strings.Join(all, " "), "/help"), "the pane's own command wins over a backend's")
		assert.NotContains(t, all, "/yes", "an agent's command named like the pane's alias is left out")
		assert.Equal(t, []string{"/compact", "/goal <objective>"}, all[len(all)-2:], "the agent's commands come last")
		assert.Equal(t, []string{"/model <model>", "/mode <mode>"}, labels(p.commandSuggestionsLocked("/mo")))
		assert.Equal(t, []string{"/accept"}, labels(p.commandSuggestionsLocked("/ye")), "aliases match too")
		assert.Equal(t, []string{"/dismiss"}, labels(p.commandSuggestionsLocked("/miss")), "so does a part of the name")
		assert.Equal(t, []string{"/compact"}, labels(p.commandSuggestionsLocked("/summar")), "and of a description")
		assert.Empty(t, p.commandSuggestionsLocked("/zzz"))
		_, found := p.findCommandLocked("/compact")
		assert.False(t, found, "the agent's commands are not run by the pane")
		yes, _ := p.findCommandLocked("/y")
		assert.Equal(t, "/accept", yes.Name)
	})
}

func TestPane_PopupCompletesAndRuns(t *testing.T) {
	p, f := newTestPane(t, 90, 20, fixture())
	var ran []string
	f.setCommands(backendCommands(&ran)...)
	p.SendText("/")
	v := view(p)
	assert.Contains(t, v, "/help")
	assert.Contains(t, v, "how the commands work")
	assert.NotContains(t, v, "/model <model>", "the popup shows a window of rows")
	for range 10 {
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
	c := ask(t, p, f, func() {
		p.SendText("\r")
	})
	assert.Equal(t, call{kind: "interrupt", id: "run-2"}, c, "enter on a complete command runs it")
	assert.Empty(t, p.input.text())

	p.SendText("/mo")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	p.SendText("\r")
	assert.Equal(t, "/mode ", p.input.text(), "enter on a command with an argument only completes it")
	p.SendText("\x15/model ")
	assert.Equal(t, []string{"opus-long", "opus", "sonnet"}, labels(p.suggestionsLocked()))
	p.SendText("opus")
	assert.Equal(t, []string{"opus", "opus-long"}, labels(p.suggestionsLocked()), "the exact choice comes first")
	p.SendText("-")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	p.SendText("\r")
	assert.Equal(t, []string{"opus-long"}, ran, "the selected argument is filled in and run")
	assert.Empty(t, p.input.text())

	p.SendText("/comp")
	assert.Equal(t, []string{"/compact"}, labels(p.suggestionsLocked()))
	c = ask(t, p, f, func() {
		p.SendText("\r")
	})
	assert.Equal(t, "/compact", c.msg.Text, "an agent's command is completed and sent as a message")

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
	assert.Equal(t, []string{"opus-long", ""}, ran, "a backend's command without an argument runs on enter")
}

func TestPane_SlashCommands(t *testing.T) {
	p, f := newTestPane(t, 80, 30, fixture())
	p.SendText("/help\r")
	assert.Contains(t, view(p), "type / to see every command")

	p.SendText("/stop\r")
	assert.Contains(t, view(p), "Stop agent?")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	c := ask(t, p, f, func() {
		p.SendText("\r")
	})
	assert.Equal(t, call{kind: "interrupt", id: "run-2"}, c, "the newest running turn")

	p.SendText("/accept\r")
	assert.Contains(t, view(p), "nothing is waiting for an approval")
	assert.Equal(t, "/accept", p.input.text(), "a command turned down stays in the input")

	conv := fixture()
	conv.State = AgentWaiting
	conv.Entries = append(conv.Entries,
		Approval{ID: "old", Prompt: "Run ls?"},
		Approval{ID: "req-1", Prompt: "Run rm -rf build?", Reason: "cleans the build", Pending: true})
	show(t, p, f, conv)
	v := view(p)
	assert.Contains(t, v, "approval: Run ls?", "a settled approval collapses")
	assert.Contains(t, v, "⚠ approval needed: Run rm -rf build?")
	assert.Contains(t, v, "cleans the build")
	assert.Contains(t, v, "type /accept or /decline")
	assert.Contains(t, v, "waiting for you")

	c = ask(t, p, f, func() {
		p.SendText("\r")
	})
	assert.Equal(t, call{kind: "decide", id: "req-1", decision: Accept}, c)
	assert.Empty(t, p.input.text(), "an accepted command leaves the input")
	for typed, d := range map[string]Decision{"/n": Decline, "/cancel": Cancel} {
		c = ask(t, p, f, func() {
			p.SendText(typed + "\r")
		})
		assert.Equal(t, call{kind: "decide", id: "req-1", decision: d}, c)
	}
	f.set(errBoom, nil)
	ask(t, p, f, func() {
		p.SendText("/yes\r")
	})
	assert.Contains(t, view(p), "answer failed: boom")
	f.set(nil, nil)

	c = ask(t, p, f, func() {
		p.SendText("/compact now\r")
	})
	assert.Equal(t, "/compact now", c.msg.Text, "a command nobody knows goes to the agent")
}

func TestPane_BackendCommands(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	sent := make(chan string, 1)
	f.setCommands(
		Command{Name: "/clear", Description: "start fresh", Run: func(string) (Reply, error) {
			return Reply{Note: "the next message starts fresh"}, nil
		}},
		Command{Name: "/side", Args: "<question>", Description: "ask aside", Run: func(arg string) (Reply, error) {
			if arg == "" {
				return Reply{}, errors.New("/side <question>")
			}
			return Reply{Note: "side chat started", Send: func(context.Context) error {
				sent <- arg
				return nil
			}}, nil
		}},
		Command{Name: "/broken", Description: "fails", Run: func(string) (Reply, error) {
			return Reply{Note: "never shown", Send: func(context.Context) error {
				sent <- "broken"
				return errors.New("broken failed: offline")
			}}, nil
		}},
	)
	p.SendText("/clear\r")
	assert.Contains(t, view(p), "the next message starts fresh", "a note is shown at once")
	p.SendText("/side\r")
	assert.Contains(t, view(p), "/side <question>")
	assert.Equal(t, "/side", p.input.text(), "an error turns the input down")

	p.SendText(" is this used?\r")
	assert.Equal(t, "is this used?", <-sent)
	waitUpdate(t, p)
	assert.Contains(t, view(p), "side chat started", "the note follows a request that succeeded")
	assert.Empty(t, p.input.text())

	p.SendText("/broken\r")
	assert.Equal(t, "broken", <-sent)
	waitUpdate(t, p)
	assert.Contains(t, view(p), "broken failed: offline")
	assert.NotContains(t, view(p), "never shown")
}

func TestPane_CommandsWhileBusy(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	var ran []string
	f.setCommands(Command{Name: "/plan", Description: "plan mode", Run: func(arg string) (Reply, error) {
		ran = append(ran, arg)
		return Reply{Send: func(context.Context) error {
			return nil
		}}, nil
	}})
	release := make(chan struct{})
	f.set(nil, release)
	p.SendText("first\r")
	nextCall(t, f)
	p.SendText("second\r")
	assert.Contains(t, view(p), "still being sent", "a second message is turned down, not dropped")
	assert.Equal(t, "second", p.input.text())
	p.SendText("\x15/help\r")
	assert.Contains(t, view(p), "type / to see every command", "commands that send nothing run while busy")
	p.SendText("/plan\r")
	assert.Contains(t, view(p), "still being sent")
	assert.Equal(t, "/plan", p.input.text())
	close(release)
	waitUpdate(t, p)
	p.SendText("\r")
	waitUpdate(t, p)
	assert.Empty(t, p.input.text(), "once the first is through, the command goes")
	require.Len(t, ran, 2)
}

func TestCommandSuggestions_OptionalArgTakesTheSelectedChoice(t *testing.T) {
	p, f := newTestPane(t, 80, 30, fixture())
	var ran []string
	f.setCommands(Command{Name: "/effort", Args: "[level]", OptionalArg: true, Description: "show or change effort",
		Run: func(arg string) (Reply, error) {
			ran = append(ran, arg)
			return Reply{}, nil
		},
		Complete: func(prefix string) []Suggestion {
			var out []Suggestion
			for _, l := range []string{"low", "medium", "high"} {
				if strings.HasPrefix(l, prefix) {
					out = append(out, Suggestion{Insert: "/effort " + l, Label: l, Runs: true})
				}
			}
			return out
		}})

	p.SendText("/effort ")
	require.Equal(t, []string{"low", "medium", "high"}, labels(p.suggestionsLocked()))
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	p.SendText("\r")
	assert.Equal(t, []string{"high"}, ran, "enter on the levels runs the one selected, not the bare command")

	p.SendText("/effort\r")
	assert.Equal(t, []string{"high", ""}, ran, "typed without a space, the bare command still runs")
}
