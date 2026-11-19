package t3client

import (
	"context"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConn_AnswerSecret(t *testing.T) {
	var got capture
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		p, _ := req["payload"].(map[string]any)
		if a, _ := p["answer"].(map[string]any); a["type"] == "save" && a["secret"] == "wrong" {
			send(t, ws, map[string]any{"_tag": "Exit", "requestId": req["id"], "exit": map[string]any{
				"_tag": "Failure", "cause": []any{map[string]any{"_tag": "Fail", "error": map[string]any{
					"_tag": "SecretRequestError", "reason": "already_answered", "detail": "This secret request was already answered."}}}}})
			return
		}
		send(t, ws, success(req["id"], nil))
	})
	c := dialFake(t, url)

	require.NoError(t, c.AnswerSecret(context.Background(), "th-1", "item-1", "hunter2", false))
	assert.Equal(t, "secrets.answerRequest", got.last()["tag"])
	assert.Equal(t, map[string]any{"threadId": "th-1", "turnItemId": "item-1", "answer": map[string]any{"type": "save", "secret": "hunter2"}},
		payloadOf(t, got.last()))

	require.NoError(t, c.AnswerSecret(context.Background(), "th-1", "item-1", "", true))
	assert.Equal(t, map[string]any{"type": "decline"}, payloadOf(t, got.last())["answer"], "a decline carries no secret")

	err := c.AnswerSecret(context.Background(), "th-1", "item-1", "wrong", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already answered", "T3's own words say why")
}
