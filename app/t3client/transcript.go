package t3client

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// Run is one agent turn of a thread.
type Run struct {
	ID            string `json:"id"`
	ThreadID      string `json:"threadId"`
	Ordinal       int    `json:"ordinal"`
	Status        string `json:"status"` // one of T3's run states; see Active
	UserMessageID string `json:"userMessageId"`
	StartedAt     string `json:"startedAt"`     // RFC 3339, "" until the run starts
	WorkStartedAt string `json:"workStartedAt"` // when the agent began working, past any preparation; "" when not sent
}

// Since is when the run began working: the agent's start, else the run's,
// zero when the server has not said.
func (r Run) Since() time.Time {
	for _, s := range []string{r.WorkStartedAt, r.StartedAt} {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
	}
	return time.Time{}
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
func (r RuntimeRequest) Pending() bool {
	return r.Status == "pending"
}

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

// QuestionAnswer is the answer a user_input_request got.
type QuestionAnswer struct {
	// Answers maps a question's id to its answer: a string, or a list of
	// them for a question that takes several options
	Answers map[string]json.RawMessage `json:"answers"`
}

// Values is the answer to question id, one value per option picked or the
// text typed; nil when it got none.
func (a *QuestionAnswer) Values(id string) []string {
	if a == nil {
		return nil
	}
	raw, ok := a.Answers[id]
	if !ok {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
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

	MessageID   string `json:"messageId"`
	InputIntent string `json:"inputIntent"`
	Text        string `json:"text"`      // user_message, assistant_message, reasoning
	Streaming   bool   `json:"streaming"` // assistant_message, reasoning, proposed_plan
	Markdown    string `json:"markdown"`  // proposed_plan
	Message     string `json:"message"`   // system_notice, run_interrupt_request, run_interrupt_result
	Summary     string `json:"summary"`   // compaction

	Attachments Attachments `json:"attachments"` // user_message, assistant_message: what the message carries

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
	// QuestionAnswer is how a user_input_request was answered, wherever
	// that was (here, T3's own clients); nil while it waits
	QuestionAnswer *QuestionAnswer `json:"questionAnswer"`
	Steps          []TodoStep      `json:"steps"`
	Failure        json.RawMessage `json:"failure"` // error

	// secret_request: what the agent asks the user for (see AnswerSecret)
	Label        string `json:"label"`
	URL          string `json:"url"`
	Placeholder  string `json:"placeholder"`
	Delivery     string `json:"delivery"`     // file: saved for shell use
	SecretStatus string `json:"secretStatus"` // pending, saved, declined or cancelled
}

// Attachment is something a message carries: an image, a file, or a kind
// of T3's this client does not know, which still has a name and a size.
type Attachment struct {
	Type      string `json:"type"` // image, file, or another kind
	ID        string `json:"id"`
	Name      string `json:"name"`
	MimeType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
}

// Attachments are a message's attachments. They decode leniently: an
// attachment of an unexpected shape keeps the fields that read, and one
// that is no object at all is left out, so the message still shows.
type Attachments []Attachment

// UnmarshalJSON reads the attachments it can; it never fails.
func (a *Attachments) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if json.Unmarshal(data, &raws) != nil {
		*a = nil
		return nil
	}
	out := make(Attachments, 0, len(raws))
	for _, raw := range raws {
		if b := bytes.TrimSpace(raw); len(b) == 0 || b[0] != '{' {
			continue
		}
		var att Attachment
		err := json.Unmarshal(raw, &att)
		if _, mistyped := errors.AsType[*json.UnmarshalTypeError](err); err != nil && !mistyped {
			continue
		}
		out = append(out, att)
	}
	*a = out
	return nil
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
	return JSONText(t.Input)
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

// JSONText shows a JSON value as text: a string as itself, anything else as
// compact JSON, nothing for null or an empty value.
func JSONText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return compactJSON(raw)
}

// compactJSON is raw without insignificant space, or raw as is when it is
// not valid JSON.
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

// Message includes queued user text before T3 promotes it into the transcript.
type Message struct {
	ID          string      `json:"id"`
	Role        string      `json:"role"`
	Text        string      `json:"text"`
	Attachments Attachments `json:"attachments"` // the images and files it carries, as T3 stored them
}

// ThreadProjection is the part of a thread's full state aidiff renders.
type ThreadProjection struct {
	Messages         []Message           `json:"messages"`
	Thread           Thread              `json:"thread"`
	Runs             []Run               `json:"runs"`
	RuntimeRequests  []RuntimeRequest    `json:"runtimeRequests"`
	VisibleTurnItems []ProjectedTurnItem `json:"visibleTurnItems"`
	TurnItems        []TurnItem          `json:"turnItems"`
	Subagents        []Subagent          `json:"subagents"`
	ProviderThreads  []ProviderThread    `json:"providerThreads"`
}
