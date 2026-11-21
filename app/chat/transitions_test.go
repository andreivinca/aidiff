package chat

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPane_ANewPromptStartsOnItsFirstOption(t *testing.T) {
	four := Question{ID: "size", Text: "Which size?", Options: []Option{{Label: "XS", Value: "xs"}, {Label: "S", Value: "s"},
		{Label: "M", Value: "m"}, {Label: "L", Value: "l"}}}
	p, f := newTestPane(t, 80, 30, asking("p1", four))
	for range 3 {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	}
	assert.Contains(t, view(p), "› 4) L")
	c := ask(t, p, f, func() { p.SendText("\r") })
	assert.Equal(t, []string{"l"}, c.answers["size"].Values)

	two := Question{ID: "color", Text: "Which color?", Options: []Option{{Label: "Red", Value: "red"}, {Label: "Blue", Value: "blue"}}}
	show(t, p, f, asking("p2", two))
	assert.Contains(t, view(p), "› 1) Red", "the next prompt's picker starts on its first option, not on a row it lacks")
	c = ask(t, p, f, func() { p.SendText("\r") })
	assert.Equal(t, []string{"red"}, c.answers["color"].Values, "and enter picks it")

	for range 2 {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	}
	follow(t, p, newFakeBackend(asking("p3", two)), "Other")
	assert.Contains(t, view(p), "› 1) Red", "another backend starts over too")
}

func TestPane_SecretRequestEndingMidPassword(t *testing.T) {
	p, f := newTestPane(t, 80, 30, askingSecret())
	p.SendText("hunt")
	ended := askingSecret()
	ended.Secret = nil
	ended.Entries[len(ended.Entries)-1] = SecretRequest{ID: "s1", Label: "sudo password", Status: "cancelled"}
	show(t, p, f, ended)
	locked(p, func() {
		assert.Empty(t, p.secret.runes, "what was typed is wiped as the request ends")
	})
	assert.Contains(t, view(p), "the secret request ended")

	p.SendText("er2")
	p.Paste("pasted")
	assert.True(t, p.SendModifiedKey(uv.KeyPressEvent{Code: uv.KeyEnter, Mod: uv.ModShift}))
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	assert.Empty(t, p.input.text(), "the rest of the password never lands in the visible draft")
	assert.NotContains(t, view(p), "er2")
	p.SendText("\r")
	assert.Empty(t, f.calls, "and its enter sends nothing to the agent")
	assert.Empty(t, p.input.text())
	p.SendText("hello")
	assert.Equal(t, "hello", p.input.text(), "after that enter the composer takes messages again")

	p.SendText("\x15")
	show(t, p, f, askingSecret())
	p.SendText("abc")
	follow(t, p, newFakeBackend(askingSecret()), "Other")
	locked(p, func() {
		assert.Empty(t, p.secret.runes, "following another backend wipes it too")
	})
	p.SendText("x")
	locked(p, func() {
		assert.Equal(t, []rune("x"), p.secret.runes)
	})
}

func TestPane_SecretSentEndsWithoutSwallowingKeys(t *testing.T) {
	p, f := newTestPane(t, 80, 30, askingSecret())
	p.SendText("hunter2")
	ask(t, p, f, func() { p.SendText("\r") })
	ended := askingSecret()
	ended.Secret = nil
	show(t, p, f, ended)
	p.SendText("thanks")
	assert.Equal(t, "thanks", p.input.text(), "a request the user answered ends quietly")
}

func TestPane_ScrolledUpStaysOnItsText(t *testing.T) {
	conv := Conversation{State: AgentRunning}
	for i := range 40 {
		conv.Entries = append(conv.Entries, AgentMessage{Markdown: fmt.Sprintf("line %d", i)})
	}
	p, f := newTestPane(t, 40, 16, conv)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyPgUp})
	before := strings.Split(view(p), "\n")
	var bodyH int
	locked(p, func() { bodyH = p.frame.layout.bodyH })
	require.Positive(t, bodyH)
	for i := 40; i < 45; i++ {
		conv.Entries = append(conv.Entries, AgentMessage{Markdown: fmt.Sprintf("line %d", i)})
	}
	conv.Entries[len(conv.Entries)-1] = AgentMessage{Markdown: "a reply\nthat grew\nthree rows", Streaming: true}
	show(t, p, f, conv)
	after := strings.Split(view(p), "\n")
	assert.Equal(t, before[1:1+bodyH], after[1:1+bodyH], "output arriving below does not slide the text under the reader")
	assert.NotEqual(t, before[0], after[0], "the header counts the lines below")

	p.SendKey(uv.KeyPressEvent{Code: uv.KeyEnd})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyEnd})
	conv.Entries = append(conv.Entries, AgentMessage{Markdown: "newest"})
	show(t, p, f, conv)
	assert.Contains(t, view(p), "newest", "at the bottom it follows the output")
}

func TestPane_ReleasedSelectionDoesNotHideNewOutput(t *testing.T) {
	conv := Conversation{State: AgentRunning, Running: []string{"t"}, Entries: []Entry{
		UserMessage{Text: "select me"},
		AgentMessage{Markdown: "first reply", Streaming: true},
	}}
	p, f := newTestPane(t, 60, 24, conv)
	rows := strings.Split(view(p), "\n")
	y := rowWith(rows, "select me")
	require.Positive(t, y)
	assert.Equal(t, "select me", copyRows(t, p, f, y, y))

	conv.Entries = append(conv.Entries, AgentMessage{Markdown: "fresh output\nmore of it\nand more"})
	show(t, p, f, conv)
	v := view(p)
	assert.Contains(t, v, "fresh output", "output that arrives after the release shows")
	assert.Contains(t, v, "and more")
	locked(p, func() {
		require.NotNil(t, p.selection.rows, "the selection follows its text")
		assert.Less(t, p.selection.start.y, y, "up, as the output pushed it")
		assert.Equal(t, "select me", p.selection.text(p.width))
	})

	show(t, p, f, Conversation{State: AgentIdle, Entries: []Entry{AgentMessage{Markdown: "something else entirely"}}})
	assert.Contains(t, view(p), "something else entirely")
	locked(p, func() {
		assert.Nil(t, p.selection.rows, "its text gone, the selection goes too")
	})
}
