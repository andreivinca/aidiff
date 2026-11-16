package t3chat

import (
	"errors"
	"fmt"
	"strings"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// effortOptionLocked is the effort option of the thread's model, or why
// there is none; mu must be held.
func (b *Backend) effortOptionLocked() (t3client.ModelOptionDescriptor, string) {
	if b.selectionLocked() == nil {
		return t3client.ModelOptionDescriptor{}, "the thread's model is not available yet"
	}
	if _, problem := b.catalogLocked(); problem != "" {
		return t3client.ModelOptionDescriptor{}, problem
	}
	if m, ok := b.modelLocked(); ok {
		if d, ok := m.Capabilities.EffortOption(); ok && len(d.SelectableValues()) > 0 {
			return d, ""
		}
	}
	return t3client.ModelOptionDescriptor{}, "this model does not advertise adjustable effort"
}

func (b *Backend) effortCmd(arg string) (chat.Reply, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, problem := b.effortOptionLocked()
	if problem != "" {
		b.retryCatalogLocked()
		return chat.Reply{}, errors.New(problem)
	}
	var levels []string
	for _, o := range d.SelectableValues() {
		levels = append(levels, o.ID)
	}
	if strings.TrimSpace(arg) == "" {
		current := b.effortLocked()
		if current == "" {
			current = "unknown"
		}
		return chat.Reply{Note: "effort: " + current + " · /effort " + strings.Join(levels, " | ")}, nil
	}
	selection, err := b.selectionLocked().WithEffort(d, strings.TrimSpace(arg))
	if err != nil {
		return chat.Reply{}, fmt.Errorf("%w; supported: %s", err, strings.Join(levels, ", "))
	}
	level := selection.Effort(t3client.ModelCapabilities{OptionDescriptors: []t3client.ModelOptionDescriptor{d}})
	return b.choose("effort", selection, "/effort "+level, "effort: "+level), nil
}

func (b *Backend) effortSuggestions(prefix string) []chat.Suggestion {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, problem := b.effortOptionLocked()
	if problem != "" {
		return []chat.Suggestion{{Label: problem}}
	}
	var out []chat.Suggestion
	for _, o := range d.SelectableValues() {
		if !strings.HasPrefix(strings.ToLower(o.ID), strings.ToLower(prefix)) {
			continue
		}
		desc := o.Label
		if o.ID == b.effortLocked() {
			desc += " · current"
		}
		out = append(out, chat.Suggestion{Insert: "/effort " + o.ID, Label: o.ID, Description: desc, Runs: true})
	}
	if len(out) == 0 {
		return []chat.Suggestion{{Label: "no supported effort matches " + prefix}}
	}
	return out
}

// effortLocked is the effort the thread's model runs with, by its
// provider's defaults; mu must be held.
func (b *Backend) effortLocked() string {
	m, _ := b.modelLocked()
	return b.selectionLocked().Effort(m.Capabilities)
}
