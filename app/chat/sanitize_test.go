package chat

import (
	"strings"
	"testing"
	"unsafe"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
)

func TestSanitize(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain text\twith a tab\nand a line", "plain text\twith a tab\nand a line"},
		{"héllo 👩‍💻 世界", "héllo 👩‍💻 世界"},
		{"copy \x1b]52;c;cm0gLXJmIH4=\x07me", "copy me"},
		{"title \x1b]0;pwned\x1b\\here", "title here"},
		{"link \x1b]8;;https://evil\x1b\\text\x1b]8;;\x1b\\", "link text"},
		{"clear\x1b[2J\x1b[Hscreen", "clearscreen"},
		{"red \x1b[31;1mword\x1b[0m", "red word"},
		{"8-bit \u009b2Jcsi and \u009d0;t\u009cosc", "8-bit csi and osc"},
		{"raw \x9b2J byte", "raw 2J byte"},
		{"bell\x07 back\bspace del\x7f", "bell backspace del"},
		{"dos\r\nline", "dos\nline"},
		{"progress 10%\r20%", "progress 10%20%"},
		{"charset \x1b(Bkept", "charset kept"},
		{"dcs \x1bPq#0;2;0;0;0\x1b\\done", "dcs done"},
		{"unterminated \x1b]52;c;AAAA", "unterminated "},
		{"lone esc\x1b", "lone esc"},
	} {
		assert.Equal(t, tc.want, sanitize(tc.in), "%q", tc.in)
	}
	s := "nothing to strip"
	assert.Same(t, unsafe.StringData(s), unsafe.StringData(sanitize(s)), "a clean string is returned as is, not copied")
}

// hostile is text a hostile server or model might send: a clipboard write,
// a window title, a screen clear and a hyperlink.
const hostile = "\x1b]52;c;cm0gLXJmIH4=\x07\x1b]0;owned\x07\x1b[2J\x1b]8;;https://evil\x1b\\"

func TestPane_StripsEscapesFromBackendText(t *testing.T) {
	conv := Conversation{
		Title: "Fix" + hostile + " login",
		State: AgentWaiting,
		Entries: []Entry{
			UserMessage{Text: "user" + hostile + " said"},
			AgentMessage{Markdown: "agent" + hostile + " wrote\n```\ncode" + hostile + " block\n```"},
			Step{Kind: StepCommand, Text: "ls" + hostile + " -l", State: Failed, Why: "exit" + hostile + " 2"},
			Failure{Text: "boom" + hostile + " failure"},
			Approval{ID: "a", Prompt: "run" + hostile + " it?", Reason: "because" + hostile + " reasons", Pending: true},
			Questions{ID: "q", Pending: true, Questions: []Question{{ID: "x", Text: "pick" + hostile + " one",
				Options: []Option{{Label: "first" + hostile + " option", Description: "desc" + hostile + " here", Value: "v\x1b[1m"}}}}},
			SecretRequest{ID: "s", Label: "sudo" + hostile + " password", Reason: "needs" + hostile + " root", Pending: false, Status: "saved"},
		},
		Queued:   []Queued{{ID: "m", Text: "queued" + hostile + " text"}},
		Activity: []Activity{{ID: "p", Title: "proc" + hostile + " title", State: Running, Status: "running", Details: []string{"out" + hostile + " put"}}},
		Settings: Settings{Mode: "Build" + hostile, Model: "Sonnet" + hostile, Details: []string{"det" + hostile + "ail"}},
	}
	p, f := newTestPane(t, 100, 60, conv)
	f.live().Note("note" + hostile + " here")
	waitUpdate(t, p)
	p.SendText("/work\r")
	assertNoEscapes(t, p.Render(true))
	v := view(p)
	for _, want := range []string{"Fix login", "user said", "agent wrote", "code block", "ls -l", "exit 2", "boom failure",
		"run it?", "because reasons", "pick one", "first option", "queued text", "Build", "Sonnet", "detail"} {
		assert.Contains(t, v, want)
	}

	locked(p, func() {
		q := p.conv.Entries[5].(Questions)
		assert.Equal(t, "v\x1b[1m", q.Questions[0].Options[0].Value, "an option's value goes back to the backend exact")
	})
	assert.Equal(t, "v\x1b[1m", conv.Entries[5].(Questions).Questions[0].Options[0].Value)
	assert.Equal(t, "first"+hostile+" option", conv.Entries[5].(Questions).Questions[0].Options[0].Label,
		"what the backend showed is copied, never written to")

	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft}) // the activity list
	p.SendText("\r")                              // its details
	assertNoEscapes(t, p.Render(true))
	assert.Contains(t, view(p), "proc title")
	assert.Contains(t, view(p), "out put")
}

func TestPane_StripsEscapesFromTheDraftAndTitle(t *testing.T) {
	p, f := newTestPane(t, 80, 12, fixture())
	p.Paste("pasted" + hostile + " text\r\nline")
	assert.Equal(t, "pasted text\nline", p.input.text())
	p.SendText("\u009b")
	assert.Equal(t, "pasted text\nline", p.input.text(), "a typed C1 control is dropped")
	follow(t, p, f, "Thread"+hostile+" title")
	assertNoEscapes(t, p.Render(true))
}

// assertNoEscapes fails when out holds what a terminal would act on beyond
// colors: an OSC, a BEL, a screen clear or an 8-bit control.
func assertNoEscapes(t *testing.T, out string) {
	t.Helper()
	for _, bad := range []string{"\x1b]", "\x07", "\x1b[2J", "\u009b", "\u009d", "\x1bP"} {
		assert.NotContains(t, out, bad)
	}
	assert.False(t, strings.ContainsFunc(out, func(r rune) bool {
		return r < 0x20 && r != '\n' && r != 0x1b
	}), "no control character but the line break and the pane's own styling")
}
