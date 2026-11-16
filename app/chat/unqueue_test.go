package chat

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
)

func TestPane_UpTakesTheQueuedMessageBack(t *testing.T) {
	conv := fixture()
	conv.Queued = []Queued{{ID: "m3", Text: "older"}, {ID: "m4", Text: "fix the tests too", Images: 1}}
	p, f := newTestPane(t, 90, 24, conv)
	assert.Contains(t, view(p), "↑ edit queued", "an empty input offers it")

	c := ask(t, p, f, func() {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	})
	assert.Equal(t, call{kind: "unqueue", id: "m4"}, c, "the newest queued message is dropped from the queue first")
	assert.Equal(t, "fix the tests too", p.input.text(), "then it is in the input to edit")
	locked(p, func() {
		assert.Equal(t, "the queued message is back in the input: enter queues it again, ctrl+u drops it; its 1 image stayed behind", p.note)
	})
	assert.NotContains(t, view(p), "↑ edit queued", "with a draft, up moves in it")

	p.SendText("\x15")
	f.set(errBoom, nil)
	ask(t, p, f, func() {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	})
	assert.Empty(t, p.input.text(), "a failed take-back leaves the input alone")
	assert.Contains(t, view(p), "taking the queued message back failed: boom")
}

func TestPane_UpWithoutAQueueScrolls(t *testing.T) {
	p, f := newTestPane(t, 40, 8, longConversation())
	view(p)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	locked(p, func() {
		assert.Equal(t, 1, p.scroll, "nothing queued: up scrolls as before")
	})
	assert.Empty(t, f.calls)
}
