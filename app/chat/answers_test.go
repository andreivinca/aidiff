package chat

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// asking is the fixture with the agent asking qs in the prompt id.
func asking(id string, qs ...Question) Conversation {
	conv := fixture()
	conv.State = AgentWaiting
	conv.Entries = append(conv.Entries, Questions{ID: id, Questions: qs, Pending: true})
	return conv
}

func TestPane_AnswersQuestions(t *testing.T) {
	p, f := newTestPane(t, 80, 30, fixture())
	p.SendText("/1\r")
	assert.Contains(t, view(p), "the agent is not asking a question")
	p.SendText("\x15")

	show(t, p, f, asking("req-9",
		Question{ID: "schema", Text: "Strict schemas or flexibility?", Options: []Option{
			{Label: "Strict (Recommended)", Description: "precise contracts", Value: "strict"},
			{Label: "Flexible", Description: "looser shapes", Value: "Flexible"},
		}},
		Question{ID: "areas", Text: "Which areas?", Multiple: true, Options: []Option{
			{Label: "API", Value: "API"}, {Label: "UI", Value: "UI"}, {Label: "Docs", Value: "Docs"},
		}},
	))
	v := view(p)
	assert.Contains(t, v, "? Strict schemas or flexibility?")
	assert.Contains(t, v, "precise contracts")
	assert.Contains(t, v, "looser shapes")
	assert.Contains(t, v, "↑/↓ choose")
	assert.Contains(t, v, "waiting for you")

	p.SendText("/3\r")
	assert.Contains(t, view(p), "pick a number from 1 to 2")
	p.SendText("\x15/1\r")
	v = view(p)
	assert.Contains(t, v, "Answer saved")
	assert.Contains(t, v, "Strict schemas or flexibility? ✓")
	assert.Contains(t, v, "/answer 1,3 picks several")

	c := ask(t, p, f, func() {
		p.SendText("/answer 1, 3\r\r")
	})
	assert.Equal(t, "answer", c.kind)
	assert.Equal(t, "req-9", c.id)
	assert.Equal(t, map[string]Answer{"schema": {Values: []string{"strict"}}, "areas": {Values: []string{"API", "Docs"}, Multiple: true}},
		c.answers, "an option's value; a list for a question that takes several")

	done := asking("req-9", Question{ID: "schema", Text: "Strict schemas or flexibility?"})
	done.State = AgentIdle
	done.Entries[len(done.Entries)-1] = Questions{ID: "req-9", Questions: []Question{{ID: "schema", Text: "Strict schemas or flexibility?"}}}
	show(t, p, f, done)
	v = view(p)
	assert.Contains(t, v, "question: Strict schemas or flexibility?", "an answered prompt collapses")
	assert.NotContains(t, v, "waiting for you")

	show(t, p, f, asking("req-10", Question{ID: "name", Text: "What should the branch be called?"}))
	assert.Contains(t, view(p), "type your answer, then enter")
	c = ask(t, p, f, func() {
		p.SendText("/answer feature/Login-Fix\r")
	})
	assert.Equal(t, map[string]Answer{"name": {Values: []string{"feature/Login-Fix"}}}, c.answers, "free text keeps its case")

	show(t, p, f, asking("req-11", Question{ID: "x", Text: "Continue?", OwnWords: true, Options: []Option{{Label: "Yes", Value: "Yes"}}}))
	assert.Contains(t, view(p), "or type your own answer")
	c = ask(t, p, f, func() {
		p.SendText("/dismiss\r")
	})
	assert.Equal(t, call{kind: "dismiss", id: "req-11"}, c)
}

func TestPane_FailedAnswersAreKept(t *testing.T) {
	p, f := newTestPane(t, 80, 22, asking("req-9",
		Question{ID: "first", Text: "First?", Options: []Option{{Label: "A", Value: "A"}, {Label: "B", Value: "B"}}},
		Question{ID: "second", Text: "Second?", Options: []Option{{Label: "C", Value: "C"}, {Label: "D", Value: "D"}}},
	))
	p.SendText("/1\r")
	f.set(errBoom, nil)
	ask(t, p, f, func() {
		p.SendText("/2\r\r")
	})
	assert.Contains(t, view(p), "answer failed: boom")
	assert.Contains(t, view(p), "First?", "the first answer survives the failure")

	f.set(nil, nil)
	c := ask(t, p, f, func() {
		p.SendText("\r")
	})
	assert.Equal(t, map[string]Answer{"first": {Values: []string{"A"}}, "second": {Values: []string{"D"}}}, c.answers,
		"giving the last answer again sends them all")
}

func TestPane_AnswerTurnedDownIsTakenBack(t *testing.T) {
	p, f := newTestPane(t, 80, 18, asking("req-9", Question{ID: "only", Text: "Only?", Options: []Option{{Label: "A", Value: "A"}}}))
	release := make(chan struct{})
	f.set(nil, release)
	p.SendText("/answer hello\r")
	nextCall(t, f)
	p.SendText("/1\r")
	assert.Contains(t, view(p), "still being sent")
	assert.Contains(t, view(p), "Type something… ✓", "the in-flight answer is preserved")
	assert.Contains(t, view(p), "“hello”")
	close(release)
	waitUpdate(t, p)
}

