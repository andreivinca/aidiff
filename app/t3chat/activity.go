package t3chat

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// activityEntry is a piece of background work as the builder joins it.
type activityEntry struct {
	id, kind, title, status, progress, updated string // kind: subagent, process, monitor or task
	ordinal                                    int
	details                                    []string
}

// activityBuilder joins the activity of a thread by the server's ids.
type activityBuilder struct {
	entries map[string]activityEntry
	aliases map[string]string
}

// activity joins T3's subagents, command rows and provider task lists by
// their server ids, most recent first. A command can be completed as a tool
// call while the provider still reports the process it launched as
// background work; items of rolled-back runs are left out, and a subagent
// tracked on its own wins over its transcript row, so nothing is counted
// twice.
func (t *thread) activity() []chat.Activity {
	b := activityBuilder{entries: map[string]activityEntry{}, aliases: map[string]string{}}
	for _, a := range t.subagents {
		if a.RunID != nil && t.runs[*a.RunID].Status == "rolled_back" {
			continue
		}
		b.agent(a)
	}
	for _, it := range t.allItems {
		if it.RunID != nil && t.runs[*it.RunID].Status == "rolled_back" {
			continue
		}
		b.item(it)
	}
	for _, provider := range t.providers {
		if t.providerID != "" && provider.ID != t.providerID {
			continue
		}
		for _, task := range provider.PendingBackgroundTasks {
			b.background(provider.ID, task)
		}
	}
	entries := slices.Collect(maps.Values(b.entries))
	slices.SortFunc(entries, activityEntry.compare)
	out := make([]chat.Activity, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.activity())
	}
	return out
}

// compare orders the activity list by its latest update, newest first.
func (e activityEntry) compare(other activityEntry) int {
	return cmp.Or(
		strings.Compare(other.updated, e.updated),
		cmp.Compare(other.ordinal, e.ordinal),
		strings.Compare(e.id, other.id),
	)
}

// activity is the entry as the pane lists it.
func (e activityEntry) activity() chat.Activity {
	title := e.title
	if title == "" {
		title = e.kind
	}
	if e.kind == "monitor" {
		title = "monitor · " + title
	}
	return chat.Activity{ID: e.id, Agent: e.kind == "subagent", Title: title, State: activityState(e.status),
		Status: e.status, Progress: e.progress, Details: e.details}
}

// activityState is where work in T3's status stands.
func activityState(status string) chat.State {
	switch status {
	case "pending":
		return chat.Pending
	case "running":
		return chat.Running
	case "waiting":
		return chat.Waiting
	case "failed":
		return chat.Failed
	}
	return chat.Done
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

// activityOutput is what a command printed, as far as T3 reported it.
func activityOutput(it t3client.TurnItem) string {
	if it.OutputOmitted {
		return "Output is not included in the live activity list."
	}
	return t3client.JSONText(it.Output)
}
