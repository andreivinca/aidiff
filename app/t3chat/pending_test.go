package t3chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umputun/revdiff/app/t3client"
)

func TestPanePendingSendVisibleBeforeAcknowledgement(t *testing.T) {
	p, f := newTestPane(t, 80, 20, fixture())
	release := make(chan struct{})
	defer close(release)
	p.mu.Lock()
	p.cfg.Dispatch = func(_ context.Context, _ *t3client.Conn, cmd t3client.Command) error {
		f.cmds <- cmd
		<-release
		return nil
	}
	p.mu.Unlock()
	p.SendText("also check the tests\r")
	cmd := nextCommand(t, f)
	assert.Contains(t, view(p), "Sending…")
	assert.Contains(t, view(p), "also check the tests")
	// Assistant updates and scrollback must not push the user's pending card away.
	for i := range 30 {
		f.live().TurnItem(t3client.TurnItem{ID: fmt.Sprint("reply-", i), Type: "assistant_message", Text: "Still working"})
	}
	p.mu.Lock()
	p.scroll = 20
	p.mu.Unlock()
	assert.Contains(t, view(p), "also check the tests")
	// A real message has its own turn-item ID: reconcile using messageId.
	f.live().TurnItem(t3client.TurnItem{ID: "server-user-row", MessageID: cmd["messageId"].(string), Type: "user_message", Status: "completed", Text: "also check the tests"})
	p.mu.Lock()
	assert.Empty(t, p.pending)
	p.scroll = 0
	p.mu.Unlock()
	assert.Equal(t, 1, strings.Count(view(p), "also check the tests"))
}

func TestPaneQueuedMessagesSurviveSnapshotAndStart(t *testing.T) {
	snap := fixture()
	snap.Messages = []t3client.Message{{ID: "queued-msg", Role: "user", Text: "Please check tests too"}}
	snap.Runs = append(snap.Runs, t3client.Run{ID: "queued-run", Ordinal: 3, Status: "queued", UserMessageID: "queued-msg"})
	p, f := newTestPane(t, 90, 20, snap)
	assert.Contains(t, view(p), "Queued")
	assert.Contains(t, view(p), "Please check tests too")
	assert.Less(t, strings.Index(view(p), "Please check tests too"), strings.Index(view(p), "Queued"), "queue status belongs below the message")
	// A queue update from another client is visible here too.
	f.live().Message(t3client.Message{ID: "queued-msg", Role: "user", Text: "Check the updated tests"})
	assert.Contains(t, view(p), "Check the updated tests")
	// Queued transcript rows stay pinned instead of being duplicated.
	f.live().TurnItem(t3client.TurnItem{ID: "queued-row", MessageID: "queued-msg", RunID: new("queued-run"), Type: "user_message", InputIntent: "queued_turn", Status: "pending", Text: "Check the updated tests"})
	assert.Equal(t, 1, strings.Count(view(p), "Check the updated tests"))
	f.live().Run(t3client.Run{ID: "queued-run", Ordinal: 3, Status: "running", UserMessageID: "queued-msg"})
	f.live().TurnItem(t3client.TurnItem{ID: "queued-row", MessageID: "queued-msg", Type: "user_message", InputIntent: "turn_start", Status: "completed", Text: "Check the updated tests"})
	assert.NotContains(t, view(p), "Queued")
	assert.Equal(t, 1, strings.Count(view(p), "Check the updated tests"))
}

func TestPanePendingFailureAndIdenticalMessages(t *testing.T) {
	p, f := newTestPane(t, 80, 25, fixture())
	for range 2 {
		p.SendText("same message\r")
		nextCommand(t, f)
		waitUpdate(t, p)
	}
	assert.Equal(t, 2, strings.Count(view(p), "same message"), "identical text with different IDs must remain two messages")
	f.fail = errors.New("offline")
	p.SendText("try again\r")
	nextCommand(t, f)
	waitUpdate(t, p)
	p.mu.Lock()
	assert.Len(t, p.pending, 2)
	p.mu.Unlock()
	assert.Contains(t, view(p), "send failed: offline")
	assert.Contains(t, view(p), "│   try again█")
}

func TestPanePendingThreadSwitchIgnoresOldSend(t *testing.T) {
	p, f := newTestPane(t, 80, 20, fixture())
	release, done := make(chan struct{}), make(chan struct{})
	p.mu.Lock()
	p.cfg.Dispatch = func(_ context.Context, _ *t3client.Conn, cmd t3client.Command) error {
		f.cmds <- cmd
		<-release
		defer close(done)
		return errors.New("old failure")
	}
	p.mu.Unlock()
	p.SendText("old thread message\r")
	nextCommand(t, f)
	p.Follow(nil, "other-thread", "Other")
	close(release)
	<-done
	assert.NotContains(t, view(p), "old thread message")
	assert.NotContains(t, view(p), "old failure")
}

func TestPanePendingFitsSmallPanes(t *testing.T) {
	p, _ := newTestPane(t, 80, 20, fixture())
	p.mu.Lock()
	p.pending = []queuedMessage{{id: "one", text: strings.Repeat("a long message ", 30), queued: true}}
	p.mu.Unlock()
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

func TestPaneCancelledQueueClearsPendingCard(t *testing.T) {
	snap := fixture()
	snap.Messages = []t3client.Message{{ID: "q", Role: "user", Text: "queued request"}}
	snap.Runs = append(snap.Runs, t3client.Run{ID: "qr", Ordinal: 3, Status: "queued", UserMessageID: "q"})
	p, f := newTestPane(t, 80, 20, snap)
	assert.Contains(t, view(p), "queued request")
	f.live().Run(t3client.Run{ID: "qr", Ordinal: 3, Status: "cancelled", UserMessageID: "q"}) //nolint:misspell // exercise the server status spelling
	assert.NotContains(t, view(p), "queued request")
}

func TestPaneSameTextAcknowledgesOnlyMatchingMessage(t *testing.T) {
	p, f := newTestPane(t, 80, 25, fixture())
	cmds := make([]t3client.Command, 0, 2)
	for range 2 {
		p.SendText("identical\r")
		cmds = append(cmds, nextCommand(t, f))
		waitUpdate(t, p)
	}
	f.live().TurnItem(t3client.TurnItem{ID: "row", MessageID: cmds[0]["messageId"].(string), Type: "user_message", Status: "completed", Text: "identical"})
	p.mu.Lock()
	defer p.mu.Unlock()
	require.Len(t, p.pending, 1)
	assert.Equal(t, cmds[1]["messageId"], p.pending[0].id)
}
