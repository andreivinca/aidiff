package t3chat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/t3client"
)

func askQuestions(f *fakeServer, id, requestID string, qs ...t3client.Question) {
	f.live().TurnItem(t3client.TurnItem{ID: id, Type: "user_input_request", Status: "waiting", RequestID: requestID, Questions: qs})
	f.live().Request(t3client.RuntimeRequest{ID: requestID, Kind: "user_input", Status: "pending"})
}

func TestPane_AnswersQuestions(t *testing.T) {
	p, f := newTestPane(t, 80, 16, fixture())
	p.SendText("/1\r")
	assert.Contains(t, view(p), "the agent is not asking a question")

	askQuestions(f, "q-item", "req-9",
		t3client.Question{ID: "schema", Question: "Strict schemas or flexibility?", Options: []t3client.QuestionOption{
			{Label: "Strict (Recommended)", Description: "precise contracts", Value: "strict"},
			{Label: "Flexible", Description: "looser shapes"},
		}},
		t3client.Question{ID: "areas", Question: "Which areas?", MultiSelect: true, Options: []t3client.QuestionOption{
			{Label: "API"}, {Label: "UI"}, {Label: "Docs"},
		}},
	)
	waitUpdate(t, p)
	v := view(p)
	assert.Contains(t, v, "? Strict schemas or flexibility?")
	assert.Contains(t, v, "1) Strict (Recommended) — precise contracts")
	assert.Contains(t, v, "2) Flexible — looser shapes")
	assert.Contains(t, v, "/1, /2… picks an option")
	assert.Contains(t, v, "waiting for you")

	p.SendText("/3\r")
	assert.Contains(t, view(p), "pick a number from 1 to 2")
	p.SendText("/1\r")
	v = view(p)
	assert.Contains(t, v, "answered 1 of 2; next: Which areas?")
	assert.Contains(t, v, "✓ Strict schemas or flexibility?")
	assert.Contains(t, v, "/answer 1,3 picks several")

	p.SendText("/answer 1, 3\r")
	cmd := nextCommand(t, f)
	assert.Equal(t, "runtime-request.respond", cmd["type"])
	assert.Equal(t, "req-9", cmd["requestId"])
	assert.Equal(t, map[string]any{"schema": "strict", "areas": []string{"API", "Docs"}}, cmd["answers"],
		"an option's value when it has one, else its label; a list for multi-select")
	waitUpdate(t, p)

	f.live().TurnItem(t3client.TurnItem{ID: "q-item", Type: "user_input_request", Status: "completed", RequestID: "req-9",
		Questions: []t3client.Question{{ID: "schema", Question: "Strict schemas or flexibility?"}}})
	f.live().Request(t3client.RuntimeRequest{ID: "req-9", Kind: "user_input", Status: "resolved"})
	waitUpdate(t, p)
	v = view(p)
	assert.Contains(t, v, "question: Strict schemas or flexibility?", "an answered request collapses")
	assert.NotContains(t, v, "waiting for you")

	askQuestions(f, "q-free", "req-10", t3client.Question{ID: "name", Question: "What should the branch be called?"})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "/answer <text> replies")
	p.SendText("/answer feature/Login-Fix\r")
	cmd = nextCommand(t, f)
	assert.Equal(t, map[string]any{"name": "feature/Login-Fix"}, cmd["answers"], "free text keeps its case")
	waitUpdate(t, p)

	askQuestions(f, "q-drop", "req-11", t3client.Question{ID: "x", Question: "Continue?", Options: []t3client.QuestionOption{{Label: "Yes"}}})
	waitUpdate(t, p)
	p.SendText("/dismiss\r")
	cmd = nextCommand(t, f)
	assert.Equal(t, "thread.user-input.dismiss", cmd["type"])
	assert.Equal(t, "req-11", cmd["requestId"])
}

func TestParseAnswer(t *testing.T) {
	single := t3client.Question{Options: []t3client.QuestionOption{{Label: "a"}, {Label: "b", Value: "B"}}}
	multi := t3client.Question{MultiSelect: true, Options: single.Options}

	v, err := parseAnswer(single, "2")
	require.NoError(t, err)
	assert.Equal(t, "B", v)
	v, err = parseAnswer(multi, "1,2")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "B"}, v)
	_, err = parseAnswer(single, "1,2")
	require.EqualError(t, err, "this question takes one answer")
	_, err = parseAnswer(single, "9")
	require.EqualError(t, err, "pick a number from 1 to 2")
	v, err = parseAnswer(single, "something else")
	require.NoError(t, err)
	assert.Equal(t, "something else", v)
	_, err = parseAnswer(single, "  ")
	require.Error(t, err)
}
