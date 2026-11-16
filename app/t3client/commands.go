package t3client

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
)

// Command is one orchestration command, sent whole through Dispatch.
type Command map[string]any

// Dispatch sends an orchestration command. A commandId is added when the
// command has none; cmd itself is left as it was.
func (c *Conn) Dispatch(ctx context.Context, cmd Command) error {
	sent := maps.Clone(cmd)
	if _, ok := sent["commandId"]; !ok {
		sent["commandId"] = NewID()
	}
	var res struct {
		Sequence int `json:"sequence"`
	}
	return c.call(ctx, "orchestration.dispatchCommand", sent, &res)
}

// NewID returns a random UUID (version 4), the id format T3's own clients
// use for commands, messages and threads. A client mints the id of what it
// creates, such as a message whose attachments are stored under it first.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("t3client: random source unavailable: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RespondCommand answers a pending approval. decision is accept,
// acceptForSession, acceptAlways, decline or cancel.
func RespondCommand(threadID, requestID, decision string) Command {
	return Command{"type": "runtime-request.respond", "threadId": threadID, "requestId": requestID, "decision": decision}
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

// InterruptCommand stops one run of the thread; its queued messages go on.
// ThreadStopCommand is the stronger stop.
func InterruptCommand(threadID, runID string) Command {
	return Command{"type": "run.interrupt", "threadId": threadID, "runId": runID}
}

// QueuedRunCancelCommand drops a message from the thread's queue before it
// starts.
func QueuedRunCancelCommand(threadID, runID string) Command {
	return Command{"type": "queued-run.cancel", "threadId": threadID, "runId": runID}
}

// QueueResumeCommand starts the thread's next queued message, which a stop
// holds back.
func QueueResumeCommand(threadID string) Command {
	return Command{"type": "queue.resume", "threadId": threadID}
}

// ThreadStopCommand stops whatever the thread's agent is doing, the way T3's
// own stop does: the server picks the run actually running (never a queued
// one), or the latest one while background work it launched is still going,
// marks it stopped even when the provider does not answer the interrupt,
// cancels the orphaned background work and stops the subagent threads the
// thread delegated to. It holds the thread's queued messages back until
// QueueResumeCommand.
func ThreadStopCommand(threadID string) Command {
	return Command{"type": "thread.stop", "threadId": threadID}
}

// InteractionModeCommand switches the thread between "default" (build) and
// "plan".
func InteractionModeCommand(threadID, mode string) Command {
	return Command{"type": "thread.interaction-mode.set", "threadId": threadID, "interactionMode": mode}
}

// SideChatCommand forks the thread at its latest stable point into a side
// chat, a place for a question that must not derail the main work. The
// client mints newThreadID with NewID.
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
