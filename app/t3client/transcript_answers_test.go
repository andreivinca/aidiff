package t3client

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuestionAnswerValues(t *testing.T) {
	var it TurnItem
	require.NoError(t, json.Unmarshal([]byte(`{"type":"user_input_request","questions":[{"id":"q1"},{"id":"q2"}],
		"questionAnswer":{"requestId":"r","answers":{"q1":"Full fix","q2":["API","Docs"]}}}`), &it))
	assert.Equal(t, []string{"Full fix"}, it.QuestionAnswer.Values("q1"), "a single answer")
	assert.Equal(t, []string{"API", "Docs"}, it.QuestionAnswer.Values("q2"), "several options")
	assert.Nil(t, it.QuestionAnswer.Values("q3"), "a question it did not answer")

	var waiting TurnItem
	require.NoError(t, json.Unmarshal([]byte(`{"type":"user_input_request","questions":[{"id":"q1"}]}`), &waiting))
	assert.Nil(t, waiting.QuestionAnswer.Values("q1"), "nil while it waits")
}

func TestThreadSideChatOf(t *testing.T) {
	var th Thread
	require.NoError(t, json.Unmarshal([]byte(`{"id":"s","lineage":{"parentThreadId":"p","relationshipToParent":"fork","sideChat":true}}`), &th))
	assert.Equal(t, "p", th.SideChatOf())
	var plain Thread
	require.NoError(t, json.Unmarshal([]byte(`{"id":"t","lineage":{"parentThreadId":null}}`), &plain))
	assert.Empty(t, plain.SideChatOf(), "a thread that is no side chat")
	var fork Thread
	require.NoError(t, json.Unmarshal([]byte(`{"id":"f","lineage":{"parentThreadId":"p","relationshipToParent":"fork"}}`), &fork))
	assert.Empty(t, fork.SideChatOf(), "a fork that is not a side chat")
}
