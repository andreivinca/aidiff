package t3client

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscribeThread_Activity(t *testing.T) {
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		send(t, ws, map[string]any{"_tag": "Chunk", "requestId": req["id"], "values": []any{
			map[string]any{"kind": "snapshot", "projection": map[string]any{
				"thread": map[string]any{"id": "th-1", "activeProviderThreadId": "pt-1"},
				"subagents": []any{map[string]any{"id": "agent-1", "title": "Review", "prompt": "Review the change",
					"status": "running", "childThreadId": "child-1", "progress": "Reading the files", "result": nil}},
				"providerThreads": []any{map[string]any{"id": "pt-1", "pendingBackgroundTasks": []any{
					map[string]any{"taskId": "native-1", "kind": "command", "itemId": "cmd-1", "command": "npm run dev", "awaited": true},
				}}},
				"turnItems": []any{map[string]any{"id": "cmd-1", "type": "command_execution", "status": "running",
					"input": "npm run dev", "monitor": true, "nativeItemRef": map[string]any{"nativeId": "native-1"}}},
			}},
			map[string]any{"kind": "event", "event": map[string]any{"type": "subagent.updated", "payload": map[string]any{
				"id": "agent-1", "status": "completed", "prompt": "", "promptOmitted": true, "result": "No issues"}}},
			map[string]any{"kind": "event", "event": map[string]any{"type": "provider-thread.updated", "payload": map[string]any{
				"id": "pt-1", "pendingBackgroundTasks": []any{}}}},
		}})
		send(t, ws, success(req["id"], nil))
	})
	c := dial(t, url)
	var snapshot ThreadProjection
	var agents []Subagent
	var providers []ProviderThread
	err := SubscribeThread(context.Background(), c, "th-1", ThreadEvents{
		Snapshot:       func(pr ThreadProjection) { snapshot = pr },
		Subagent:       func(a Subagent) { agents = append(agents, a) },
		ProviderThread: func(p ProviderThread) { providers = append(providers, p) },
	})
	require.NoError(t, err)
	require.NotNil(t, snapshot.Thread.ActiveProviderThreadID)
	assert.Equal(t, "pt-1", *snapshot.Thread.ActiveProviderThreadID)
	require.Len(t, snapshot.Subagents, 1)
	assert.Equal(t, "Reading the files", snapshot.Subagents[0].Progress)
	assert.Equal(t, "child-1", snapshot.Subagents[0].ChildThreadID)
	require.Len(t, snapshot.TurnItems, 1)
	assert.True(t, snapshot.TurnItems[0].Monitor)
	assert.Equal(t, "native-1", snapshot.TurnItems[0].NativeItemRef.NativeID)
	require.Len(t, snapshot.ProviderThreads, 1)
	require.Len(t, snapshot.ProviderThreads[0].PendingBackgroundTasks, 1)
	assert.True(t, snapshot.ProviderThreads[0].PendingBackgroundTasks[0].Awaited)
	require.Len(t, agents, 1)
	assert.Equal(t, "completed", agents[0].Status)
	assert.True(t, agents[0].PromptOmitted)
	assert.Equal(t, "No issues", agents[0].Result)
	require.Len(t, providers, 1)
	assert.Empty(t, providers[0].PendingBackgroundTasks, "an empty update clears finished background work")

	h := ThreadEvents{Subagent: func(Subagent) { t.Fatal("invalid subagent dispatched") },
		ProviderThread: func(ProviderThread) { t.Fatal("invalid provider thread dispatched") }}
	for _, event := range []string{"subagent.updated", "provider-thread.updated"} {
		h.dispatch(event, json.RawMessage(`{"status":"running"}`))
		h.dispatch(event, json.RawMessage(`{`))
	}
}
