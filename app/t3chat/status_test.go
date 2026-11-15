package t3chat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/umputun/revdiff/app/t3client"
)

func TestPanePersistentStatusWhileTypingAndFinishing(t *testing.T) {
	p, f := newTestPane(t, 100, 25, activityFixture())
	p.SendText("a draft")
	assert.Contains(t, view(p), "Working · 1 subagent · 2 processes", "counts stay visible with a nonempty composer")
	f.live().Run(t3client.Run{ID: "run-2", Ordinal: 2, Status: "completed"})
	assert.Contains(t, view(p), "Working · 1 subagent · 2 processes", "background work keeps the status working after the turn ends")
	f.live().Subagent(t3client.Subagent{ID: "a1", Status: "completed", NativeTaskRef: &t3client.ProviderRef{NativeID: "agent-native-1"}})
	f.live().ProviderThread(t3client.ProviderThread{ID: "provider-1"})
	assert.Contains(t, view(p), "Idle")
	assert.NotContains(t, view(p), "Working")
	assert.NotContains(t, view(p), "subagent ·")
}

func TestPaneStatusWaitingAndNoExtraRequests(t *testing.T) {
	p, f := newTestPane(t, 100, 20, activityFixture())
	f.live().Request(t3client.RuntimeRequest{ID: "approval", Status: "pending"})
	assert.Contains(t, view(p), "Waiting for you · 1 subagent · 2 processes")
	for i := 0; i < 10; i++ {
		p.SendText("x")
		view(p)
	}
	assert.Empty(t, f.cmds, "status uses subscribed events without fetching or polling")
	p.Follow(nil, "other", "Other")
	assert.Contains(t, view(p), "not connected")
	assert.NotContains(t, view(p), "1 subagent")
}
