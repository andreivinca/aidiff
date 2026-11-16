package t3chat

import (
	"strings"

	"github.com/umputun/revdiff/app/chat"
)

// settingsLocked is how the thread runs, for the composer: build or plan,
// its model and effort, then its provider, its permission level and a
// fresh context armed by /clear. mu must be held.
func (b *Backend) settingsLocked() chat.Settings {
	t := &b.thread
	s := chat.Settings{Mode: "Build"}
	if t.interaction == "plan" {
		s.Mode, s.Attention = "Plan", true
	}
	model, provider := b.modelNamesLocked()
	if effort := b.effortLocked(); effort != "" {
		model += " · " + effort
	}
	s.Model = model
	if provider != "" {
		s.Details = append(s.Details, provider)
	}
	if t.runtimeMode != "" {
		s.Details = append(s.Details, strings.ReplaceAll(t.runtimeMode, "-", " "))
	}
	if b.fresh {
		s.Details = append(s.Details, "fresh context")
	}
	return s
}

// modelNamesLocked names the thread's model and provider as the catalog
// does, without guessing from a slug; mu must be held.
func (b *Backend) modelNamesLocked() (model, provider string) {
	current := b.thread.model
	if current == nil || current.Model == "" {
		return "T3 Code", ""
	}
	m, ok := b.modelLocked()
	if !ok {
		return current.Model, current.InstanceID
	}
	if m.Name != "" {
		return m.Name, m.Provider
	}
	return current.Model, m.Provider
}