func TestParseAnswer(t *testing.T) {
	single := Question{Options: []Option{{Label: "a", Value: "a"}, {Label: "b", Value: "B"}}}
	multi := Question{Multiple: true, Options: single.Options}

	a, err := parseAnswer(single, "2")
	require.NoError(t, err)
	assert.Equal(t, Answer{Values: []string{"B"}}, a)
	a, err = parseAnswer(multi, "1,2")
	require.NoError(t, err)
	assert.Equal(t, Answer{Values: []string{"a", "B"}, Multiple: true}, a)
	_, err = parseAnswer(single, "1,2")
	require.EqualError(t, err, "this question takes one answer")
	_, err = parseAnswer(single, "9")
	require.EqualError(t, err, "pick a number from 1 to 2")
	a, err = parseAnswer(multi, "something else")
	require.NoError(t, err)
	assert.Equal(t, Answer{Values: []string{"something else"}}, a, "the user's own words are one value")
	_, err = parseAnswer(single, "  ")
	require.Error(t, err)
}

func TestPane_PlainTextAnswersPendingQuestion(t *testing.T) {
	for _, raw := range []string{"2", "None of these; surprise me"} {
		t.Run(raw, func(t *testing.T) {
			p, f := newTestPane(t, 80, 22, asking("req", Question{
				ID: "choice", Text: "Choose?", Options: []Option{
					{Label: "First", Value: "a"}, {Label: "Second", Value: "b"},
				},
			}))
			c := ask(t, p, f, func() { p.SendText(raw + "\r") })
			want := raw
			if raw == "2" {
				want = "b"
			}
			assert.Equal(t, "answer", c.kind)
			assert.Equal(t, map[string]Answer{"choice": {Values: []string{want}}}, c.answers)
		})
	}
}

func TestQuestionRowsWrapFullText(t *testing.T) {
	p, _ := newTestPane(t, 80, 22, fixture())
	rows := questionRows(Questions{Pending: true, Questions: []Question{{
		ID: "q", Text: "A long question that must wrap\nwith its final detail preserved",
		Options: []Option{{Label: "An option", Description: "A long description that must wrap and preserve its ending"}},
	}}}, 32, p.styles, nil)
	rendered := strings.Join(rows.rows, "\n")
	assert.Contains(t, rendered, "preserved")
	assert.Contains(t, rendered, "ending")
}

func TestPane_QuestionTabsReviseAnswers(t *testing.T) {
	p, f := newTestPane(t, 100, 30, asking("tabs",
		Question{ID: "one", Text: "First?"},
		Question{ID: "two", Text: "Second?"},
	))
	p.SendText("original\r")
	p.SendText("second\r")
	assert.Contains(t, view(p), "Review your answers")
	assert.Contains(t, view(p), "→ original")
	p.SendText("\t")
	assert.Contains(t, view(p), "✓ original")
	p.SendText("\x15revised\r")
	p.SendText("\t")
	c := ask(t, p, f, func() { p.SendText("\r") })
	assert.Equal(t, map[string]Answer{
		"one": {Values: []string{"revised"}},
		"two": {Values: []string{"second"}},
	}, c.answers)
}

func TestPane_QuestionPickerArrowSelection(t *testing.T) {
	p, f := newTestPane(t, 100, 30, asking("picker", Question{
		ID: "choice", Header: "Layout", Text: "Which layout?",
		Options: []Option{{Label: "First", Value: "a"}, {Label: "Second", Value: "b"}},
	}))
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	assert.Contains(t, view(p), "› 2) Second")
	c := ask(t, p, f, func() { p.SendText("\r") })
	assert.Equal(t, []string{"b"}, c.answers["choice"].Values)
}

func TestQuestionPromptRowsLayout(t *testing.T) {
	p, _ := newTestPane(t, 80, 22, fixture())
	q := Question{ID: "story", Header: "Story", Text: "Which direction?", Options: []Option{
		{Label: "A mystery: investigate the photograph and trace the letter's recipient", Description: "A mystery: investigate the photograph and trace the letter's recipient", Value: "m"},
		{Label: "Comedy", Description: "a neighbor's theories turn into a spectacle", Value: "c"},
	}}
	rows := questionPromptRows(Questions{ID: "x", Pending: true, Questions: []Question{q, {ID: "tone", Text: "Tone?"}}},
		40, p.styles, map[string]Answer{"story": {Values: []string{"c"}}}, 0, 1)
	text := ansi.Strip(strings.Join(rows.rows, "\n"))
	assert.Contains(t, text, "Story ✓")
	assert.Equal(t, 1, strings.Count(text, "investigate"), "a description that repeats the label is not shown")
	assert.Contains(t, text, "\n        photograph and trace", "wrapped option text hangs under the label")
	assert.Contains(t, text, "› 2) Comedy ✓", "the saved answer is ticked")
	assert.Contains(t, text, "a neighbor's theories")
	for _, row := range strings.Split(text, "\n") {
		assert.LessOrEqual(t, ansi.StringWidth(row), 40)
	}
}

func TestPane_QuestionPickerCustomAnswer(t *testing.T) {
	p, f := newTestPane(t, 100, 30, asking("picker", Question{
		ID: "choice", Text: "Which layout?", Options: []Option{{Label: "First", Value: "a"}},
	}))
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	p.SendText("\r")
	assert.Contains(t, view(p), "Type your own answer below")
	c := ask(t, p, f, func() { p.SendText("Something different\r") })
	assert.Equal(t, []string{"Something different"}, c.answers["choice"].Values)
}
