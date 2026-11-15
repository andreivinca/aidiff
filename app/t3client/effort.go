package t3client

import "encoding/json"

// ModelCapabilities contains the server-reported option defaults.
type ModelCapabilities struct {
	OptionDescriptors []ModelOptionDescriptor `json:"optionDescriptors"`
}

type ModelOptionDescriptor struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	CurrentValue json.RawMessage `json:"currentValue"`
}

// Effort resolves an explicit selection before the model's reported default.
// Older servers may serialize selections as an object instead of an array.
func (m *ModelSelection) Effort(capabilities ModelCapabilities) string {
	if m == nil {
		return ""
	}
	values := map[string]json.RawMessage{}
	var options []struct {
		ID    string          `json:"id"`
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(m.Options, &options) == nil {
		for _, o := range options {
			values[o.ID] = o.Value
		}
	} else {
		_ = json.Unmarshal(m.Options, &values)
	}
	ids := []string{"reasoningEffort", "effort", "reasoning", "variant", "reasoning_effort", "thinking_level"}
	for _, id := range ids {
		var value string
		if json.Unmarshal(values[id], &value) == nil && value != "" {
			return value
		}
	}
	for _, id := range ids {
		for _, d := range capabilities.OptionDescriptors {
			var value string
			if d.ID == id && d.Type == "select" && json.Unmarshal(d.CurrentValue, &value) == nil && value != "" {
				return value
			}
		}
	}
	return ""
}
