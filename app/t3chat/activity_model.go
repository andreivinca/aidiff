package t3chat

import (
	"fmt"
	"slices"
	"strings"

	"github.com/umputun/revdiff/app/t3client"
)

type activityEntry struct {
	id, kind, title, status, progress, updated string
	ordinal                                    int
	details                                    []string
}

func (e activityEntry) active() bool {
	return e.status == "pending" || e.status == "running" || e.status == "waiting"
}

type activityBuilder struct {
	entries map[string]activityEntry
	aliases map[string]string
}

// activityEntriesLocked joins T3's subagents, command rows and provider task
// lists by their server IDs. A command can be completed as a tool call while
// the provider still reports the process it launched as background work.
func (p *Pane) activityEntriesLocked() []activityEntry {
	b := activityBuilder{entries: map[string]activityEntry{}, aliases: map[string]string{}}
	for _, a := range p.subagents {
		if a.RunID != nil && p.runs[*a.RunID].Status == "rolled_back" {
			continue
		}
		b.agent(a)
	}
	for _, it := range p.activityItems {
		if it.RunID != nil && p.runs[*it.RunID].Status == "rolled_back" {
			continue
		}
		b.item(it)
	}
	for _, provider := range p.providers {
		if p.providerID != "" && provider.ID != p.providerID {
			continue
		}
		for _, task := range provider.PendingBackgroundTasks {
			b.background(provider.ID, task)
		}
	}
	out := make([]activityEntry, 0, len(b.entries))
	for _, e := range b.entries {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b activityEntry) int {
		if a.kind == "subagent" && b.kind != "subagent" {
			return -1
		}
		if b.kind == "subagent" && a.kind != "subagent" {
			return 1
		}
		if a.active() != b.active() {
			if a.active() {
				return -1
			}
			return 1
		}
		if a.updated != b.updated {
			return strings.Compare(b.updated, a.updated)
		}
		if a.ordinal != b.ordinal {
			return b.ordinal - a.ordinal
		}
		return strings.Compare(a.id, b.id)
	})
	return out
}

func (b *activityBuilder) alias(id string, keys ...string) {
	for _, key := range keys {
		if key != "" {
			b.aliases[key] = id
		}
	}
}

func (b *activityBuilder) find(keys ...string) (activityEntry, bool) {
	for _, key := range keys {
		if id, ok := b.aliases[key]; ok {
			return b.entries[id], true
		}
	}
	return activityEntry{}, false
}

func (b *activityBuilder) agent(a t3client.Subagent) {
	e := activityEntry{id: "agent:" + a.ID, kind: "subagent", title: a.Title, status: a.Status,
		progress: a.Progress, updated: a.UpdatedAt}
	if e.title == "" {
		e.title = firstLine(a.Prompt)
	}
	e.details = activityDetails(a.Prompt, a.Progress, a.Result)
	if a.Model != "" {
		e.details = append(e.details, "Model: "+a.Model)
	}
	if a.ChildThreadID != "" {
		e.details = append(e.details, "Thread: "+a.ChildThreadID)
		b.alias(e.id, "child:"+a.ChildThreadID)
	}
	b.entries[e.id] = e
	b.alias(e.id, a.ID)
	if a.NativeTaskRef != nil {
		b.alias(e.id, a.NativeTaskRef.NativeID)
	}
}

func (b *activityBuilder) item(it t3client.TurnItem) {
	if it.Type != "command_execution" && it.Type != "subagent" {
		return
	}
	nativeID := ""
	if it.NativeItemRef != nil {
		nativeID = it.NativeItemRef.NativeID
	}
	if it.Type == "subagent" {
		keys := []string{it.SubagentID}
		if it.ChildThreadID != "" {
			keys = append(keys, "child:"+it.ChildThreadID)
		}
		if e, ok := b.find(keys...); ok {
			b.alias(e.id, it.ID, nativeID)
			return // the independently tracked subagent holds the latest progress
		}
	}
	e := activityFromItem(it)
	b.entries[e.id] = e
	b.alias(e.id, it.ID, nativeID)
	if it.ChildThreadID != "" {
		b.alias(e.id, "child:"+it.ChildThreadID)
	}
}

func activityFromItem(it t3client.TurnItem) activityEntry {
	e := activityEntry{id: "item:" + it.ID, kind: "process", status: it.Status, updated: it.UpdatedAt, ordinal: it.Ordinal}
	if it.Title != nil {
		e.title = *it.Title
	}
	if it.Type == "subagent" {
		e.kind = "subagent"
		e.progress = it.Progress
		if e.title == "" {
			e.title = firstLine(it.Prompt)
		}
		e.details = activityDetails(it.Prompt, it.Progress, it.Result)
		return e
	}
	if it.Monitor {
		e.kind = "monitor"
	}
	if e.title == "" {
		e.title = firstLine(it.InputText())
	}
	e.details = activityDetails(it.InputText(), activityOutput(it))
	if it.ExitCode != nil {
		e.details = append(e.details, fmt.Sprintf("Exit code: %d", *it.ExitCode))
	}
	return e
}

func (b *activityBuilder) background(providerID string, task t3client.BackgroundTask) {
	if task.TaskID == "" {
		return
	}
	keys := []string{task.ItemID, task.NativeItemID, task.TaskID}
	if task.ChildThreadID != "" {
		keys = append(keys, "child:"+task.ChildThreadID)
	}
	e, found := b.find(keys...)
	if found && e.kind == "subagent" {
		return
	}
	if !found {
		e = activityEntry{id: "task:" + providerID + ":" + task.TaskID, kind: "task", title: task.Description}
		switch task.Kind {
		case "subagent":
			e.kind = "subagent"
		case "command", "background_command":
			e.kind = "process"
		case "monitor":
			e.kind = "monitor"
		}
		e.details = activityDetails(task.Command, task.Description)
	}
	e.status = "running"
	if task.Awaited {
		e.status = "waiting"
	}
	if e.title == "" {
		e.title = firstLine(task.Command)
	}
	if e.title == "" {
		e.title = task.TaskID
	}
	b.entries[e.id] = e
	b.alias(e.id, task.TaskID, task.ItemID, task.NativeItemID)
}

func activityDetails(parts ...string) []string {
	var out []string
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func activityOutput(it t3client.TurnItem) string {
	if it.OutputOmitted {
		return "Output is not included in the live activity list."
	}
	var output string
	if jsonOutput := it.Output; len(jsonOutput) > 0 {
		output = t3client.TurnItem{Input: jsonOutput}.InputText()
	}
	return output
}

func (p *Pane) onSubagent(id uint64, a t3client.Subagent) {
	p.mu.Lock()
	if id == p.subID {
		previous := p.subagents[a.ID]
		if a.PromptOmitted {
			a.Prompt = previous.Prompt
		}
		if a.ResultOmitted {
			a.Result = previous.Result
		}
		p.subagents[a.ID] = a
		p.activityCounts.valid = false
		p.gen++
	}
	p.mu.Unlock()
	p.signal()
}

func (p *Pane) onProviderThread(id uint64, th t3client.ProviderThread) {
	p.mu.Lock()
	if id == p.subID {
		p.providers[th.ID] = th
		p.activityCounts.valid = false
		p.gen++
	}
	p.mu.Unlock()
	p.signal()
}
