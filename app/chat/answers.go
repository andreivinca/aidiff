package chat

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// pendingQuestionsLocked is the prompt of questions the agent still waits
// on, or nil; mu must be held.
func (p *Pane) pendingQuestionsLocked() *Questions {
	for _, e := range slices.Backward(p.conv.Entries) {
		if q, ok := e.(Questions); ok && q.Pending && q.ID != "" && len(q.Questions) > 0 {
			return &q
		}
	}
	return nil
}

// answerCmd records one answer to the agent's open question and sends the
// answers once every question has one. They are kept until the backend took
// them; when sending fails, only the last answer has to be given again.
func (p *Pane) answerCmd(raw string) error {
	if p.busy {
		return errBusy
	}
	prompt := p.pendingQuestionsLocked()
	if prompt == nil {
		return errors.New("the agent is not asking a question")
	}
	if p.answersFor != prompt.ID {
		p.answersFor, p.answers = prompt.ID, map[string]Answer{}
		p.questionTab, p.questionDrafts = 0, map[int]string{}
	}
	q := nextQuestion(prompt.Questions, p.answers)
	if len(prompt.Questions) > 1 {
		if p.questionTab >= len(prompt.Questions) {
			return p.submitAnswersLocked(prompt)
		}
		q = &prompt.Questions[p.questionTab]
	}
	if q == nil {
		return errors.New("every question is answered already")
	}
	answer, err := parseAnswer(*q, raw)
	if err != nil {
		return err
	}
	p.answers[q.ID] = answer
	if len(prompt.Questions) > 1 {
		p.questionDrafts[p.questionTab] = raw
		p.questionTab++
		p.questionChoice = p.savedChoiceLocked(prompt)
		p.note = "Answer saved; Tab / Shift+Tab to review or change answers"
		return nil
	}
	if next := nextQuestion(prompt.Questions, p.answers); next != nil {
		p.note = fmt.Sprintf("answered %d of %d; next: %s", len(p.answers), len(prompt.Questions), firstLine(next.Text))
		return nil
	}
	id, last, answers := prompt.ID, q.ID, maps.Clone(p.answers)
	b := p.backend
	err = p.requestLocked(request{
		run: func(ctx context.Context) error {
			if sendErr := b.Answer(ctx, id, answers); sendErr != nil {
				return fmt.Errorf("answer failed: %w", sendErr)
			}
			return nil
		},
		finish: func(sendErr error) {
			p.answeredLocked(id, last, sendErr)
		},
	})
	if err != nil {
		delete(p.answers, last)
	}
	return err
}

// answeredLocked settles the answers to the prompt once the backend
// replied: taken, they are done with; refused, the last one is taken back,
// so giving it again sends them all. mu must be held.
func (p *Pane) answeredLocked(promptID, last string, err error) {
	if p.answersFor != promptID {
		return
	}
	if err != nil {
		delete(p.answers, last)
		return
	}
	p.answers, p.answersFor = nil, ""
}

func (p *Pane) dismissCmd(string) error {
	prompt := p.pendingQuestionsLocked()
	if prompt == nil {
		return errors.New("the agent is not asking a question")
	}
	id := prompt.ID
	b := p.backend
	return p.requestLocked(request{
		run: func(ctx context.Context) error {
			if err := b.Dismiss(ctx, id); err != nil {
				return fmt.Errorf("dismiss failed: %w", err)
			}
			return nil
		},
		finish: func(err error) {
			if err == nil && p.answersFor == id {
				p.answers, p.answersFor = nil, ""
			}
		},
	})
}

// nextQuestion is the first question without an answer, or nil.
func nextQuestion(qs []Question, answered map[string]Answer) *Question {
	for i := range qs {
		if _, ok := answered[qs[i].ID]; !ok {
			return &qs[i]
		}
	}
	return nil
}

// parseAnswer reads an answer to q: an option number, several numbers
// ("1,3") for a question that takes several, or the user's own words.
func parseAnswer(q Question, raw string) (Answer, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Answer{}, errors.New("/answer <number>, /answer 1,3 for several, or /answer <text>")
	}
	nums, numeric := parseNumbers(raw)
	if !numeric {
		return Answer{Values: []string{raw}}, nil
	}
	values := make([]string, 0, len(nums))
	for _, n := range nums {
		if n < 1 || n > len(q.Options) {
			return Answer{}, fmt.Errorf("pick a number from 1 to %d", len(q.Options))
		}
		values = append(values, q.Options[n-1].Value)
	}
	if q.Multiple {
		return Answer{Values: values, Multiple: true}, nil
	}
	if len(values) > 1 {
		return Answer{}, errors.New("this question takes one answer")
	}
	return Answer{Values: values}, nil
}

// parseNumbers reads "2" or "1, 3" as option numbers.
func parseNumbers(s string) ([]int, bool) {
	var out []int
	separator := func(r rune) bool {
		return r == ',' || r == ' '
	}
	for _, part := range strings.FieldsFunc(s, separator) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, len(out) > 0
}

// moveQuestionLocked preserves drafts while switching question tabs.
func (p *Pane) moveQuestionLocked(delta int) bool {
	prompt := p.pendingQuestionsLocked()
	if prompt == nil || len(prompt.Questions) < 2 || p.busy {
		return false
	}
	if p.answersFor != prompt.ID {
		p.answersFor, p.answers = prompt.ID, map[string]Answer{}
		p.questionTab, p.questionDrafts = 0, map[int]string{}
	}
	p.questionDrafts[p.questionTab] = p.input.text()
	p.questionTab = (p.questionTab + delta + len(prompt.Questions) + 1) % (len(prompt.Questions) + 1)
	p.questionChoice = p.savedChoiceLocked(prompt)
	p.input.clear()
	p.input.insert(p.questionDrafts[p.questionTab])
	p.inputChanged()
	p.scroll = 0
	return true
}

func (p *Pane) submitAnswersLocked(prompt *Questions) error {
	if nextQuestion(prompt.Questions, p.answers) != nil {
		return errors.New("answer every question before submitting; Tab to return")
	}
	id, answers := prompt.ID, maps.Clone(p.answers)
	b := p.backend
	return p.requestLocked(request{
		run: func(ctx context.Context) error {
			if err := b.Answer(ctx, id, answers); err != nil {
				return fmt.Errorf("answer failed: %w", err)
			}
			return nil
		},
		finish: func(err error) {
			if err == nil && p.answersFor == id {
				p.answers, p.answersFor = nil, ""
				p.questionTab, p.questionDrafts = 0, nil
			}
		},
	})
}

// savedChoiceLocked is the picker row to start the current tab on: the
// answer already saved for its question, else the first option.
func (p *Pane) savedChoiceLocked(prompt *Questions) int {
	if p.questionTab >= len(prompt.Questions) {
		return 0
	}
	q := prompt.Questions[p.questionTab]
	a, ok := p.answers[q.ID]
	return savedChoice(q, a, ok)
}

func (p *Pane) activeChoiceLocked() *Question {
	prompt := p.pendingQuestionsLocked()
	if prompt == nil {
		return nil
	}
	tab := 0
	if p.answersFor == prompt.ID {
		tab = p.questionTab
	}
	if tab >= len(prompt.Questions) {
		return nil
	}
	return &prompt.Questions[tab]
}
