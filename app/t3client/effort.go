package t3client

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ModelCapabilities contains the server-reported option defaults.
type ModelCapabilities struct {
	OptionDescriptors []ModelOptionDescriptor `json:"optionDescriptors"`
}

type ModelOptionDescriptor struct {
	ID                   string             `json:"id"`
	Type                 string             `json:"type"`
	CurrentValue         json.RawMessage    `json:"currentValue"`
	Options              []ModelOptionValue `json:"options"`
	PromptInjectedValues []string           `json:"promptInjectedValues"`
}

// Effort resolves an explicit selection before the model's reported default.
// When the model's capabilities are known, only its own effort option counts,
// so a value left over from another provider's model is not shown. The default
// is the descriptor's currentValue (Codex) or its isDefault option (Claude).
// Older servers may serialize selections as an object instead of an array.
func (m *ModelSelection) Effort(capabilities ModelCapabilities) string {
	if m == nil {
		return ""
	}
	values := m.optionValues()
	explicit := func(id string) string {
		var value string
		if json.Unmarshal(values[id], &value) == nil {
			return value
		}
		return ""
	}
	if d, ok := capabilities.EffortOption(); ok {
		if v := explicit(d.ID); v != "" {
			return v
		}
		return d.defaultValue()
	}
	for _, id := range effortIDs {
		if v := explicit(id); v != "" {
			return v
		}
	}
	return ""
}

// optionValues reads the selection's options by id, from the array form or
// the older object form.
func (m *ModelSelection) optionValues() map[string]json.RawMessage {
	values := map[string]json.RawMessage{}
	var options []struct {
		ID    string          `json:"id"`
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(m.Options, &options) == nil {
		for _, o := range options {
			values[o.ID] = o.Value
		}
		return values
	}
	_ = json.Unmarshal(m.Options, &values)
	return values
}

// defaultValue is the level the model runs with when none is chosen.
func (d ModelOptionDescriptor) defaultValue() string {
	var value string
	if json.Unmarshal(d.CurrentValue, &value) == nil && value != "" {
		return value
	}
	for _, o := range d.Options {
		if o.IsDefault {
			return o.ID
		}
	}
	return ""
}

var effortIDs = []string{"reasoningEffort", "effort", "reasoning", "variant", "reasoning_effort", "thinking_level"}

type ModelOptionValue struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	IsDefault bool   `json:"isDefault"`
}

// EffortOption returns the model's supported reasoning selector.
func (c ModelCapabilities) EffortOption() (ModelOptionDescriptor, bool) {
	for _, id := range effortIDs {
		for _, d := range c.OptionDescriptors {
			if d.ID == id && d.Type == "select" {
				return d, true
			}
		}
	}
	return ModelOptionDescriptor{}, false
}

// SelectableValues excludes options that T3 implements by modifying a prompt.
func (d ModelOptionDescriptor) SelectableValues() []ModelOptionValue {
	var out []ModelOptionValue
	for _, o := range d.Options {
		if !slices.Contains(d.PromptInjectedValues, o.ID) {
			out = append(out, o)
		}
	}
	return out
}

// WithEffort changes only the effort option, preserving all other model settings.
func (m ModelSelection) WithEffort(d ModelOptionDescriptor, level string) (ModelSelection, error) {
	value := ""
	for _, o := range d.SelectableValues() {
		if strings.EqualFold(o.ID, level) {
			value = o.ID
			break
		}
	}
	if value == "" {
		return m, fmt.Errorf("unsupported effort %q", level)
	}
	type option struct {
		ID    string          `json:"id"`
		Value json.RawMessage `json:"value"`
	}
	var options []option
	if len(m.Options) > 0 && string(m.Options) != "null" {
		if err := json.Unmarshal(m.Options, &options); err != nil {
			var legacy map[string]json.RawMessage
			if err := json.Unmarshal(m.Options, &legacy); err != nil {
				return m, fmt.Errorf("read model options: %w", err)
			}
			var keys []string
			for id := range legacy {
				keys = append(keys, id)
			}
			slices.Sort(keys)
			for _, id := range keys {
				options = append(options, option{ID: id, Value: legacy[id]})
			}
		}
	}
	options = slices.DeleteFunc(options, func(o option) bool { return o.ID == d.ID })
	encoded, _ := json.Marshal(value)
	options = append(options, option{ID: d.ID, Value: encoded})
	data, err := json.Marshal(options)
	if err != nil {
		return m, fmt.Errorf("encode model options: %w", err)
	}
	m.Options = data
	return m, nil
}
