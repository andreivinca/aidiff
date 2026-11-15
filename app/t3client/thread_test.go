package t3client

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscribeThreadDecodes(t *testing.T) {
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		assert.Equal(t, "orchestration.subscribeThread", req["tag"])
		assert.Equal(t, "th-1", payloadOf(t, req)["threadId"])
		snapshot := map[string]any{"kind": "snapshot", "snapshotSequence": 7, "projection": map[string]any{
			"thread": map[string]any{"id": "th-1", "projectId": "p1", "title": "Fix login", "updatedAt": "2026-11-15T10:00:00Z",
				"interactionMode": "plan", "runtimeMode": "approval-required"},
			"runs":            []any{map[string]any{"id": "run-1", "threadId": "th-1", "ordinal": 1, "status": "running", "userMessageId": "m1"}},
			"runtimeRequests": []any{map[string]any{"id": "req-1", "kind": "command", "status": "pending"}},
			"visibleTurnItems": []any{
				map[string]any{"position": 2, "visibility": "local", "item": map[string]any{"id": "i2", "type": "command_execution",
					"status": "completed", "ordinal": 2, "input": "go test", "outputOmitted": true, "exitCode": 0}},
				map[string]any{"position": 1, "visibility": "local", "item": map[string]any{"id": "i1", "type": "user_message",
					"status": "completed", "ordinal": 1, "text": "hi"}},
				map[string]any{"position": 3, "visibility": "local", "item": map[string]any{"id": "i3", "type": "mystery_kind",
					"status": "completed", "ordinal": 3, "weird": map[string]any{"x": 1}}},
			},
		}}
		events := []any{
			map[string]any{"kind": "event", "sequence": 8, "event": map[string]any{"type": "turn-item.updated", "payload": map[string]any{
				"id": "i4", "type": "assistant_message", "status": "running", "ordinal": 4, "text": "Look", "streaming": true}}},
			map[string]any{"kind": "event", "sequence": 9, "event": map[string]any{"type": "run.updated", "payload": map[string]any{
				"id": "run-1", "status": "completed", "ordinal": 1}}},
			map[string]any{"kind": "event", "sequence": 10, "event": map[string]any{"type": "runtime-request.updated", "payload": map[string]any{
				"id": "req-1", "kind": "command", "status": "resolved"}}},
			map[string]any{"kind": "event", "sequence": 11, "event": map[string]any{"type": "thread.metadata-updated", "payload": map[string]any{
				"id": "th-1", "projectId": "p1", "title": "Fix login properly", "updatedAt": "2026-11-15T10:01:00Z"}}},
			map[string]any{"kind": "event", "sequence": 12, "event": map[string]any{"type": "thread.visit-recorded", "payload": map[string]any{
				"threadId": "th-1"}}},
			map[string]any{"kind": "unknown-event", "sequence": 13, "eventType": "future"},
		}
		send(t, ws, map[string]any{"_tag": "Chunk", "requestId": req["id"], "values": append([]any{snapshot}, events...)})
		send(t, ws, success(req["id"], nil))
	})
	c := dial(t, url)

	var snap ThreadProjection
	var items []TurnItem
	var runs []Run
	var reqs []RuntimeRequest
	var threads []Thread
	err := SubscribeThread(context.Background(), c, "th-1", ThreadEvents{
		Snapshot: func(p ThreadProjection) { snap = p },
		TurnItem: func(it TurnItem) { items = append(items, it) },
		Run:      func(r Run) { runs = append(runs, r) },
		Request:  func(r RuntimeRequest) { reqs = append(reqs, r) },
		Thread:   func(th Thread) { threads = append(threads, th) },
	})
	require.NoError(t, err)

	assert.Equal(t, "Fix login", snap.Thread.Title)
	assert.Equal(t, "plan", snap.Thread.InteractionMode)
	assert.Equal(t, "approval-required", snap.Thread.RuntimeMode)
	require.Len(t, snap.Runs, 1)
	assert.True(t, snap.Runs[0].Active())
	require.Len(t, snap.RuntimeRequests, 1)
	assert.True(t, snap.RuntimeRequests[0].Pending())
	require.Len(t, snap.VisibleTurnItems, 3)
	cmd := snap.VisibleTurnItems[0].Item
	assert.Equal(t, "go test", cmd.InputText())
	assert.True(t, cmd.OutputOmitted)
	assert.Equal(t, 0, *cmd.ExitCode)
	assert.True(t, cmd.Done())
	assert.Equal(t, "mystery_kind", snap.VisibleTurnItems[2].Item.Type, "unknown item types still decode")

	require.Len(t, items, 1)
	assert.Equal(t, "Look", items[0].Text)
	assert.True(t, items[0].Streaming)
	require.Len(t, runs, 1)
	assert.False(t, runs[0].Active())
	require.Len(t, reqs, 1)
	assert.False(t, reqs[0].Pending())
	require.Len(t, threads, 1, "only thread.* events that carry a thread")
	assert.Equal(t, "Fix login properly", threads[0].Title)
}

