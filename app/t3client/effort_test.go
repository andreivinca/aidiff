package t3client

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModelSelectionEffort(t *testing.T) {
	caps := ModelCapabilities{OptionDescriptors: []ModelOptionDescriptor{{ID: "reasoningEffort", Type: "select", CurrentValue: json.RawMessage(`"medium"`)}}}
	for _, tt := range []struct{ name, options, want string }{
		{"default", `[]`, "medium"},
		{"codex override", `[{"id":"reasoningEffort","value":"high"}]`, "high"},
		{"claude", `[{"id":"effort","value":"max"}]`, "max"},
		{"legacy", `{"reasoningEffort":"low"}`, "low"},
		{"unrelated", `[{"id":"fastMode","value":true}]`, "medium"},
		{"missing", ``, "medium"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := &ModelSelection{Options: json.RawMessage(tt.options)}
			assert.Equal(t, tt.want, m.Effort(caps))
		})
	}
	assert.Empty(t, (*ModelSelection)(nil).Effort(caps))
	assert.Empty(t, (&ModelSelection{}).Effort(ModelCapabilities{}))
}
