package splitview

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWorkspaceHarness is a 100x30 split starting in workspace key with one
// terminal open.
func newWorkspaceHarness(t *testing.T, key string) *harness {
	t.Helper()
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.OpenOnStart = true
		cfg.Workspace = key
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	require.Len(t, hs.m.terms, 1)
	return hs
}

// otherInner is a second wrapped model, recording what it receives.
func otherInner() (innerModel, *[]tea.Msg) {
	got := []tea.Msg{}
	return innerModel{got: &got}, &got
}

func TestWorkspace_UnnamedClosesOnSwitch(t *testing.T) {
	hs := newWorkspaceHarness(t, "")
	local := hs.term(0)
	b, bGot := otherInner()
	var sw WorkspaceSwitch
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b, Done: func(s WorkspaceSwitch) { sw = s }})
	hs.m.Close()
	assert.True(t, local.isClosed(), "the unnamed workspace's terminal closes")
	assert.Empty(t, hs.m.terms, "the new workspace starts with no panes")
	assert.Equal(t, "b", hs.m.Workspace())
	assert.True(t, sw.Created)
	assert.False(t, sw.LeftKept)
	assert.Contains(t, *bGot, tea.WindowSizeMsg{Width: 100, Height: 30}, "the new review gets the whole width")
}

func TestWorkspace_SwitchingKeepsPanesAndLayout(t *testing.T) {
	hs := newWorkspaceHarness(t, "a")
	aTerm := hs.term(0)
	hs.send(tea.MouseMsg{X: hs.m.leftW - 1, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	hs.send(tea.MouseMsg{X: 49, Y: 3, Action: tea.MouseActionRelease})
	require.Equal(t, 50, hs.m.leftW, "the user dragged a's divider")

	b, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b})
	assert.False(t, aTerm.isClosed(), "a named workspace keeps running in the background")
	assert.Equal(t, 100, hs.m.leftW)
	hs.send(OpenPaneMsg{Start: hs.config().Start})
	bTerm := hs.started[len(hs.started)-1]
	assert.Equal(t, 60, hs.m.leftW, "b's pane opens at the default width")

	aTerm.updates <- struct{}{}
	hs.send(termUpdateMsg{term: aTerm})
	assert.Len(t, hs.m.terms, 1, "a parked pane's update leaves the screen alone")

	var sw WorkspaceSwitch
	hs.send(SwitchWorkspaceMsg{Key: "a", Model: innerModel{got: &[]tea.Msg{}}, Done: func(s WorkspaceSwitch) { sw = s }})
	assert.True(t, sw.Unused, "an existing workspace keeps its review unless asked to replace it")
	assert.True(t, sw.LeftKept)
	assert.Same(t, aTerm, hs.term(0), "a comes back with its pane")
	assert.Equal(t, 50, hs.m.leftW, "and its layout")
	assert.Equal(t, 0, hs.m.focus)

	bTerm.exited = true
	hs.send(termUpdateMsg{term: bTerm})
	hs.send(SwitchWorkspaceMsg{Key: "b"})
	assert.Empty(t, hs.m.terms, "a parked pane that exited is gone when its workspace comes back")
	assert.Equal(t, focusDiff, hs.m.focus)

	hs.m.Close()
	assert.True(t, aTerm.isClosed(), "Close closes the parked workspaces' panes too")
}

func TestWorkspace_QuitCountsParkedPanes(t *testing.T) {
	hs := newWorkspaceHarness(t, "a")
	b, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b})
	hs.send(quitRequestMsg{})
	assert.True(t, hs.m.quitDialog, "a parked terminal still asks before quitting")
	assert.Contains(t, hs.m.quitDetails()[0], "Closes the terminal")
}

func TestWorkspace_ReplaceAndUnknown(t *testing.T) {
	hs := newWorkspaceHarness(t, "a")
	hs.send(SwitchWorkspaceMsg{Key: "nowhere"})
	assert.Equal(t, "a", hs.m.Workspace(), "an unknown workspace without a model changes nothing")

	b, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b})
	c, cGot := otherInner()
	var sw WorkspaceSwitch
	hs.send(SwitchWorkspaceMsg{Key: "a", Model: c, Replace: true, Done: func(s WorkspaceSwitch) { sw = s }})
	assert.NotNil(t, sw.Replaced, "Replace swaps an existing workspace's review")
	assert.Len(t, hs.m.terms, 1, "and keeps its panes")
	assert.NotEmpty(t, *cGot)

	d, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "a", Model: d, Done: func(s WorkspaceSwitch) { sw = s }})
	assert.Nil(t, sw.Left, "the same workspace: nothing is left")
	assert.NotNil(t, sw.Replaced, "its review is replaced")
}

// loadedMsg is a reply a wrapped model's command brings back.
type loadedMsg struct{ n int }

// loader is a wrapped model whose "l" key starts a load that reports
// loadedMsg; it records the loads it received.
type loader struct {
	got *[]int
}

func (l loader) Init() tea.Cmd { return nil }

func (l loader) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "l" {
			return l, tea.Batch(func() tea.Msg { return loadedMsg{n: 1} }, tea.Quit)
		}
	case loadedMsg:
		*l.got = append(*l.got, msg.n)
	}
	return l, nil
}

func (l loader) View() string { return "loader" }

// deliver runs cmd and feeds what it produces back to the split, batches
// unpacked, and returns the Bubble Tea messages it passed on untagged.
func (hs *harness) deliver(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	var passed []tea.Msg
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			passed = append(passed, hs.deliver(c)...)
		}
	case innerMsg:
		hs.send(msg)
	default:
		passed = append(passed, msg)
	}
	return passed
}

func TestWorkspace_RepliesReachTheirOwnModel(t *testing.T) {
	var aGot, bGot []int
	hs := &harness{m: New(loader{got: &aGot}, Config{Workspace: "a"})}
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})

	pending := hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")}) // a starts a load
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: loader{got: &bGot}})
	passed := hs.deliver(pending) // a's reply lands while b is on screen
	assert.Equal(t, []int{1}, aGot, "the parked model gets its own reply")
	assert.Empty(t, bGot, "the model on screen does not")
	assert.Equal(t, []tea.Msg{tea.QuitMsg{}}, passed, "Bubble Tea's own messages pass untouched")

	pending = hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")}) // b starts one
	var cGot []int
	hs.send(ReplaceInnerMsg{Model: loader{got: &cGot}})
	hs.deliver(pending)
	assert.Empty(t, bGot, "a replaced model's reply is dropped")
	assert.Empty(t, cGot, "and does not reach its successor")
}
