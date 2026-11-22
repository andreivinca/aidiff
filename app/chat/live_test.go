package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestElapsedText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second:                          "0s",
		0:                                     "0s",
		12*time.Second + 900*time.Millisecond: "12s",
		5*time.Minute + 24*time.Second:        "5m 24s",
		time.Hour + 3*time.Minute + 59*time.Second: "1h 03m",
	} {
		assert.Equal(t, want, elapsedText(d), d)
	}
}

func TestPane_StatusCountsTheRunsTime(t *testing.T) {
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	conv := fixture()
	conv.RunningSince = start
	p, f := newTestPane(t, 80, 30, conv)
	locked(p, func() {
		p.now = func() time.Time { return start.Add(5*time.Minute + 24*time.Second) }
		p.gen++ // the frame drawn before the clock was set is stale
	})
	assert.Contains(t, view(p), "Working 5m 24s")

	idle := fixture()
	idle.State, idle.Running = AgentIdle, nil
	show(t, p, f, idle)
	assert.NotContains(t, view(p), "Working", "nothing runs, no count")
}

func TestPane_ElapsedTimeRedrawsByItself(t *testing.T) {
	old := clockTick
	clockTick = 20 * time.Millisecond
	t.Cleanup(func() { clockTick = old })
	conv := fixture()
	conv.RunningSince = time.Now()
	p, _ := newTestPane(t, 80, 30, conv)
	view(p) // drawing the count arms the redraw
	for len(p.Updates()) > 0 {
		<-p.Updates() // what opening the pane asked for
	}
	select {
	case <-p.Updates():
	case <-time.After(2 * time.Second):
		t.Fatal("the elapsed time never asked for a redraw")
	}
}

func TestPane_ShowsWhatTheAgentDoesNow(t *testing.T) {
	conv := fixture()
	conv.Entries = []Entry{
		UserMessage{Text: "go"},
		Step{Kind: StepCommand, Text: "make lint", State: Done},
		Step{Kind: StepThought, Text: "done thinking"},
		Step{Kind: StepCommand, Text: "python3 - <<'EOF'\nprint(1)\nEOF", State: Running},
		Step{Kind: StepThought, Text: "I'm weighing whether a JSON rewrite with json.dump would keep the file's formatting", State: Running},
	}
	p, f := newTestPane(t, 80, 30, conv)
	v := view(p)
	assert.Contains(t, v, "⋯ Ran 2 commands · /work")
	assert.Contains(t, v, "$ python3 - <<'EOF' …", "the command running, its first line")
	assert.Contains(t, v, "✻ I'm weighing whether a JSON rewrite", "the thought being written")
	assert.NotContains(t, v, "make lint", "beyond the last three steps, the count")

	long := strings.Repeat("word ", 200) + "the latest words"
	conv.Entries[4] = Step{Kind: StepThought, Text: long, State: Running}
	show(t, p, f, conv)
	rows := locked2(p, func() []string { return p.liveStepRows(stepsOf(p.conv.Entries), 40, false).rows })
	require.Len(t, rows, 1+liveThoughtRows, "a long thought keeps only its end")
	assert.Contains(t, strings.Join(rows, " "), "latest", "the newest words are the ones kept")
	for _, r := range rows {
		assert.LessOrEqual(t, ansi.StringWidth(r), 40)
	}

	for i := range conv.Entries {
		if s, ok := conv.Entries[i].(Step); ok {
			s.State = Done
			conv.Entries[i] = s
		}
	}
	show(t, p, f, conv)
	v = view(p)
	assert.Contains(t, v, "$ python3 - <<'EOF'", "while the agent works, the last steps stay though they finished")
	assert.Contains(t, v, "✻ …word", "and so does the last thought, as T3's client shows it, its start cut")

	conv.State, conv.Running = AgentIdle, nil
	show(t, p, f, conv)
	v = view(p)
	assert.NotContains(t, v, "python3", "once the agent stops the count stands alone")
	assert.NotContains(t, v, "✻")

	conv.State, conv.Running = AgentRunning, []string{"run-2"}
	conv.Entries = append(conv.Entries, AgentMessage{Markdown: "Status: moving on"})
	show(t, p, f, conv)
	assert.NotContains(t, view(p), "python3", "a run of steps the agent has written past is only counted")
}

func TestWorkSummary(t *testing.T) {
	for want, work := range map[string][]Step{
		"+ Changed 1 file and ran 5 commands": {{Kind: StepEdit}, {Kind: StepCommand}, {Kind: StepThought}, {Kind: StepCommand},
			{Kind: StepCommand}, {Kind: StepCommand}, {Kind: StepCommand}},
		"+ Ran 6 commands":  {{Kind: StepCommand}, {Kind: StepCommand}, {Kind: StepCommand}, {Kind: StepCommand}, {Kind: StepCommand}, {Kind: StepCommand}},
		"+ Thought":         {{Kind: StepThought}},
		"+ Thought 2 times": {{Kind: StepThought}, {Kind: StepThought}},
		"⋯ Ran 1 command":   {{Kind: StepThought}, {Kind: StepCommand, State: Running}},
		"+ Searched 1 time, ran 1 command and used 1 tool": {{Kind: StepWeb}, {Kind: StepCommand}, {Kind: StepTool}},
	} {
		assert.Equal(t, want+" · /work shows them", workSummary(work))
	}
}

func stepsOf(entries []Entry) []Step {
	var out []Step
	for _, e := range entries {
		if s, ok := e.(Step); ok {
			out = append(out, s)
		}
	}
	return out
}

func locked2[T any](p *Pane, f func() T) T {
	p.mu.Lock()
	defer p.mu.Unlock()
	return f()
}
