package t3client

import (
	"context"
	"fmt"
)

// AnswerSecret answers the agent's secret request itemID, a secret_request
// item of thread threadID, with secrets.answerRequest: secret is saved, or
// with decline the request is turned down. T3 keeps the secret in its own
// store, for the agent to use, never in the thread.
func (c *Conn) AnswerSecret(ctx context.Context, threadID, itemID, secret string, decline bool) error {
	answer := map[string]any{"type": "save", "secret": secret}
	if decline {
		answer = map[string]any{"type": "decline"}
	}
	payload := map[string]any{"threadId": threadID, "turnItemId": itemID, "answer": answer}
	if err := c.call(ctx, "secrets.answerRequest", payload, nil); err != nil {
		return fmt.Errorf("answer the secret request: %w", err)
	}
	return nil
}
