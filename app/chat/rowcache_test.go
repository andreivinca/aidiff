package chat

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/stretchr/testify/assert"
)

// longThread is a conversation of turns exchanges, each a question and a
// long markdown answer.
func longThread(turns int) Conversation {
	var entries []Entry
	for i := 0; i < turns; i++ {
		entries = append(entries, UserMessage{Text: fmt.Sprintf("question %d: please look at the handler and explain the flow in detail", i)})
		var md strings.Builder
		for j := 0; j < 12; j++ {
			fmt.Fprintf(&md, "Paragraph %d with **bold** and `code` and a [link](http://x) that wraps over the pane width several times to be realistic.\n\n", j)
		}
		md.WriteString("```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```\n")
		entries = append(entries, AgentMessage{Markdown: md.String()})
	}
	return Conversation{State: AgentIdle, Entries: entries}
}

// BenchmarkPane_WheelScroll is a wheel notch and the render after it, on a
// conversation of 10, 50 and 200 exchanges: without the row cache the cost
// grew with the conversation (36ms at 200), with it the transcript's
// messages are rendered once.
func BenchmarkPane_WheelScroll(b *testing.B) {
	for _, turns := range []int{10, 50, 200} {
		b.Run(fmt.Sprint(turns), func(b *testing.B) {
			f := newFakeBackend(longThread(turns))
			p := New(f, "t", 100, 50, Config{NoColor: false})
			defer p.Close()
			<-p.Updates()
			p.Render(true)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p.SendMouse(vt.Mouse(uv.MouseWheelEvent{Button: uv.MouseWheelUp}))
				p.Render(true)
			}
		})
	}
}

func TestRowCache(t *testing.T) {
	var c rowCache
	renders := 0
	render := func() []string {
		renders++
		return []string{"row"}
	}
	a, b := rowKey{kind: 'a', text: "hello", w: 40}, rowKey{kind: 'a', text: "hello▍", w: 40}
	c.rows(a, render)
	c.rows(a, render)
	c.sweep()
	c.rows(a, render)
	assert.Equal(t, 1, renders, "a message is rendered once, across renders")
	c.rows(b, render)
	assert.Equal(t, 2, renders, "changed text, as a streaming reply grows, renders again")
	c.rows(rowKey{kind: 'a', text: "hello", w: 60}, render)
	assert.Equal(t, 3, renders, "so does another width")
	c.sweep()
	c.sweep()
	c.rows(a, render)
	assert.Equal(t, 4, renders, "rows a render did not use are dropped")
}

func TestPane_StreamingReplyRendersFresh(t *testing.T) {
	conv := fixture()
	p, f := newTestPane(t, 80, 20, conv)
	assert.Contains(t, view(p), "Looking at the handler▍")
	conv.Entries[4] = AgentMessage{Markdown: "Looking at the handler and the tests", Streaming: true}
	show(t, p, f, conv)
	assert.Contains(t, view(p), "Looking at the handler and the tests▍", "a growing reply is not served from the cache")
}
