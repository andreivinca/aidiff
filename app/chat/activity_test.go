package chat

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// activityFixture is the fixture with a subagent, a process waiting in
// the background, a monitor and a finished command, newest first.
func activityFixture() Conversation {
	conv := fixture()
	conv.Activity = []Activity{
		{ID: "cmd-old", Title: "go test ./...", State: Done, Status: "completed", Details: []string{"go test ./...", "ok", "Exit code: 0"}},
		{ID: "monitor", Title: "monitor · Watch test results", State: Running, Status: "running"},
		{ID: "process", Title: "npm run dev", State: Waiting, Status: "waiting"},
		{ID: "agent", Agent: true, Title: "Security review", State: Running, Status: "running", Progress: "Checking authentication",
			Details: []string{"Review login.go for security issues", "Model: review-model"}},
	}
	return conv
}

func TestPane_Activity(t *testing.T) {
	p, f := newTestPane(t, 100, 25, activityFixture())
	assert.Contains(t, view(p), "← 1 agent, 2 processes", "the composer advertises the live work")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	v := view(p)
	assert.Contains(t, v, "Subagents & processes · Active")
	assert.Less(t, strings.Index(v, "Security review"), strings.Index(v, "Processes & tasks"), "subagents come first")
	assert.Contains(t, v, "Checking authentication")
	assert.Contains(t, v, "waiting     npm run dev")
	assert.Contains(t, v, "monitor · Watch test results")
	assert.NotContains(t, v, "go test ./...", "finished work is under Recent")
	p.SendText("\r")
	assert.Contains(t, view(p), "Review login.go for security issues")
	assert.Contains(t, view(p), "Model: review-model")

	conv := activityFixture()
	conv.Activity[3].State, conv.Activity[3].Status = Done, "completed"
	show(t, p, f, conv)
	assert.NotContains(t, view(p), "Security review", "finished agents leave Active")
	conv.Activity = conv.Activity[:1]
	show(t, p, f, conv)
	assert.Contains(t, view(p), "No active subagents or processes")

	p.SendText("\t")
	assert.Contains(t, view(p), "Recent (active and finished)")
	assert.Contains(t, view(p), "completed   go test ./...")
	p.SendText("\r")
	assert.Contains(t, view(p), "Exit code: 0", "details of the selected entry")
	p.SendText("\x1b")
	assert.Contains(t, view(p), "Looking at the handler", "closing Activity restores the conversation")

	conv.Activity = nil
	show(t, p, f, conv)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	assert.Contains(t, view(p), "No subagents or processes have been reported in this conversation.", "Recent stays chosen")
}

func TestPane_ActivityKeysAndFollow(t *testing.T) {
	p, f := newTestPane(t, 100, 20, activityFixture())
	p.SendText("draft")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	assert.False(t, p.activity.open)
	assert.Equal(t, 4, p.input.cur, "left still edits a nonempty draft")
	p.SendText("\x15")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	assert.True(t, p.activity.open)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyRight})
	assert.False(t, p.activity.open)
	p.SendText("/activity\r")
	assert.True(t, p.activity.open)
	assert.Empty(t, f.calls, "opening the view asks nothing of the backend")
	p.SendText("hello")
	assert.False(t, p.activity.open)
	assert.Equal(t, "hello", p.input.text(), "typing leaves Activity and keeps the typed text")
	p.SendText("\x15")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.Paste("pasted draft")
	assert.False(t, p.activity.open)
	assert.Equal(t, "pasted draft", p.input.text())

	p.SendText("\x15")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.Follow(nil, "Other thread")
	assert.False(t, p.activity.open, "following another backend closes the view")
	assert.NotContains(t, view(p), "1 agent", "and forgets its activity")
}

func TestPane_ActivityKeepsSelectionAndFits(t *testing.T) {
	conv := fixture()
	for i := range 35 {
		conv.Activity = append(conv.Activity, Activity{ID: fmt.Sprintf("cmd-%02d", 34-i), Title: fmt.Sprintf("process %02d", 34-i),
			State: Running, Status: "running"})
	}
	p, f := newTestPane(t, 70, 15, conv)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyEnd})
	assert.Contains(t, view(p), "process 00", "End scrolls the final process into view")
	selected := p.activity.selected
	conv.Activity = append([]Activity{{ID: "new", Title: "new process", State: Running, Status: "running"}}, conv.Activity...)
	show(t, p, f, conv)
	assert.Contains(t, view(p), "process 00")
	assert.Equal(t, selected, p.activity.selected, "new work does not move the selected process")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyHome})
	assert.Contains(t, view(p), "new process")
	p.SendText("\r")
	assert.Contains(t, view(p), "No further details reported.")
	for _, w := range []int{1, 6, 20, 60} {
		for _, h := range []int{1, 2, 6, 10, 25} {
			p.Resize(w, h)
			rows := strings.Split(view(p), "\n")
			require.Len(t, rows, h)
			for _, row := range rows {
				assert.Equal(t, w, ansi.StringWidth(row), "size %dx%d", w, h)
			}
		}
	}
}

func TestPane_ActivityWheelAndPaging(t *testing.T) {
	conv := fixture()
	for i := range 20 {
		conv.Activity = append(conv.Activity, Activity{ID: strconv.Itoa(i), Title: fmt.Sprintf("job %02d", i), State: Running})
	}
	p, _ := newTestPane(t, 60, 14, conv)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	view(p)
	p.SendMouse(uv.MouseWheelEvent{Button: uv.MouseWheelDown})
	assert.Equal(t, "3", p.activity.selected, "the wheel moves the selection three entries")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyPgDown})
	assert.NotEqual(t, "3", p.activity.selected)
	assert.Contains(t, view(p), "unknown", "an entry without a status says so")
}
