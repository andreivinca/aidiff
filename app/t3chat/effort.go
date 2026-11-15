package t3chat

import (
	"strings"

	"github.com/umputun/revdiff/app/t3client"
)

func (p *Pane) effortOptionLocked() (t3client.ModelOptionDescriptor, string) {
	if p.current == nil {
		return t3client.ModelOptionDescriptor{}, "the thread's model is not available yet"
	}
	p.ensureCatalogLocked()
	if p.catalog == nil {
		return t3client.ModelOptionDescriptor{}, "the model list is still loading; try again in a moment"
	}
	for _, m := range p.catalog.Models {
		if m.InstanceID == p.current.InstanceID && m.Slug == p.current.Model {
			if d, ok := m.Capabilities.EffortOption(); ok && len(d.SelectableValues()) > 0 {
				return d, ""
			}
		}
	}
	return t3client.ModelOptionDescriptor{}, "this model does not advertise adjustable effort"
}

func (p *Pane) effortCmd(arg string) {
	d, problem := p.effortOptionLocked()
	if problem != "" {
		p.note = problem
		return
	}
	var levels []string
	for _, o := range d.SelectableValues() {
		levels = append(levels, o.ID)
	}
	if strings.TrimSpace(arg) == "" {
		current := p.composerEffortLocked()
		if current == "" {
			current = "unknown"
		}
		p.note = "effort: " + current + " · /effort " + strings.Join(levels, " | ")
		return
	}
	selection, err := p.current.WithEffort(d, strings.TrimSpace(arg))
	if err != nil {
		p.note = err.Error() + "; supported: " + strings.Join(levels, ", ")
		return
	}
	p.dispatchLocked(t3client.ModelSelectionCommand(p.threadID, selection), "effort")
}

func (p *Pane) effortSuggestions(prefix string) []suggestion {
	d, problem := p.effortOptionLocked()
	if problem != "" {
		return []suggestion{{label: problem}}
	}
	var out []suggestion
	for _, o := range d.SelectableValues() {
		if !strings.HasPrefix(strings.ToLower(o.ID), strings.ToLower(prefix)) {
			continue
		}
		desc := o.Label
		if o.ID == p.composerEffortLocked() {
			desc += " · current"
		}
		out = append(out, suggestion{insert: "/effort " + o.ID, label: o.ID, desc: desc, runs: true})
	}
	if len(out) == 0 {
		return []suggestion{{label: "no supported effort matches " + prefix}}
	}
	return exactFirst(out, prefix)
}
