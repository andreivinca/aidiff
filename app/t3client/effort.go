package t3client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ModelCapabilities are the options a model takes, as the server reports them.
type ModelCapabilities struct {
	OptionDescriptors []ModelOptionDescriptor `json:"optionDescriptors"`
}

// ModelOptionDescriptor is one option a model takes: a select with its
// choices, or a boolean.
type ModelOptionDescriptor struct {
	ID                   string             `json:"id"`
	Type                 string             `json:"type"` // select or boolean
	Role                 string             `json:"role"` // "reasoning" marks an effort control under an agent's own id
	CurrentValue         json.RawMessage    `json:"currentValue"`
	Options              []ModelOptionValue `json:"options"`
	PromptInjectedValues []string           `json:"promptInjectedValues"`
}

// ModelOptionValue is one choice of a select option.
type ModelOptionValue struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	IsDefault bool   `json:"isDefault"`
}

// effortOptionIDs are the ids providers give their effort control, in the
// order T3's own client looks for them; a descriptor with the reasoning role
// is one too.
var effortOptionIDs = []string{"reasoningEffort", "effort", "reasoning", "variant", "reasoning_effort", "thinking_level"}

// EffortOption returns the model's reasoning effort selector, if it has one.
func (c ModelCapabilities) EffortOption() (ModelOptionDescriptor, bool) {
	for _, d := range c.OptionDescriptors {
		if d.Type == "select" && d.Role == "reasoning" {
			return d, true
		}
	}
	for _, id := range effortOptionIDs {
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

// defaultValue is the level the model runs with when none is chosen: the
// descriptor's currentValue (Codex) or its isDefault option (Claude).
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

// Effort is the selection's effort level for a model with capabilities:
// the level chosen in its options, else the model's default. Only the
// model's own effort option counts, so a value left over from another
// provider's model is not shown; without the model's capabilities the
// level is unknown and Effort is "".
func (m *ModelSelection) Effort(capabilities ModelCapabilities) string {
	d, ok := capabilities.EffortOption()
	if m == nil || !ok {
		return ""
	}
	opts, err := m.parseOptions()
	if err != nil {
		return d.defaultValue()
	}
	if v := opts.stringOption(d.ID); v != "" {
		return v
	}
	return d.defaultValue()
}

// Same reports whether two selections name the same model with the same
// options, whatever form or order the options came in.
func (m *ModelSelection) Same(other *ModelSelection) bool {
	if m == nil || other == nil {
		return m == other
	}
	if m.InstanceID != other.InstanceID || m.Model != other.Model {
		return false
	}
	a, errA := m.parseOptions()
	b, errB := other.parseOptions()
	if errA != nil || errB != nil {
		return false
	}
	return a.normalized() == b.normalized()
}

// WithEffort is the selection with its effort option, described by d, set to
// level. All other options are kept, in the form the selection holds them.
func (m *ModelSelection) WithEffort(d ModelOptionDescriptor, level string) (ModelSelection, error) {
	if m == nil {
		return ModelSelection{}, errors.New("no model selected")
	}
	value := ""
	for _, o := range d.SelectableValues() {
		if strings.EqualFold(o.ID, level) {
			value = o.ID
			break
		}
	}
	if value == "" {
		return *m, fmt.Errorf("unsupported effort %q", level)
	}
	opts, err := m.parseOptions()
	if err != nil {
		return *m, err
	}
	encodedValue, err := json.Marshal(value)
	if err != nil {
		return *m, fmt.Errorf("encode effort: %w", err)
	}
	encoded, err := opts.with(d.ID, encodedValue).encode()
	if err != nil {
		return *m, err
	}
	changed := *m
	changed.Options = encoded
	return changed, nil
}

// optionsForm is how a selection serializes its options.
type optionsForm int

const (
	optionsArray  optionsForm = iota // [{"id":…,"value":…}], what current servers send
	optionsObject                    // {"id": value}, what older servers send
)

// modelOption is one option of a selection.
type modelOption struct {
	ID    string          `json:"id"`
	Value json.RawMessage `json:"value"`
}

// modelOptions are a selection's options and the form they came in.
type modelOptions struct {
	form   optionsForm
	values []modelOption
}

// parseOptions reads the selection's options from either form; none at all
// read as an empty array.
func (m *ModelSelection) parseOptions() (modelOptions, error) {
	if len(m.Options) == 0 || string(m.Options) == "null" {
		return modelOptions{form: optionsArray}, nil
	}
	var values []modelOption
	if json.Unmarshal(m.Options, &values) == nil {
		return modelOptions{form: optionsArray, values: values}, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(m.Options, &object); err != nil {
		return modelOptions{}, fmt.Errorf("read model options: %w", err)
	}
	for _, id := range slices.Sorted(maps.Keys(object)) {
		values = append(values, modelOption{ID: id, Value: object[id]})
	}
	return modelOptions{form: optionsObject, values: values}, nil
}

// stringOption is the string value of option id, "" when it is unset or not
// a string.
func (o modelOptions) stringOption(id string) string {
	for _, v := range o.values {
		var s string
		if v.ID == id && json.Unmarshal(v.Value, &s) == nil {
			return s
		}
	}
	return ""
}

// with is o with option id set to value, replacing any earlier value.
func (o modelOptions) with(id string, value json.RawMessage) modelOptions {
	values := slices.DeleteFunc(slices.Clone(o.values), func(v modelOption) bool {
		return v.ID == id
	})
	return modelOptions{form: o.form, values: append(values, modelOption{ID: id, Value: value})}
}

// normalized is the options as one comparable string: compact values,
// sorted by id.
func (o modelOptions) normalized() string {
	parts := make([]string, 0, len(o.values))
	for _, v := range o.values {
		var buf bytes.Buffer
		if json.Compact(&buf, v.Value) != nil {
			buf.Reset()
			buf.Write(v.Value)
		}
		parts = append(parts, v.ID+"="+buf.String())
	}
	slices.Sort(parts)
	return strings.Join(parts, "\x00")
}

// encode writes the options back in the form they were read in.
func (o modelOptions) encode() (json.RawMessage, error) {
	var data []byte
	var err error
	switch o.form {
	case optionsObject:
		object := make(map[string]json.RawMessage, len(o.values))
		for _, v := range o.values {
			object[v.ID] = v.Value
		}
		data, err = json.Marshal(object)
	default:
		data, err = json.Marshal(o.values)
	}
	if err != nil {
		return nil, fmt.Errorf("encode model options: %w", err)
	}
	return data, nil
}
