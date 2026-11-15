package t3client

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

// Run is one agent turn of a thread.
type Run struct {
	ID            string `json:"id"`
	ThreadID      string `json:"threadId"`
	Ordinal       int    `json:"ordinal"`
	Status        string `json:"status"` // one of T3's run states; see Active
	UserMessageID string `json:"userMessageId"`
}

// Active reports whether the run is still going.
func (r Run) Active() bool {
	switch r.Status {
	case "preparing", "queued", "starting", "running", "waiting":
		return true
	}
	return false
}

// RuntimeRequest is something the agent asked the user: an approval or a question.
type RuntimeRequest struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`   // command, file-read, file-change, mcp-elicitation, permission, dynamic_tool_call, user_input, auth_refresh
	Status string `json:"status"` // pending until answered, expired or withdrawn; see Pending
}

// Pending reports whether the request still waits for an answer.
func (r RuntimeRequest) Pending() bool { return r.Status == "pending" }

// RequestOption is one answer an approval request offers.
type RequestOption struct {
	Decision string `json:"decision"`
	Label    string `json:"label"`
	Warning  string `json:"warning,omitempty"`
}

// QuestionOption is one choice of a question.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Value       string `json:"value"` // what choosing it sends; the label when empty
}

// Answer is what choosing the option sends.
func (o QuestionOption) Answer() string {
	if o.Value != "" {
		return o.Value
	}
	return o.Label
}

// Question is one question of a user_input_request item.
type Question struct {
	ID                string           `json:"id"`
	Header            string           `json:"header"`
	Question          string           `json:"question"`
	Options           []QuestionOption `json:"options"`
	MultiSelect       bool             `json:"multiSelect"`
	AllowCustomAnswer bool             `json:"allowCustomAnswer"`
}

// TodoStep is one step of a todo_list item.
type TodoStep struct {
	Text   string `json:"text"`
	Status string `json:"status"` // pending, running, completed
}

// TurnItem is one entry of a thread's transcript. Type picks which of the
// optional fields the server fills; an unknown type still decodes its base
// fields. Large bodies (command output, tool output) are left out on the wire
// and OutputOmitted is set.
type TurnItem struct {
	ID            string       `json:"id"`
	RunID         *string      `json:"runId"`
	Ordinal       int          `json:"ordinal"`
	Type          string       `json:"type"`
	Status        string       `json:"status"` // idle, pending, running or waiting while open; see Done for the final states
	Title         *string      `json:"title"`
	UpdatedAt     string       `json:"updatedAt"`
	StartedAt     string       `json:"startedAt"`
	NativeItemRef *ProviderRef `json:"nativeItemRef"`

	Text      string `json:"text"`      // user_message, assistant_message, reasoning
	Streaming bool   `json:"streaming"` // assistant_message, reasoning, proposed_plan
	Markdown  string `json:"markdown"`  // proposed_plan
	Message   string `json:"message"`   // system_notice, run_interrupt_request, run_interrupt_result
	Summary   string `json:"summary"`   // compaction

	Input         json.RawMessage `json:"input"`  // command_execution: the command; dynamic_tool: the arguments
	Output        json.RawMessage `json:"output"` // stripped on the wire when large
	OutputOmitted bool            `json:"outputOmitted"`
	ExitCode      *int            `json:"exitCode"`
	ToolName      *string         `json:"toolName"`
	Monitor       bool            `json:"monitor"`
	SubagentID    string          `json:"subagentId"`
	ChildThreadID string          `json:"childThreadId"`
	Progress      string          `json:"progress"`
	Result        string          `json:"result"`
	PromptOmitted bool            `json:"promptOmitted"`
	ResultOmitted bool            `json:"resultOmitted"`

	FileName  string   `json:"fileName"` // file_change
	Additions *int     `json:"additions"`
	Deletions *int     `json:"deletions"`
	Pattern   string   `json:"pattern"`  // file_search
	Patterns  []string `json:"patterns"` // web_search

	Prompt    string          `json:"prompt"` // approval_request, subagent
	Reason    string          `json:"reason"`
	RequestID string          `json:"requestId"` // approval_request, user_input_request
	Options   []RequestOption `json:"options"`
	Questions []Question      `json:"questions"`
	Steps     []TodoStep      `json:"steps"`
	Failure   json.RawMessage `json:"failure"` // error
}

// Done reports whether the item reached a final state.
func (t TurnItem) Done() bool {
	switch t.Status {
	case "completed", "failed", "cancelled", "interrupted": //nolint:misspell // T3's spelling
		return true
	}
	return false
}

// InputText is the item's input as text: a command, or a tool's arguments
// as compact JSON.
func (t TurnItem) InputText() string {
	var s string
	if json.Unmarshal(t.Input, &s) == nil {
		return s
	}
	return compactJSON(t.Input)
}

// FailureText is an error item's message.
func (t TurnItem) FailureText() string {
	var f struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(t.Failure, &f) == nil && f.Message != "" {
		return f.Message
	}
	return compactJSON(t.Failure)
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

// ProjectedTurnItem places a turn item in the transcript.
type ProjectedTurnItem struct {
	Position int      `json:"position"`
	Item     TurnItem `json:"item"`
}

// ThreadProjection is the part of a thread's full state aidiff renders.
type ThreadProjection struct {
	Thread           Thread              `json:"thread"`
	Runs             []Run               `json:"runs"`
	RuntimeRequests  []RuntimeRequest    `json:"runtimeRequests"`
	VisibleTurnItems []ProjectedTurnItem `json:"visibleTurnItems"`
	TurnItems        []TurnItem          `json:"turnItems"`
	Subagents        []Subagent          `json:"subagents"`
	ProviderThreads  []ProviderThread    `json:"providerThreads"`
}

// ThreadEvents receives what SubscribeThread decodes. Handlers run on the
// connection's read goroutine and must not block. The server re-sends whole
// entities, so a streaming reply arrives as the same item with more text.
type ThreadEvents struct {
	Snapshot       func(ThreadProjection)
	TurnItem       func(TurnItem)
	Run            func(Run)
	Request        func(RuntimeRequest)
	Thread         func(Thread)
	Subagent       func(Subagent)
	ProviderThread func(ProviderThread)
}

type threadStreamItem struct {
	Kind       string            `json:"kind"` // snapshot, event, unknown-event, synchronized
	Projection *ThreadProjection `json:"projection"`
	Event      *struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	} `json:"event"`
}

// SubscribeThread streams a thread's transcript: a snapshot, then every
// update, until ctx ends or the connection drops.
func SubscribeThread(ctx context.Context, c *Conn, threadID string, h ThreadEvents) error {
	payload := map[string]any{"threadId": threadID}
	return c.Subscribe(ctx, "orchestration.subscribeThread", payload, func(v json.RawMessage) {
		var it threadStreamItem
		if json.Unmarshal(v, &it) != nil {
			return
		}
		switch {
		case it.Kind == "snapshot" && it.Projection != nil:
			if h.Snapshot != nil {
				h.Snapshot(*it.Projection)
			}
		case it.Kind == "event" && it.Event != nil:
			h.dispatch(it.Event.Type, it.Event.Payload)
		}
	})
}

// dispatch decodes one domain event for the handler that wants it.
func (h ThreadEvents) dispatch(typ string, payload json.RawMessage) {
	if h.dispatchActivity(typ, payload) {
		return
	}
	switch {
	case typ == "turn-item.updated" && h.TurnItem != nil:
		var t TurnItem
		if json.Unmarshal(payload, &t) == nil && t.ID != "" {
			h.TurnItem(t)
		}
	case (typ == "run.created" || typ == "run.updated") && h.Run != nil:
		var r Run
		if json.Unmarshal(payload, &r) == nil && r.ID != "" {
			h.Run(r)
		}
	case typ == "runtime-request.updated" && h.Request != nil:
		var r RuntimeRequest
		if json.Unmarshal(payload, &r) == nil && r.ID != "" {
			h.Request(r)
		}
	case strings.HasPrefix(typ, "thread.") && h.Thread != nil:
		var t Thread
		if json.Unmarshal(payload, &t) == nil && t.ID != "" {
			h.Thread(t)
		}
	}
}

// RespondCommand answers a pending approval. decision is accept,
// acceptForSession, acceptAlways, decline or cancel.
func RespondCommand(threadID, requestID, decision string) Command {
	return Command{"type": "runtime-request.respond", "threadId": threadID, "requestId": requestID, "decision": decision}
}

// RespondRequest answers a pending approval; see RespondCommand.
func RespondRequest(ctx context.Context, c *Conn, threadID, requestID, decision string) error {
	return Dispatch(ctx, c, RespondCommand(threadID, requestID, decision))
}

// AnswerCommand answers the questions of a user input request: one value per
// question id, a string, or a list of strings for a multi-select question.
func AnswerCommand(threadID, requestID string, answers map[string]any) Command {
	return Command{"type": "runtime-request.respond", "threadId": threadID, "requestId": requestID, "answers": answers}
}

// DismissQuestionCommand drops a user input request without answering it.
func DismissQuestionCommand(threadID, requestID string) Command {
	return Command{"type": "thread.user-input.dismiss", "threadId": threadID, "requestId": requestID}
}

// InterruptCommand stops the thread's run.
func InterruptCommand(threadID, runID string) Command {
	return Command{"type": "run.interrupt", "threadId": threadID, "runId": runID}
}

// InteractionModeCommand switches the thread between "default" (build) and
// "plan".
func InteractionModeCommand(threadID, mode string) Command {
	return Command{"type": "thread.interaction-mode.set", "threadId": threadID, "interactionMode": mode}
}

// NewThreadID mints an id for a thread the client creates, such as a fork.
func NewThreadID() string { return newID() }

// SideChatCommand forks the thread at its latest stable point into a side
// chat, a place for a question that must not derail the main work.
func SideChatCommand(sourceThreadID, newThreadID string) Command {
	return Command{
		"type":           "thread.fork",
		"sourceThreadId": sourceThreadID,
		"targetThreadId": newThreadID,
		"sourcePoint":    map[string]any{"type": "latest_stable"},
		"sideChat":       true,
		"title":          "Side chat",
	}
}

// ModelSelectionCommand switches the thread to another model.
func ModelSelectionCommand(threadID string, m ModelSelection) Command {
	return Command{"type": "thread.model-selection.set", "threadId": threadID, "modelSelection": m}
}

// RuntimeModeCommand sets what the agent may do on its own: approval-required,
// auto-accept-edits, auto or full-access.
func RuntimeModeCommand(threadID, mode string) Command {
	return Command{"type": "thread.runtime-mode.set", "threadId": threadID, "runtimeMode": mode}
}
