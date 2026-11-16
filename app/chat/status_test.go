package chat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPane_StatusCountsBackgroundWork(t *testing.T) {
	p, f := newTestPane(t, 100, 25, activityFixture())
	p.SendText("a draft")
	assert.Contains(t, view(p), "Working · 1 subagent · 2 processes", "counts stay visible with a nonempty composer")
	conv := activityFixture()
	conv.State, conv.Running = AgentIdle, nil
	show(t, p, f, conv)
	assert.Contains(t, view(p), "Working · 1 subagent · 2 processes", "background work keeps the status working after the turn ends")
	conv.Activity = conv.Activity[:1]
	show(t, p, f, conv)
	assert.Contains(t, view(p), "Idle")
	assert.NotContains(t, view(p), "Working")

	conv = activityFixture()
	conv.State = AgentWaiting
	show(t, p, f, conv)
	assert.Contains(t, view(p), "Waiting for you · 1 subagent · 2 processes")
	conv.State = "interrupted"
	show(t, p, f, conv)
	assert.Contains(t, view(p), "Fix login · interrupted", "a state the pane does not know is shown as is")
	assert.Contains(t, view(p), "Interrupted · 1 subagent")
	p.Follow(nil, "Other")
	assert.Contains(t, view(p), "not connected")
	assert.NotContains(t, view(p), "1 subagent")
}
