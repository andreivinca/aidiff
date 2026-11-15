package t3chat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/umputun/revdiff/app/t3client"
)

func TestPaneEffortCommand(t *testing.T) {
	snap := fixture()
	snap.Thread.ModelSelection = &t3client.ModelSelection{InstanceID: "codex", Model: "gpt-5", Options: json.RawMessage(`[{"id":"serviceTier","value":"priority"}]`)}
	p, f := newTestPane(t, 120, 24, snap)
	waitFor(t, p, func() bool { return p.catalog != nil })
	p.mu.Lock()
	for i := range p.catalog.Models {
		m := &p.catalog.Models[i]
		if m.InstanceID == "codex" {
			m.Capabilities = t3client.ModelCapabilities{OptionDescriptors: []t3client.ModelOptionDescriptor{{ID: "reasoningEffort", Type: "select", CurrentValue: json.RawMessage(`"medium"`), Options: []t3client.ModelOptionValue{{ID: "low"}, {ID: "medium"}, {ID: "high"}}}}}
		}
	}
	p.mu.Unlock()
	p.SendText("/effort\r")
	assert.Contains(t, view(p), "effort: medium")
	p.SendText("/effort ")
	assert.Equal(t, []string{"low", "medium", "high"}, labels(p.suggestionsLocked()))
	p.SendText("high\r")
	cmd := nextCommand(t, f)
	assert.Equal(t, "thread.model-selection.set", cmd["type"])
	selection := cmd["modelSelection"].(t3client.ModelSelection)
	assert.JSONEq(t, `[{"id":"serviceTier","value":"priority"},{"id":"reasoningEffort","value":"high"}]`, string(selection.Options))
	waitFor(t, p, func() bool { return !p.busy })
	f.live().Thread(t3client.Thread{ID: "th-1", ModelSelection: &selection})
	assert.Contains(t, view(p), "GPT-5 · high")
	p.SendText("/effort invalid\r")
	assert.Contains(t, view(p), "unsupported effort")
	assert.Empty(t, f.cmds, "invalid levels are not dispatched")
	f.live().Thread(t3client.Thread{ID: "th-1", ModelSelection: &t3client.ModelSelection{InstanceID: "claude", Model: "claude-sonnet-4"}})
	p.SendText("/effort high\r")
	assert.Contains(t, view(p), "does not advertise adjustable effort")
	assert.Empty(t, f.cmds)
}
