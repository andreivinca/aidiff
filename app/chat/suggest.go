package chat

import (
	"cmp"
	"slices"
	"strings"
)

// maxSuggestionRows bounds the popup under the transcript.
const maxSuggestionRows = 6

// agentRank puts the agent's own commands after the pane's and the
// backend's.
const agentRank = 10

// suggestionsLocked lists what the typed slash text could become: commands
// for a bare word, then a command's argument choices, the exact one first;
// mu must be held.
func (p *Pane) suggestionsLocked() []Suggestion {
	text := p.input.text()
	if p.sugHidden || !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		return nil
	}
	word, rest, hasArg := strings.Cut(text, " ")
	if !hasArg {
		return p.commandSuggestionsLocked(strings.ToLower(word))
	}
	c, ok := p.findCommandLocked(strings.ToLower(word))
	if !ok || c.Complete == nil {
		return nil
	}
	prefix := strings.TrimSpace(rest)
	return exactFirst(c.Complete(prefix), prefix)
}

// rankedSuggestion is a command's popup row and how well it matched.
type rankedSuggestion struct {
	Suggestion
	rank int // lower first
}

// commandSuggestionsLocked lists the commands a typed prefix could mean,
// best matches first. A command whose name an earlier one took is left out,
// so the pane's own wins; mu must be held.
func (p *Pane) commandSuggestionsLocked(prefix string) []Suggestion {
	var ranked []rankedSuggestion
	seen := map[string]bool{}
	for _, c := range p.commandsLocked() {
		if seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		for _, a := range c.Aliases {
			seen[a] = true
		}
		if s, ok := c.suggestion(prefix); ok {
			ranked = append(ranked, s)
		}
	}
	slices.SortStableFunc(ranked, func(a, b rankedSuggestion) int {
		return cmp.Compare(a.rank, b.rank)
	})
	out := make([]Suggestion, 0, len(ranked))
	for _, s := range ranked {
		out = append(out, s.Suggestion)
	}
	return out
}

// suggestion is the popup row of the command when prefix matches it.
func (c command) suggestion(prefix string) (rankedSuggestion, bool) {
	rank := rankMatch(c.Name, c.Aliases, c.Description, prefix)
	if rank < 0 {
		return rankedSuggestion{}, false
	}
	if c.agent() {
		rank += agentRank
	}
	s := Suggestion{Insert: c.Name, Label: c.Name, Description: c.Description, Runs: c.Args == ""}
	if c.Args != "" {
		s.Insert = c.Name + " "
		s.Label = c.Name + " " + c.Args
	}
	return rankedSuggestion{Suggestion: s, rank: rank}, true
}

// rankMatch scores how a typed prefix fits a command: the name exactly (0),
// at the start (1), an alias at the start (2), anywhere past the slash (3),
// in the description (4); -1 when it does not.
func rankMatch(name string, aliases []string, desc, prefix string) int {
	switch {
	case prefix == name:
		return 0
	case strings.HasPrefix(name, prefix):
		return 1
	}
	for _, a := range aliases {
		if strings.HasPrefix(a, prefix) {
			return 2
		}
	}
	q := strings.TrimPrefix(prefix, "/")
	switch {
	case q == "":
		return 1
	case strings.Contains(name, q):
		return 3
	case len(q) >= 3 && strings.Contains(strings.ToLower(desc), q):
		return 4 // a description match needs a few letters, or "st" would mean "request"
	}
	return -1
}

// applySuggestionLocked completes the input with the selected suggestion.
// With enter it also runs a completed command. It reports whether a
// suggestion took the key; mu must be held.
func (p *Pane) applySuggestionLocked(enter bool) bool {
	sugs := p.suggestionsLocked()
	if len(sugs) == 0 {
		return false
	}
	typed := strings.TrimSpace(p.input.text())
	for _, s := range sugs {
		if s.Insert != "" && strings.TrimSpace(s.Insert) == typed {
			if enter {
				p.submitLocked() // what was typed is exactly a choice: run it
			}
			return true
		}
	}
	// The input as typed, not trimmed: "/effort" lists commands, and enter
	// runs that exact one; "/effort " lists its arguments, and enter takes
	// the one selected.
	if word, _, hasArg := strings.Cut(p.input.text(), " "); enter && !hasArg {
		if c, ok := p.findCommandLocked(strings.ToLower(word)); ok && (c.Args == "" || c.OptionalArg) {
			p.submitLocked() // an exact command or alias runs, whatever else matches
			return true
		}
	}
	s := sugs[min(p.sug, len(sugs)-1)]
	if s.Insert == "" {
		return false
	}
	p.input.clear()
	p.insertLocked(s.Insert)
	if enter && s.Runs {
		p.submitLocked()
	}
	return true
}

// exactFirst moves the suggestion whose label is exactly what was typed to
// the top, so enter picks it over longer matches.
func exactFirst(sugs []Suggestion, typed string) []Suggestion {
	typed = strings.TrimSpace(typed)
	for i, s := range sugs {
		if i > 0 && strings.EqualFold(s.Label, typed) {
			return append([]Suggestion{s}, append(sugs[:i:i], sugs[i+1:]...)...)
		}
	}
	return sugs
}

// moveSuggestionLocked moves the popup's selection by delta, wrapping; it
// reports whether there was a popup; mu must be held.
func (p *Pane) moveSuggestionLocked(delta int) bool {
	n := len(p.suggestionsLocked())
	if n == 0 {
		return false
	}
	p.sug = ((min(p.sug, n-1)+delta)%n + n) % n
	return true
}

// inputChanged resets the popup after an edit; mu must be held.
func (p *Pane) inputChanged() {
	p.sug = 0
	p.sugHidden = false
}
