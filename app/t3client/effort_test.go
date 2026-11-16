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
	acp := ModelCapabilities{OptionDescriptors: []ModelOptionDescriptor{
		{ID: "thought_level", Type: "select", Role: "reasoning", Options: []ModelOptionValue{{ID: "deep", IsDefault: true}}},
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
		{"agent's own id with the reasoning role", `[{"id":"thought_level","value":"shallow"}]`, "shallow", acp},
		{"no catalog yet: unknown", `[{"id":"effort","value":"max"}]`, "", ModelCapabilities{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := &ModelSelection{Options: json.RawMessage(tt.options)}
			assert.Equal(t, tt.want, m.Effort(tt.caps))
		})
	}
	assert.Empty(t, (*ModelSelection)(nil).Effort(codex))
	assert.Empty(t, (&ModelSelection{}).Effort(ModelCapabilities{}))
}

func TestWithEffortPreservesOptionsInTheirForm(t *testing.T) {
	for _, key := range []string{"effort", "reasoningEffort"} {
		d := ModelOptionDescriptor{ID: key, Type: "select", Options: []ModelOptionValue{{ID: "high"}, {ID: "ultrathink"}}, PromptInjectedValues: []string{"ultrathink"}}
		caps := ModelCapabilities{OptionDescriptors: []ModelOptionDescriptor{d}}
		for _, tt := range []struct {
			name, raw, want string
		}{
			{"array", `[{"id":"serviceTier","value":"priority"},{"id":"fastMode","value":true}]`,
				`[{"id":"serviceTier","value":"priority"},{"id":"fastMode","value":true},{"id":"` + key + `","value":"high"}]`},
			{"object from an older server", `{"serviceTier":"priority","fastMode":true}`,
				`{"serviceTier":"priority","fastMode":true,"` + key + `":"high"}`},
			{"none yet", ``, `[{"id":"` + key + `","value":"high"}]`},
		} {
			t.Run(key+" "+tt.name, func(t *testing.T) {
				m := ModelSelection{InstanceID: "provider", Model: "model", Options: json.RawMessage(tt.raw)}
				changed, err := m.WithEffort(d, "HIGH")
				require.NoError(t, err)
				assert.Equal(t, m.InstanceID, changed.InstanceID)
				assert.Equal(t, m.Model, changed.Model)
				assert.Equal(t, "high", changed.Effort(caps))
				assert.JSONEq(t, tt.want, string(changed.Options))
				assert.Equal(t, tt.raw, string(m.Options), "original selection is not mutated")
				_, err = m.WithEffort(d, "invalid")
				require.Error(t, err)
				_, err = m.WithEffort(d, "ultrathink")
				require.Error(t, err, "a prompt-injected value is not an effort level")
			})
		}
		broken := ModelSelection{Options: json.RawMessage(`broken`)}
		_, err := broken.WithEffort(d, "high")
		require.Error(t, err)
	}
	_, err := (*ModelSelection)(nil).WithEffort(ModelOptionDescriptor{}, "high")
	require.Error(t, err)
}

func TestWithEffortReplacesAnEarlierLevel(t *testing.T) {
	d := ModelOptionDescriptor{ID: "effort", Type: "select", Options: []ModelOptionValue{{ID: "low"}, {ID: "max"}}}
	m := ModelSelection{Options: json.RawMessage(`[{"id":"effort","value":"low"},{"id":"fastMode","value":true}]`)}
	changed, err := m.WithEffort(d, "max")
	require.NoError(t, err)
	assert.JSONEq(t, `[{"id":"fastMode","value":true},{"id":"effort","value":"max"}]`, string(changed.Options))
}
