package t3client

import (
	"cmp"
	"context"
	"errors"
	"strings"
)

// LaunchRequest describes a thread to start.
type LaunchRequest struct {
	ProjectID   string
	Title       string // defaults to the first line of Message
	Message     string // the agent's first instruction
	Model       ModelSelection
	RuntimeMode string // what the agent may do on its own; see RuntimeModeCommand
	// InteractionMode is "default" (build) or "plan"; empty means default.
	InteractionMode string
}

// LaunchThread starts a thread in the project's folder and returns its id.
// With a message the agent starts on it at once; without one the thread
// waits empty for the first message, titled "New thread" until T3 names it
// after that message.
func (c *Conn) LaunchThread(ctx context.Context, req LaunchRequest) (string, error) {
	if req.RuntimeMode == "" {
		return "", errors.New("launchThread: no runtime mode")
	}
	msg := strings.TrimSpace(req.Message)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = titleFrom(msg)
	}
	payload := map[string]any{
		"commandId":         NewID(),
		"projectId":         req.ProjectID,
		"title":             title,
		"modelSelection":    req.Model,
		"runtimeMode":       req.RuntimeMode,
		"interactionMode":   cmp.Or(req.InteractionMode, "default"),
		"workspaceStrategy": map[string]any{"type": "root"},
	}
	if msg != "" {
		payload["initialMessage"] = map[string]any{"messageId": NewID(), "text": msg, "attachments": []any{}}
	}
	if title == "" {
		payload["title"] = "New thread"
		payload["generateTitle"] = true
	}
	var res struct {
		ThreadID string `json:"threadId"`
	}
	if err := c.call(ctx, "orchestration.launchThread", payload, &res); err != nil {
		return "", err
	}
	if res.ThreadID == "" {
		return "", errors.New("launchThread: the server returned no thread id")
	}
	return res.ThreadID, nil
}
