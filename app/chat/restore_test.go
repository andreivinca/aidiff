package chat

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
)

func TestPane_FailedSendKeepsADraftTypedMeanwhile(t *testing.T) {
	p, f := newTestPane(t, 80, 20, fixture())
	hold := make(chan struct{})
	f.set(errBoom, hold)
	p.SendText("the message\r")
	nextCall(t, f)
	p.SendText("a draft typed meanwhile")
	close(hold)
	waitUpdate(t, p)
	assert.Equal(t, "the message\na draft typed meanwhile", p.input.text(), "the failed message comes back ahead of the draft, neither lost")
	assert.True(t, p.input.atEnd(), "the cursor stays where it was in the draft")
	assert.Contains(t, view(p), "send failed: boom")
	assert.NotContains(t, view(p), "Sending…", "its card is gone")
}

func TestPane_FailedSendComesBackAfterFollow(t *testing.T) {
	p, f := newTestPane(t, 80, 20, fixture())
	hold := make(chan struct{})
	f.set(errBoom, hold)
	p.SendText("the message\r")
	nextCall(t, f)
	p.SendText("draft for the next thread")
	other := newFakeBackend(Conversation{Title: "Other"})
	follow(t, p, other, "Other")
	close(hold)
	waitUpdate(t, p)
	assert.Equal(t, "the message\ndraft for the next thread", p.input.text(),
		"a message that failed after the pane moved on still comes back: the draft outlives the conversation")
	assert.Contains(t, view(p), "send failed: boom")
}

func TestPane_UnqueueKeepsADraftTypedMeanwhile(t *testing.T) {
	conv := fixture()
	conv.Queued = []Queued{{ID: "m4", Text: "the queued one"}}
	p, f := newTestPane(t, 90, 24, conv)
	hold := make(chan struct{})
	f.set(nil, hold)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	nextCall(t, f)
	p.SendText("typed while it came back")
	close(hold)
	waitUpdate(t, p)
	assert.Equal(t, "the queued one\ntyped while it came back", p.input.text(), "the queued message is never dropped")

	p.SendText("\x15")
	hold = make(chan struct{})
	f.set(nil, hold)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	nextCall(t, f)
	follow(t, p, newFakeBackend(Conversation{Title: "Other"}), "Other")
	close(hold)
	waitUpdate(t, p)
	assert.Equal(t, "the queued one", p.input.text(), "nor after the pane moved on")
}
