package t3client

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThreadEventsOnlyWholeThreadsReachThread(t *testing.T) {
	var got []Thread
	s := ThreadEvents{Thread: func(th Thread) {
		got = append(got, th)
	}}.stream()
	s.dispatch("thread.pull-request-link-updated", json.RawMessage(`{"id":"link-1","threadId":"th-1","url":"https://x"}`))
	s.dispatch("thread.future-event", json.RawMessage(`{"id":"th-1"}`))
	assert.Empty(t, got, "a thread.* event carrying something else never replaces the thread")
	s.dispatch("thread.runtime-mode-updated", json.RawMessage(`{"id":"th-1","runtimeMode":"auto"}`))
	require.Len(t, got, 1)
	assert.Equal(t, "auto", got[0].RuntimeMode)
}

func TestOutgoingMessageCommand(t *testing.T) {
	model := ModelSelection{InstanceID: "claude", Model: "opus"}
	cmd := OutgoingMessage{ThreadID: "th-1", MessageID: "m-1", Text: "hi", TitleSeed: "hi", FreshContext: true, Model: &model}.Command()
	assert.Equal(t, "message.dispatch", cmd["type"])
	assert.Equal(t, "hi", cmd["titleSeed"])
	assert.Equal(t, true, cmd["freshContext"])
	assert.Equal(t, model, cmd["modelSelection"])
	assert.Equal(t, []any{}, cmd["attachments"])

	plain := OutgoingMessage{ThreadID: "th-1", MessageID: "m-2", Text: "more"}.Command()
	for _, key := range []string{"titleSeed", "freshContext", "modelSelection"} {
		assert.NotContains(t, plain, key)
	}
}

func TestImageUploadValidate(t *testing.T) {
	require.NoError(t, ImageUpload{Name: "ok.png", Data: make([]byte, MaxImageBytes)}.Validate())
	err := ImageUpload{Name: "big.png", Data: make([]byte, MaxImageBytes+MaxImageBytes/20)}.Validate()
	require.EqualError(t, err, "big.png is 10.5 MB; T3 takes images up to 10 MB")
	require.NoError(t, ValidateImageSize("ok.png", MaxImageBytes))
	require.EqualError(t, ValidateImageSize("huge.png", 3*MaxImageBytes), "huge.png is 30.0 MB; T3 takes images up to 10 MB")
}

func TestDispatchLeavesTheCommandAsItWas(t *testing.T) {
	var got capture
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		send(t, ws, success(req["id"], nil))
	})
	c := dialFake(t, url)
	cmd := InterruptCommand("th-1", "run-1")
	require.NoError(t, c.Dispatch(context.Background(), cmd))
	assert.NotContains(t, cmd, "commandId", "the caller's map is not changed")
	assert.Regexp(t, uuidRe, payloadOf(t, got.last())["commandId"])
}

func TestJSONText(t *testing.T) {
	assert.Equal(t, "go test", JSONText(json.RawMessage(`"go test"`)))
	assert.JSONEq(t, `{"a":1}`, JSONText(json.RawMessage(`{ "a": 1 }`)))
	assert.Empty(t, JSONText(nil))
	assert.Empty(t, JSONText(json.RawMessage(`null`)))
}
