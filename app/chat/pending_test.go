package chat

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPane_PendingSendVisibleBeforeAcknowledgement(t *testing.T) {
	p, f := newTestPane(t, 80, 20, fixture())
	release := make(chan struct{})
	f.set(nil, release)
	p.SendText("also check the tests\r")
	c := nextCall(t, f)
	assert.Contains(t, view(p), "Sending…")
	assert.Contains(t, view(p), "also check the tests")
	locked(p, func() {
		p.scroll = 20
	})
	assert.Contains(t, view(p), "also check the tests", "scrolling back does not push the card away")
	close(release)
	waitUpdate(t, p)
	assert.Contains(t, view(p), "Queued", "a message sent during a turn waits for the next")

	conv := fixture()
	conv.Entries = append(conv.Entries, UserMessage{Text: "also check the tests"})
	conv.Delivered = []string{c.msg.ID}
	show(t, p, f, conv)
	locked(p, func() {
		assert.Empty(t, p.pending, "the card goes once the backend took the message over")
		p.scroll = 0
	})
	assert.Equal(t, 1, strings.Count(view(p), "also check the tests"))
}

func TestPane_QueuedMessagesMergeByID(t *testing.T) {
	conv := fixture()
	conv.Queued = []Queued{{ID: "queued-msg", Text: "Please check tests too"}}
	p, f := newTestPane(t, 90, 20, conv)
	v := view(p)
	assert.Contains(t, v, "Queued")
	assert.Less(t, strings.Index(v, "Please check tests too"), strings.Index(v, "Queued"), "queue status belongs below the message")

	c := ask(t, p, f, func() {
		p.SendText("one more\r")
	})
	conv.Queued = append(conv.Queued, Queued{ID: c.msg.ID, Text: "one more, as the backend holds it"})
	show(t, p, f, conv)
	v = view(p)
	assert.Contains(t, v, "one more, as the backend holds it", "the backend's card replaces the pane's own")
	assert.Equal(t, 2, strings.Count(v, "Queued"))

	conv.Queued = nil
	show(t, p, f, conv)
	assert.Contains(t, view(p), "Queued", "the pane's card stays until the backend delivered it")
	conv.Delivered = []string{c.msg.ID}
	show(t, p, f, conv)
	assert.NotContains(t, view(p), "Queued")
}

func TestPane_PendingFailureAndIdenticalMessages(t *testing.T) {
	p, f := newTestPane(t, 80, 25, fixture())
	ids := make([]string, 0, 2)
	for range 2 {
		c := ask(t, p, f, func() {
			p.SendText("same message\r")
		})
		ids = append(ids, c.msg.ID)
	}
	assert.Equal(t, 2, strings.Count(view(p), "same message"), "identical text with different ids remains two messages")
	f.set(errors.New("offline"), nil)
	ask(t, p, f, func() {
		p.SendText("try again\r")
	})
	locked(p, func() {
		assert.Len(t, p.pending, 2)
	})
	assert.Contains(t, view(p), "send failed: offline")
	assert.Contains(t, view(p), "│   try again█")

	conv := fixture()
	conv.Delivered = ids[:1]
	show(t, p, f, conv)
	locked(p, func() {
		require.Len(t, p.pending, 1)
		assert.Equal(t, ids[1], p.pending[0].id, "only the delivered message's card goes")
	})
}

func TestPane_PendingFollowIgnoresOldSend(t *testing.T) {
	p, f := newTestPane(t, 80, 20, fixture())
	release := make(chan struct{})
	f.set(errors.New("old failure"), release)
	p.SendText("old thread message\r")
	nextCall(t, f)
	p.Follow(nil, "Other")
	close(release)
	assert.NotContains(t, view(p), "old thread message")
	assert.NotContains(t, view(p), "old failure")
}

func TestPane_PendingFitsSmallPanes(t *testing.T) {
	conv := fixture()
	conv.Queued = []Queued{{ID: "one", Text: strings.Repeat("a long message ", 30)}}
	for i := range 12 {
		conv.Queued = append(conv.Queued, Queued{ID: fmt.Sprint("more-", i), Text: "more"})
	}
	p, _ := newTestPane(t, 80, 20, conv)
	assert.Contains(t, view(p), "13 pending messages", "a long queue says how long it is")
	for _, w := range []int{1, 5, 20, 60} {
		for _, h := range []int{1, 3, 7, 12, 20} {
			p.Resize(w, h)
			rows := strings.Split(view(p), "\n")
			require.Len(t, rows, h)
			for _, row := range rows {
				assert.Equal(t, w, ansi.StringWidth(row))
			}
		}
	}
}
