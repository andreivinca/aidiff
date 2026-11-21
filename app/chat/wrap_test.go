package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// longCurl is a shell command with a JSON body, as an agent puts one in a
// code block: long tokens, no space where a word wrap could break them.
const longCurl = `curl -sS -X POST https://api.example.com/v1/models/add-update -H 'Content-Type: application/json' ` +
	`-d '{"model":"llama-3.1-8b","prompt":"Request the size of one call","max_tokens":150,"stream":false}'`

// joinPieces puts a wrapped line back together, as a copy does.
func joinPieces(pieces []piece) string {
	var b strings.Builder
	for _, pc := range pieces {
		b.WriteString(pc.joint + pc.text)
	}
	return b.String()
}

func TestWrapLine(t *testing.T) {
	for _, line := range []string{longCurl, "    " + longCurl, "a  b   c", "x", "", "世界世界世界世界 emoji 👩‍💻👩‍💻👩‍💻 end",
		strings.Repeat("word ", 40), "trailing   "} {
		for _, limit := range []int{1, 2, 7, 13, 40} {
			hard := wrapLine(line, limit, max(1, limit-2), true)
			assert.Equal(t, line, joinPieces(hard), "a hard wrap keeps every character, %q at %d", line, limit)
			for i, pc := range hard {
				assert.Empty(t, pc.joint, "a hard wrap takes nothing out")
				assert.Equal(t, i > 0, pc.cont)
			}
			word := wrapLine(line, limit, limit, false)
			assert.Equal(t, strings.TrimRight(line, " "), strings.TrimRight(joinPieces(word), " "),
				"a word wrap's joints put the line back, %q at %d", line, limit)
			for _, pc := range word {
				if limit > 1 {
					assert.LessOrEqual(t, ansi.StringWidth(pc.text), limit, "%q at %d", line, limit)
				}
			}
		}
	}
	got := wrapLine("alpha beta  gamma", 10, 10, false)
	assert.Equal(t, []piece{{text: "alpha beta"}, {text: "gamma", cont: true, joint: "  "}}, got,
		"the spaces a wrap breaks at leave the rows and come back in a copy")
}

// copyRows drags over rows first to last of the pane on screen and returns
// what was copied.
func copyRows(t *testing.T, p *Pane, f *fakeBackend, first, last int) string {
	t.Helper()
	p.SendMouse(uv.MouseClickEvent{X: 0, Y: first, Button: uv.MouseLeft})
	p.SendMouse(uv.MouseMotionEvent{X: p.width, Y: last, Button: uv.MouseLeft})
	p.SendMouse(uv.MouseReleaseEvent{X: p.width, Y: last, Button: uv.MouseLeft})
	select {
	case got := <-f.clip.copied:
		waitUpdate(t, p)
		return got
	case <-time.After(waitLimit):
		t.Fatal("selection was not copied")
		return ""
	}
}

// transcriptView is the transcript and the queued cards of the frame on
// screen, without the header and the composer.
func transcriptView(p *Pane) string {
	rows := strings.Split(view(p), "\n")
	var l layout
	locked(p, func() { l = p.frame.layout })
	return strings.Join(rows[l.headerH:l.textBottom], "\n")
}

// rowWith is the first row of rows holding text, -1 when none does.
func rowWith(rows []string, text string) int {
	for i, r := range rows {
		if strings.Contains(r, text) {
			return i
		}
	}
	return -1
}

func TestPane_WrapsCodeAndCopiesTheLineWhole(t *testing.T) {
	conv := Conversation{State: AgentIdle, Entries: []Entry{
		AgentMessage{Markdown: "Run this:\n```sh\n    " + longCurl + "\n```\ndone"},
	}}
	p, f := newTestPane(t, 40, 40, conv)
	rows := strings.Split(view(p), "\n")
	assert.NotContains(t, transcriptView(p), "…", "nothing is cut")
	first, last := rowWith(rows, "    curl -sS"), rowWith(rows, `false}'`)
	require.Positive(t, first)
	require.Greater(t, last, first+3, "the command takes several rows")
	assert.True(t, strings.HasPrefix(rows[first], inset+codeGutter+"    curl"), "the first row keeps the line's indentation: %q", rows[first])
	for _, r := range rows[first+1 : last+1] {
		assert.True(t, strings.HasPrefix(r, inset+codeGutter+"      "),
			"a continuation row hangs two cells under the line's indentation: %q", r)
		assert.LessOrEqual(t, ansi.StringWidth(r), 40)
	}

	assert.Equal(t, "    "+longCurl, copyRows(t, p, f, first, last),
		"the wrapped line copies as one line, without the inset or the hanging indent")
	assert.Equal(t, "Run this:\n    "+longCurl+"\ndone", copyRows(t, p, f, first-1, last+1))
}

