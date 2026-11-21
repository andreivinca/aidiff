package t3chat

import (
	"fmt"
	"strings"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// entry is a transcript item as the pane shows it: the conversation in
// full, the agent's work as steps.
func (t *thread) entry(it t3client.TurnItem) chat.Entry {
	switch it.Type {
	case "user_message":
		return chat.UserMessage{Text: it.Text}
	case "assistant_message":
		return chat.AgentMessage{Markdown: it.Text, Streaming: it.Streaming || it.Status == "running"}
	case "proposed_plan":
		return chat.Plan{Markdown: it.Markdown}
	case "todo_list":
		return todoList(it)
	case "approval_request":
		return chat.Approval{ID: it.RequestID, Prompt: it.Prompt, Reason: it.Reason, Pending: t.awaits(it)}
	case "user_input_request":
		return chat.Questions{ID: it.RequestID, Questions: questions(it.Questions), Pending: t.awaits(it)}
	case "error":
		return chat.Failure{Text: it.FailureText()}
	case "secret_request":
		return secretRequest(it)
	}
	return stepOf(it, t.texts(it).in)
}

// secretRequest is a secret_request item as the pane shows it: pending
// while T3 still waits for the secret.
func secretRequest(it t3client.TurnItem) chat.SecretRequest {
	label := it.Label
	if label == "" && it.Title != nil {
		label = *it.Title
	}
	return chat.SecretRequest{ID: it.ID, Label: label, Reason: it.Reason, URL: it.URL, Placeholder: it.Placeholder,
		Status: it.SecretStatus, Pending: secretPending(it)}
}

// secretPending reports whether T3 still waits for the secret of it.
func secretPending(it t3client.TurnItem) bool {
	return it.Type == "secret_request" && !it.Done() && (it.SecretStatus == "" || it.SecretStatus == "pending")
}

func todoList(it t3client.TurnItem) chat.TodoList {
	steps := make([]chat.TodoStep, 0, len(it.Steps))
	for _, s := range it.Steps {
		state := chat.Pending
		switch s.Status {
		case "completed":
			state = chat.Done
		case "running":
			state = chat.Running
		}
		steps = append(steps, chat.TodoStep{Text: s.Text, State: state})
	}
	return chat.TodoList{Steps: steps}
}

func questions(qs []t3client.Question) []chat.Question {
	out := make([]chat.Question, 0, len(qs))
	for _, q := range qs {
		options := make([]chat.Option, 0, len(q.Options))
		for _, o := range q.Options {
			options = append(options, chat.Option{Label: o.Label, Description: o.Description, Value: o.Answer()})
		}
		out = append(out, chat.Question{Header: q.Header, ID: q.ID, Text: q.Question, Options: options,
			Multiple: q.MultiSelect, OwnWords: q.AllowCustomAnswer})
	}
	return out
}

// step is one of the agent's work items as a step of the pane's.
func step(it t3client.TurnItem) chat.Step {
	return stepOf(it, it.InputText())
}

// stepOf is step, with the item's input already read as text.
func stepOf(it t3client.TurnItem, input string) chat.Step {
	s := chat.Step{Kind: chat.StepOther}
	s.State, s.Why = stepState(it)
	switch it.Type {
	case "reasoning":
		s.Kind, s.Text = chat.StepThought, it.Text
		if it.Streaming && s.State == chat.Done {
			s.State = chat.Running
		}
	case "command_execution":
		s.Kind, s.Text = chat.StepCommand, input
	case "file_change":
		s.Kind, s.Text = chat.StepEdit, it.FileName
		if it.Additions != nil || it.Deletions != nil {
			s.Text += fmt.Sprintf(" +%d −%d", count(it.Additions), count(it.Deletions))
		}
	case "file_search":
		s.Kind, s.Text = chat.StepSearch, it.Pattern
	case "web_search":
		s.Kind, s.Text = chat.StepWeb, strings.Join(it.Patterns, ", ")
	case "dynamic_tool":
		s.Kind, s.Text = chat.StepTool, toolName(it)+" "+firstLine(input)
	case "subagent":
		s.Kind, s.Text = chat.StepSubagent, it.Prompt
	case "system_notice":
		s.Kind, s.Text = chat.StepNotice, it.Message
	case "run_interrupt_request", "run_interrupt_result":
		s.Kind, s.Text = chat.StepInterrupt, it.Message
	case "compaction":
		s.Kind, s.Text = chat.StepCompaction, "context compacted"
	default:
		s.Text = stepLabel(it)
	}
	return s
}

// stepState is where a work item stands, and why it failed when it did.
func stepState(it t3client.TurnItem) (chat.State, string) {
	switch {
	case it.Status == "running":
		return chat.Running, ""
	case it.Status == "pending":
		return chat.Pending, ""
	case it.Status == "failed":
		return chat.Failed, ""
	case it.ExitCode != nil && *it.ExitCode != 0:
		return chat.Failed, fmt.Sprintf("exit %d", *it.ExitCode)
	case it.Done() || it.Status == "idle":
		return chat.Done, ""
	}
	return chat.Waiting, "" // open in another way, such as waiting
}

// stepLabel names an item of a type the pane has no glyph for: its type,
// with its title.
func stepLabel(it t3client.TurnItem) string {
	label := strings.ReplaceAll(it.Type, "_", " ")
	if it.Title != nil && *it.Title != "" {
		label += ": " + *it.Title
	}
	return label
}

// toolName is the name of a dynamic tool call, "tool" when it has none.
func toolName(it t3client.TurnItem) string {
	if it.ToolName != nil && *it.ToolName != "" {
		return *it.ToolName
	}
	return "tool"
}

func count(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

// firstLine is the first line of s, trimmed.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
