package t3client

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelSelectionEffort(t *testing.T) {
	// the shapes T3 reports: Codex names its default in currentValue, Claude
	// marks one option isDefault
	codex := ModelCapabilities{OptionDescriptors: []ModelOptionDescriptor{{ID: "reasoningEffort", Type: "select", CurrentValue: json.RawMessage(`"medium"`)}}}
	claude := ModelCapabilities{OptionDescriptors: []ModelOptionDescriptor{
		{ID: "effort", Type: "select", Options: []ModelOptionValue{{ID: "low"}, {ID: "medium", IsDefault: true}, {ID: "max"}}},
		{ID: "fastMode", Type: "boolean"},
	}}
	for _, tt := range []struct {
		name, options, want string
		caps                ModelCapabilities
	}{
		{"codex default", `[]`, "medium", codex},
		{"codex override", `[{"id":"reasoningEffort","value":"high"}]`, "high", codex},
		{"codex legacy", `{"reasoningEffort":"low"}`, "low", codex},
		{"codex unrelated", `[{"id":"fastMode","value":true}]`, "medium", codex},
		{"codex missing", ``, "medium", codex},
		{"claude override", `[{"id":"effort","value":"max"}]`, "max", claude},
		{"claude default", `[]`, "medium", claude},
		{"claude missing", ``, "medium", claude},
		{"claude ignores codex leftover", `[{"id":"reasoningEffort","value":"low"}]`, "medium", claude},
		{"no catalog yet", `[{"id":"effort","value":"max"}]`, "max", ModelCapabilities{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := &ModelSelection{Options: json.RawMessage(tt.options)}
			assert.Equal(t, tt.want, m.Effort(tt.caps))
		})
	}
	assert.Empty(t, (*ModelSelection)(nil).Effort(codex))
	assert.Empty(t, (&ModelSelection{}).Effort(ModelCapabilities{}))
}

func TestWithEffortPreservesOptions(t *testing.T) {
	for _, key := range []string{"effort", "reasoningEffort"} {
		d := ModelOptionDescriptor{ID: key, Type: "select", Options: []ModelOptionValue{{ID: "high"}, {ID: "ultrathink"}}, PromptInjectedValues: []string{"ultrathink"}}
		for _, raw := range []string{`[{"id":"serviceTier","value":"priority"},{"id":"fastMode","value":true}]`, `{"serviceTier":"priority","fastMode":true}`} {
			m := ModelSelection{InstanceID: "provider", Model: "model", Options: json.RawMessage(raw)}
			changed, err := m.WithEffort(d, "HIGH")
			require.NoError(t, err)
			assert.Equal(t, m.InstanceID, changed.InstanceID)
			assert.Equal(t, m.Model, changed.Model)
			assert.Equal(t, "high", changed.Effort(ModelCapabilities{}))
			var options []struct {
				ID    string
				Value any
			}
			require.NoError(t, json.Unmarshal(changed.Options, &options))
			values := map[string]any{}
			for _, o := range options {
				values[o.ID] = o.Value
			}
			assert.Equal(t, map[string]any{key: "high", "serviceTier": "priority", "fastMode": true}, values)
			assert.Equal(t, raw, string(m.Options), "original selection is not mutated")
			_, err = m.WithEffort(d, "invalid")
			require.Error(t, err)
			_, err = m.WithEffort(d, "ultrathink")
			require.Error(t, err)
		}
		_, err := (ModelSelection{Options: json.RawMessage(`broken`)}).WithEffort(d, "high")
		require.Error(t, err)
	}
}
