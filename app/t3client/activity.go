package t3client

// ProviderRef identifies an item in the provider's own transcript.
type ProviderRef struct {
	NativeID string `json:"nativeId"`
}

// Subagent is a task T3 tracks independently of its visible conversation row.
type Subagent struct {
	ID            string       `json:"id"`
	ThreadID      string       `json:"threadId"`
	RunID         *string      `json:"runId"`
	NativeTaskRef *ProviderRef `json:"nativeTaskRef"`
	ChildThreadID string       `json:"childThreadId"`
	Title         string       `json:"title"`
	Prompt        string       `json:"prompt"`
	Progress      string       `json:"progress"`
	Result        string       `json:"result"`
	Model         string       `json:"model"`
	Status        string       `json:"status"`
	StartedAt     string       `json:"startedAt"`
	UpdatedAt     string       `json:"updatedAt"`
	PromptOmitted bool         `json:"promptOmitted"`
	ResultOmitted bool         `json:"resultOmitted"`
}

// BackgroundTask is work the provider reports as still pending, including
// commands left running after the agent's turn ends. IDs come from T3.
type BackgroundTask struct {
	TaskID        string `json:"taskId"`
	Kind          string `json:"kind"`
	Description   string `json:"description"`
	Command       string `json:"command"`
	ItemID        string `json:"itemId"`
	NativeItemID  string `json:"nativeItemId"`
	ChildThreadID string `json:"childThreadId"`
	Awaited       bool   `json:"awaited"`
}

// ProviderThread carries the provider's authoritative background task list.
// Each update replaces the list, including an empty list when work ends.
type ProviderThread struct {
	ID                     string           `json:"id"`
	PendingBackgroundTasks []BackgroundTask `json:"pendingBackgroundTasks"`
}