func TestTurnItemTextHelpers(t *testing.T) {
	it := TurnItem{Input: []byte(`{"path": "a.go",  "n": 1}`), Failure: []byte(`{"message": "rate limited", "kind": "provider"}`)}
	assert.Len(t, it.InputText(), 21, "tool arguments come back as compact JSON")
	assert.JSONEq(t, `{"path":"a.go","n":1}`, it.InputText())
	assert.Equal(t, "rate limited", it.FailureText())
	assert.Empty(t, TurnItem{}.InputText())
	assert.Equal(t, `{"code":7}`, TurnItem{Failure: []byte(`{"code": 7}`)}.FailureText(), "a failure without a message shows as is")
}

func TestCommandsAndDispatch(t *testing.T) {
	var got capture
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		send(t, ws, success(req["id"], map[string]any{"sequence": 1}))
	})
	c := dial(t, url)
	ctx := context.Background()

	require.NoError(t, Dispatch(ctx, c, InterruptCommand("th-1", "run-1")))
	p := payloadOf(t, got.last())
	assert.Equal(t, "orchestration.dispatchCommand", got.last()["tag"])
	assert.Equal(t, "run.interrupt", p["type"])
	assert.Equal(t, "run-1", p["runId"])
	assert.Regexp(t, uuidRe, p["commandId"], "a command id is added")

	require.NoError(t, RespondRequest(ctx, c, "th-1", "req-1", "accept"))
	p = payloadOf(t, got.last())
	assert.Equal(t, "runtime-request.respond", p["type"])
	assert.Equal(t, "req-1", p["requestId"])
	assert.Equal(t, "accept", p["decision"])

	require.NoError(t, Dispatch(ctx, c, InteractionModeCommand("th-1", "plan")))
	assert.Equal(t, "plan", payloadOf(t, got.last())["interactionMode"])
	require.NoError(t, Dispatch(ctx, c, RuntimeModeCommand("th-1", "auto")))
	assert.Equal(t, "auto", payloadOf(t, got.last())["runtimeMode"])

	require.NoError(t, Dispatch(ctx, c, Command{"type": "thread.visit", "threadId": "th-1", "commandId": "fixed"}))
	assert.Equal(t, "fixed", payloadOf(t, got.last())["commandId"], "a given command id is kept")

	require.NoError(t, Dispatch(ctx, c, AnswerCommand("th-1", "req-2", map[string]any{"q1": "Strict", "q2": []string{"a", "b"}})))
	p = payloadOf(t, got.last())
	assert.Equal(t, "runtime-request.respond", p["type"])
	assert.Equal(t, map[string]any{"q1": "Strict", "q2": []any{"a", "b"}}, p["answers"])
	require.NoError(t, Dispatch(ctx, c, DismissQuestionCommand("th-1", "req-2")))
	assert.Equal(t, "thread.user-input.dismiss", payloadOf(t, got.last())["type"])
	assert.Equal(t, "label", QuestionOption{Label: "label"}.Answer())
	assert.Equal(t, "v", QuestionOption{Label: "label", Value: "v"}.Answer())
}

func TestPersistImagesAndMessageWith(t *testing.T) {
	var got capture
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		send(t, ws, success(req["id"], map[string]any{"attachments": []any{
			map[string]any{"type": "image", "id": "att-1", "name": "shot.png", "mimeType": "image/png", "sizeBytes": 4},
		}}))
	})
	c := dial(t, url)
	atts, err := PersistImages(context.Background(), c, "th-1", "msg-1", []ImageUpload{{Name: "shot.png", MimeType: "image/png", Data: []byte("abcd")}})
	require.NoError(t, err)
	p := payloadOf(t, got.last())
	assert.Equal(t, "assets.persistChatAttachments", got.last()["tag"])
	assert.Equal(t, "msg-1", p["messageId"])
	uploads, _ := p["attachments"].([]any)
	require.Len(t, uploads, 1)
	up, _ := uploads[0].(map[string]any)
	assert.Equal(t, "image", up["type"])
	assert.InDelta(t, 4.0, up["sizeBytes"], 0)
	assert.Equal(t, "data:image/png;base64,YWJjZA==", up["dataUrl"])
	require.Len(t, atts, 1)

	cmd := MessageCommandWith("th-1", "msg-1", "see", atts)
	assert.Equal(t, "msg-1", cmd["messageId"])
	raw, err := json.Marshal(cmd["attachments"])
	require.NoError(t, err)
	assert.JSONEq(t, `[{"type":"image","id":"att-1","name":"shot.png","mimeType":"image/png","sizeBytes":4}]`, string(raw))

	_, err = PersistImages(context.Background(), c, "th-1", "msg-2", []ImageUpload{{Name: "huge.png", MimeType: "image/png", Data: make([]byte, MaxImageBytes+1)}})
	require.Error(t, err, "over T3's limit")
}

func TestTitleSeed(t *testing.T) {
	assert.Equal(t, "Fix the login page", TitleSeed("  Fix the login page\n\nmore", 0))
	assert.Equal(t, "Attached image", TitleSeed("", 1))
	assert.Equal(t, "Attached images", TitleSeed(" ", 3))
	assert.Empty(t, TitleSeed("", 0))
}