func TestPane_WrappedProseCopiesAsItWasWritten(t *testing.T) {
	para := "The handler validates the token before   it reads the body, so a request without one fails early with 401."
	conv := Conversation{State: AgentIdle, Entries: []Entry{
		UserMessage{Text: "explain why the login handler rejects requests that carry a valid session cookie"},
		AgentMessage{Markdown: para + "\n\n- a bullet item long enough to wrap onto a second row of the pane"},
	}}
	p, f := newTestPane(t, 36, 40, conv)
	rows := strings.Split(view(p), "\n")
	first := rowWith(rows, "The handler")
	last := rowWith(rows, "401.")
	require.Greater(t, last, first)
	assert.Equal(t, para, copyRows(t, p, f, first, last), "the spaces a wrap took come back")

	bullet := rowWith(rows, "• a bullet")
	require.Positive(t, bullet)
	assert.True(t, strings.HasPrefix(rows[bullet+1], inset+"  "), "a bullet's text hangs under itself")
	assert.Equal(t, "• a bullet item long enough to wrap onto a second row of the pane", copyRows(t, p, f, bullet, bullet+2))

	user := rowWith(rows, "explain why")
	assert.Equal(t, "explain why the login handler rejects requests that carry a valid session cookie",
		copyRows(t, p, f, user, user+2), "the user's message too")
}

func TestPane_WrapsWhatItUsedToCut(t *testing.T) {
	long := "the quick brown fox jumps over the lazy dog and keeps running far beyond the edge of the pane"
	conv := Conversation{State: AgentWaiting, Entries: []Entry{
		TodoList{Steps: []TodoStep{{Text: "todo " + long, State: Running}}},
		Approval{ID: "a1", Prompt: "approve " + long, Pending: true},
		Approval{ID: "a0", Prompt: "decided " + long},
		Questions{ID: "q0", Questions: []Question{{ID: "x", Text: "asked " + long}}},
		Failure{Text: "error " + long + "\nsecond line of the error"},
		SecretRequest{ID: "s", Label: "secret " + long, Status: "saved"},
		Step{Kind: StepCommand, Text: "command " + long + "\n--and a second line", State: Failed, Why: "exit 2"},
	}}
	p, _ := newTestPane(t, 30, 80, conv)
	p.SendText("/work\r")
	v := view(p)
	assert.NotContains(t, transcriptView(p), "…", "no line of the agent's is cut")
	text := strings.Join(strings.Fields(v), " ")
	for _, want := range []string{"todo " + long, "approve " + long, "decided " + long, "asked " + long,
		"error " + long, "second line of the error", "secret " + long, "command " + long, "--and a second line ✗ exit 2"} {
		assert.Contains(t, text, want)
	}
	for _, r := range strings.Split(v, "\n") {
		assert.Equal(t, 30, ansi.StringWidth(r))
	}
}

func TestPane_ShowsALongNoteWhole(t *testing.T) {
	p, f := newTestPane(t, 30, 30, fixture())
	note := strings.Repeat("server said something long ", 8) + "END"
	f.live().Note(note)
	waitUpdate(t, p)
	assert.Contains(t, strings.Join(strings.Fields(view(p)), " "), strings.TrimSpace(note))
}

func TestPane_ExpandedActivityShowsEverything(t *testing.T) {
	var details []string
	for i := 1; i <= 60; i++ {
		details = append(details, fmt.Sprintf("output line %d", i))
	}
	title := "a background command with a title far too long for one row of the list"
	conv := fixture()
	conv.Activity = []Activity{{ID: "p1", Title: title, State: Running, Status: "running",
		Progress: "progress that is also long enough to need a second row", Details: []string{strings.Join(details, "\n")}}}
	p, _ := newTestPane(t, 40, 20, conv)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendText("\r")
	v := strings.Join(strings.Fields(view(p)), " ")
	assert.Contains(t, v, title, "the expanded entry's title is whole")
	assert.Contains(t, v, "progress that is also long enough to need a second row")
	assert.NotContains(t, view(p), "output line 60")
	for range 20 {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyPgDown})
		view(p)
	}
	assert.Contains(t, view(p), "output line 60", "page down reads the details to their end")
	for range 20 {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyPgUp})
		view(p)
	}
	assert.Contains(t, view(p), "output line 1 ", "and back")
}
