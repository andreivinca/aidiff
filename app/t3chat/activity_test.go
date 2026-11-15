package t3chat

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/t3client"
)

func activityFixture() t3client.ThreadProjection {
	snap := fixture()
	snap.Thread.ActiveProviderThreadID = new("provider-1")
	snap.Subagents = []t3client.Subagent{{ID: "a1", Title: "Security review", Prompt: "Review login.go for security issues",
		Status: "running", Progress: "Checking authentication", ChildThreadID: "child-1", Model: "review-model",
		NativeTaskRef: &t3client.ProviderRef{NativeID: "agent-native-1"}}}
	snap.TurnItems = []t3client.TurnItem{
		{ID: "agent-row", Type: "subagent", SubagentID: "a1", ChildThreadID: "child-1", Status: "running", Prompt: "Review login.go"},
		{ID: "cmd-1", Type: "command_execution", Status: "completed", Input: json.RawMessage(`"npm run dev"`),
			NativeItemRef: &t3client.ProviderRef{NativeID: "native-cmd-1"}, Output: json.RawMessage(`"Ready on localhost:3000"`), ExitCode: new(0)},
	}
	snap.ProviderThreads = []t3client.ProviderThread{
		{ID: "provider-1", PendingBackgroundTasks: []t3client.BackgroundTask{
			{TaskID: "native-cmd-1", Kind: "command", Awaited: true},
			{TaskID: "agent-native-1", Kind: "subagent", ChildThreadID: "child-1", Description: "Duplicate review task"},
			{TaskID: "monitor-1", Kind: "monitor", Description: "Watch test results"},
		}},
		{ID: "old-provider", PendingBackgroundTasks: []t3client.BackgroundTask{
			{TaskID: "old-task", Kind: "command", Description: "Old session process"},
		}},
	}
	return snap
}

func TestPane_ActivityFromT3(t *testing.T) {
	p, f := newTestPane(t, 100, 25, activityFixture())
	assert.Contains(t, view(p), "← 1 agent, 2 processes", "the composer advertises the live work")
	assert.NotContains(t, view(p), "npm run dev", "hidden turn items stay outside the conversation")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	v := view(p)
	assert.Contains(t, v, "Subagents & processes · Active")
	assert.Contains(t, v, "Security review")
	assert.Contains(t, v, "Checking authentication")
	assert.Contains(t, v, "waiting     npm run dev", "T3 still reports the process after its launch tool completed")
	assert.Contains(t, v, "monitor · Watch test results")
	assert.NotContains(t, v, "Old session process")
	assert.NotContains(t, v, "Duplicate review task")
	assert.NotContains(t, v, "go test ./...", "finished commands are under Recent")
	p.SendText("\r")
	assert.Contains(t, view(p), "Review login.go for security issues")
	assert.Contains(t, view(p), "Model: review-model")

	f.live().Subagent(t3client.Subagent{ID: "a1", Title: "Security review", Status: "running",
		PromptOmitted: true, Progress: "Checking permissions", ChildThreadID: "child-1",
		NativeTaskRef: &t3client.ProviderRef{NativeID: "agent-native-1"}})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "Checking permissions")
	assert.Contains(t, view(p), "Review login.go for security issues", "deduplicated updates keep the original prompt")
	f.live().Subagent(t3client.Subagent{ID: "a1", Title: "Security review", Status: "completed", PromptOmitted: true, Result: "No issues found",
		NativeTaskRef: &t3client.ProviderRef{NativeID: "agent-native-1"}})
	waitUpdate(t, p)
	assert.NotContains(t, view(p), "Security review", "finished agents leave Active")
	f.live().ProviderThread(t3client.ProviderThread{ID: "provider-1"})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "No active subagents or processes")

	p.SendText("\t")
	assert.Contains(t, view(p), "Recent (active and finished)")
	assert.Contains(t, view(p), "Security review")
	assert.Contains(t, view(p), "completed   npm run dev")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyHome})
	p.SendText("\r")
	assert.Contains(t, view(p), "No issues found", "the selected agent exposes its completed result")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyDown})
	assert.Contains(t, view(p), "Ready on localhost:3000", "details include the command output T3 supplied")
	assert.Contains(t, view(p), "Exit code: 0")
	p.SendText("\x1b")
	assert.Contains(t, view(p), "Looking at the handler", "closing Activity restores the conversation")
}

func TestPane_ActivityKeysAndThreadSwitch(t *testing.T) {
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
	assert.Empty(t, f.cmds, "opening the view sends no command to the agent")
	p.SendText("hello")
	assert.False(t, p.activity.open)
	assert.Equal(t, "hello", p.input.text(), "typing leaves Activity and keeps the typed text")
	p.SendText("\x15")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.Paste("pasted draft")
	assert.False(t, p.activity.open)
	assert.Equal(t, "pasted draft", p.input.text())

	old := p.subID
	p.Follow(nil, "th-2", "Other thread")
	p.onSubagent(old, t3client.Subagent{ID: "stale", Title: "Stale agent", Status: "running"})
	p.onProviderThread(old, t3client.ProviderThread{ID: "stale", PendingBackgroundTasks: []t3client.BackgroundTask{{TaskID: "stale"}}})
	assert.Empty(t, p.subagents)
	assert.Empty(t, p.providers)
	assert.False(t, p.activity.open)
}

func TestPane_ActivityKeepsSelectionAndFits(t *testing.T) {
	snap := fixture()
	for i := range 35 {
		snap.TurnItems = append(snap.TurnItems, t3client.TurnItem{ID: fmt.Sprintf("cmd-%02d", i),
			Type: "command_execution", Status: "running", Ordinal: i, Input: json.RawMessage(fmt.Sprintf(`"process %02d"`, i))})
	}
	p, f := newTestPane(t, 70, 15, snap)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyEnd})
	assert.Contains(t, view(p), "process 00", "End scrolls the final process into view")
	selected := p.activity.selected
	f.live().TurnItem(t3client.TurnItem{ID: "new", Type: "command_execution", Status: "running", Ordinal: 100, Input: json.RawMessage(`"new process"`)})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "process 00")
	assert.Equal(t, selected, p.activity.selected, "new work does not move the selected process")
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyHome})
	assert.Contains(t, view(p), "new process")
	p.SendText("\r")
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

func TestPane_ActivityExcludesRolledBackAndClearsPendingTasks(t *testing.T) {
	snap := fixture()
	snap.Runs = append(snap.Runs, t3client.Run{ID: "rollback", Ordinal: 3, Status: "rolled_back"})
	snap.Subagents = []t3client.Subagent{{ID: "rolled-back-agent", Status: "running", RunID: new("rollback")}}
	snap.TurnItems = []t3client.TurnItem{{ID: "rolled-back-command", Type: "command_execution", Status: "running", RunID: new("rollback")}}
	p, f := newTestPane(t, 90, 20, snap)
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyLeft})
	assert.Contains(t, view(p), "No active subagents or processes")
	f.live().ProviderThread(t3client.ProviderThread{ID: "provider", PendingBackgroundTasks: []t3client.BackgroundTask{
		{TaskID: "future-kind", Kind: "future_kind", Description: "Provider-reported task"},
	}})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "Provider-reported task", "unknown future task kinds still appear")
	f.live().ProviderThread(t3client.ProviderThread{ID: "provider"})
	waitUpdate(t, p)
	assert.Contains(t, view(p), "No active subagents or processes", "an empty task update removes ended processes")
}
