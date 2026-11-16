package t3chat

import (
	"errors"
	"fmt"
	"strings"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// modelCmd switches the model, or reports the current one.
func (b *Backend) modelCmd(arg string) (chat.Reply, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return chat.Reply{Note: "model: " + b.currentModelLocked() + " · /model <name> switches it"}, nil
	}
	cat, problem := b.catalogLocked()
	if problem != "" {
		b.retryCatalogLocked()
		return chat.Reply{}, errors.New(problem)
	}
	choice, err := pickModel(cat.Models, arg)
	if err != nil {
		return chat.Reply{}, err
	}
	return b.dispatch("model", t3client.ModelSelectionCommand(b.threadID, choice.Selection())), nil
}

// pickModel finds the model arg names: a slug, "instance:slug", or a unique
// part of a slug or name.
func pickModel(models []t3client.ModelChoice, arg string) (t3client.ModelChoice, error) {
	q := strings.ToLower(arg)
	var partial []t3client.ModelChoice
	for _, m := range models {
		slug := strings.ToLower(m.Slug)
		if q == slug || q == strings.ToLower(m.InstanceID)+":"+slug {
			return m, nil
		}
		if strings.Contains(slug, q) || strings.Contains(strings.ToLower(m.Name), q) {
			partial = append(partial, m)
		}
	}
	switch len(partial) {
	case 0:
		return t3client.ModelChoice{}, errors.New("no model matches " + arg + "; /model and tab lists them")
	case 1:
		return partial[0], nil
	}
	return t3client.ModelChoice{}, fmt.Errorf("%d models match %s; be more specific", len(partial), arg)
}

// modelSuggestions lists the server's models matching the typed text.
func (b *Backend) modelSuggestions(prefix string) []chat.Suggestion {
	b.mu.Lock()
	defer b.mu.Unlock()
	cat, problem := b.catalogLocked()
	if problem != "" {
		return []chat.Suggestion{{Label: problem}}
	}
	q := strings.ToLower(prefix)
	current := b.thread.model
	var out []chat.Suggestion
	for _, m := range cat.Models {
		if q != "" && !strings.Contains(strings.ToLower(m.Slug+" "+m.Name+" "+m.Provider), q) {
			continue
		}
		desc := m.Name + " · " + m.Provider
		if current != nil && current.Model == m.Slug && current.InstanceID == m.InstanceID {
			desc += " · current"
		}
		out = append(out, chat.Suggestion{Insert: "/model " + m.Slug, Label: m.Slug, Description: desc, Runs: true})
	}
	if len(out) == 0 {
		return []chat.Suggestion{{Label: "no model matches " + prefix}}
	}
	return out
}

// currentModelLocked names the thread's model; mu must be held.
func (b *Backend) currentModelLocked() string {
	current := b.thread.model
	if current == nil || current.Model == "" {
		return "unknown"
	}
	if m, ok := b.modelLocked(); ok {
		return m.Slug + " (" + m.Provider + ")"
	}
	return current.Model
}

// modelLocked is the catalog's entry for the thread's model, if both are
// known; mu must be held.
func (b *Backend) modelLocked() (t3client.ModelChoice, bool) {
	current := b.thread.model
	cat, _ := b.catalog.get()
	if current == nil || cat == nil {
		return t3client.ModelChoice{}, false
	}
	for _, m := range cat.Models {
		if m.Slug == current.Model && m.InstanceID == current.InstanceID {
			return m, true
		}
	}
	return t3client.ModelChoice{}, false
}
