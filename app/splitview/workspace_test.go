package splitview

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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

	d, dGot := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "a", Model: d, Replace: true, Done: func(s WorkspaceSwitch) { sw = s }})
	assert.Nil(t, sw.Left, "the same workspace: nothing is left")
	assert.NotNil(t, sw.Replaced, "its review is replaced when asked to")
	assert.NotEmpty(t, *dGot)
}

func TestWorkspace_OnScreenKeepsItsReviewUnlessReplaced(t *testing.T) {
	hs := newWorkspaceHarness(t, "a")
	hs.key("j")
	onScreen, id := hs.m.inner, hs.m.wsID

	e, eGot := otherInner()
	var sw WorkspaceSwitch
	cmd := hs.send(SwitchWorkspaceMsg{Key: "a", Model: e, Done: func(s WorkspaceSwitch) { sw = s }})
	assert.Nil(t, cmd)
	assert.Equal(t, WorkspaceSwitch{Unused: true}, sw, "picking the workspace on screen again keeps its review")
	assert.Equal(t, onScreen, hs.m.inner)
	assert.Equal(t, id, hs.m.wsID, "and what it has under way")
	assert.Empty(t, *eGot, "the model not used is never initialized or sized")
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

// msgsOf runs cmd and returns what it produces, batches unpacked.
func msgsOf(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, msgsOf(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// sendAll sends each of msgs and returns the commands they produce.
func (hs *harness) sendAll(msgs []tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(msgs))
	for _, msg := range msgs {
		cmds = append(cmds, hs.send(msg))
	}
	return tea.Batch(cmds...)
}

// initLoader is a wrapped model whose Init starts a load; it records what
// it receives.
type initLoader struct {
	got *[]tea.Msg
}

func (l initLoader) Init() tea.Cmd {
	return func() tea.Msg { return loadedMsg{n: 7} }
}

func (l initLoader) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	*l.got = append(*l.got, msg)
	return l, nil
}

func (l initLoader) View() string { return "built" }

// replaceWith is a command whose reply replaces the wrapped model with
// next, handing the model replaced to *retired.
func replaceWith(next tea.Model, retired *tea.Model) tea.Cmd {
	return func() tea.Msg {
		return ReplaceInnerMsg{Model: next, Retire: func(old tea.Model) { *retired = old }}
	}
}

func TestWorkspace_LateReplacementGoesToTheWorkspaceThatAskedForIt(t *testing.T) {
	var built []tea.Msg
	var retired tea.Model
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Workspace = "a"
		cfg.Commands = func() []Command {
			return []Command{{Name: "Diff View…", Run: func(tea.Model) tea.Cmd {
				return replaceWith(initLoader{got: &built}, &retired)
			}}}
		}
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	aModel := hs.m.inner

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	hs.key("diff")
	pending := hs.send(tea.KeyMsg{Type: tea.KeyEnter}) // a's view is being built...
	require.NotNil(t, pending)
	b, bGot := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b}) // ...when the user moves on to b
	bBefore := len(*bGot)

	initCmd := hs.sendAll(msgsOf(pending))
	assert.Equal(t, b, hs.m.inner, "b keeps its review")
	assert.Len(t, *bGot, bBefore, "and is not disturbed")
	assert.Equal(t, aModel, retired, "a's review is the one replaced")
	hs.sendAll(msgsOf(initCmd))
	assert.Contains(t, built, loadedMsg{n: 7}, "the newcomer starts loading in the background")

	hs.send(SwitchWorkspaceMsg{Key: "a"})
	assert.IsType(t, initLoader{}, hs.m.inner, "a comes back with the view it asked for")
	assert.Contains(t, built, tea.WindowSizeMsg{Width: 100, Height: 29}, "sized once on screen, under the tab strip of a and b")
}

func TestWorkspace_ReplacementForAModelGoneIsDropped(t *testing.T) {
	hs := newCommandHarness(t, nil) // the unnamed workspace, closed when left
	var built []tea.Msg
	var retired tea.Model
	pending := hs.send(RunMsg{Run: func(tea.Model) tea.Cmd { return replaceWith(initLoader{got: &built}, &retired) }})
	b, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b})

	assert.Nil(t, hs.sendAll(msgsOf(pending)), "nothing to start")
	assert.Equal(t, b, hs.m.inner)
	assert.Nil(t, retired, "nothing was replaced")
	assert.Empty(t, built, "the newcomer is never initialized or sized")

	// in one workspace, a slow request landing after a newer one
	slow := hs.send(RunMsg{Run: func(tea.Model) tea.Cmd { return replaceWith(initLoader{got: &built}, &retired) }})
	newer, _ := otherInner()
	hs.send(ReplaceInnerMsg{Model: newer})
	hs.sendAll(msgsOf(slow))
	assert.Equal(t, newer, hs.m.inner, "the older request does not undo the newer one")
	assert.Empty(t, built)
}

func TestWorkspace_RequestsOfAModelOffScreenAreDropped(t *testing.T) {
	hs := newCommandHarness(t, func() []Command {
		return []Command{{Name: "Diff View…", Run: func(tea.Model) tea.Cmd { return ran("diff") }}}
	})
	hs.send(SwitchWorkspaceMsg{Key: "a", Model: hs.m.inner})
	aID := hs.m.wsID
	b, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b})

	ranRun := false
	assert.Nil(t, hs.send(tagMsg(aID, RunMsg{Run: func(tea.Model) tea.Cmd { ranRun = true; return nil }})))
	assert.False(t, ranRun, "a RunMsg would act on the review on screen, not its own")
	assert.Nil(t, hs.send(tagMsg(aID, RunCommandMsg{Suffix: "Diff View…"})))
	hs.send(tagMsg(aID, ShowPickerMsg{Title: "Compare a against"}))
	hs.send(tagMsg(aID, ShowPromptMsg{Title: "Name"}))
	assert.False(t, hs.m.palette.open, "a picker or prompt about a review off screen is not shown")
	hs.send(tagMsg(aID, ConfirmMsg{Title: "Revert a.go?"}))
	assert.Nil(t, hs.m.confirm, "nor is a confirmation")

	hs.send(tagMsg(aID, FlashMsg{Text: "3 annotation(s) copied"}))
	assert.Equal(t, "3 annotation(s) copied", hs.m.flash, "a flash only informs: it shows whatever is on screen")
	hs.send(tagMsg(hs.m.wsID, ShowPickerMsg{Title: "Compare b against"}))
	assert.True(t, hs.m.palette.open, "the review on screen still asks")
}

func TestWorkspace_PickerAnswersGoToTheWorkspaceItWasOpenedFor(t *testing.T) {
	hs := newCommandHarness(t, nil)
	hs.send(SwitchWorkspaceMsg{Key: "a", Model: hs.m.inner})
	var built []tea.Msg
	var retired tea.Model
	hs.send(ShowPickerMsg{Title: "Compare a against", Items: []PickerItem{{Label: "main"}},
		OnPick: func(PickerItem) tea.Cmd { return replaceWith(initLoader{got: &built}, &retired) }})
	b, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b}) // a switch lands under the open picker
	require.True(t, hs.m.palette.open)

	hs.sendAll(msgsOf(hs.send(tea.KeyMsg{Type: tea.KeyEnter})))
	assert.Equal(t, b, hs.m.inner, "picking a's branch leaves b alone")
	hs.send(SwitchWorkspaceMsg{Key: "a"})
	assert.IsType(t, initLoader{}, hs.m.inner, "and rebuilds a")
}

// flasher is a wrapped model whose "c" copies, reporting with a flash, as
// the annotation list's copy-all does.
type flasher struct {
	innerModel
}

func (f flasher) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "c" {
		return f, func() tea.Msg { return FlashMsg{Text: "3 annotation(s) copied"} }
	}
	return f, nil
}

func TestWorkspace_ModelCommandsReachTheSplit(t *testing.T) {
	hs := &harness{m: New(flasher{}, Config{})}
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	hs.sendAll(msgsOf(hs.key("c")))
	assert.Equal(t, "3 annotation(s) copied", hs.m.flash, "a model's flash is the split's to show")
	assert.Contains(t, ansi.Strip(hs.m.View()), "3 annotation(s) copied")

	for _, msg := range []tea.Msg{
		FilesChangedMsg{}, OpenPaneMsg{}, OpenTerminalMsg{}, ShowPickerMsg{}, ShowPromptMsg{}, RunCommandMsg{},
		RunMsg{}, FlashMsg{}, ReplaceInnerMsg{}, ForceQuitMsg{}, ConfirmMsg{}, FilesMsg{}, ShowSidebarMsg{},
		HideSidebarMsg{}, SwitchWorkspaceMsg{},
	} {
		_, toModel := tagMsg(1, msg).(innerMsg)
		assert.False(t, toModel, "%T goes to the split, not back to the model", msg)
	}
}

// TestSplitMessagesAreMarked fails for an exported message type of the
// package without the splitMsg mark, which tagMsg would hand back to the
// wrapped model that produced it.
func TestSplitMessagesAreMarked(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	types, marked := map[string]bool{}, map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() && strings.HasSuffix(ts.Name.Name, "Msg") {
						types[ts.Name.Name] = true
					}
				}
			case *ast.FuncDecl:
				if d.Recv != nil && d.Name.Name == "splitMsg" {
					if id, ok := d.Recv.List[0].Type.(*ast.Ident); ok {
						marked[id.Name] = true
					}
				}
			}
		}
	}
	require.NotEmpty(t, types)
	for name := range types {
		assert.True(t, marked[name], "%s has no splitMsg method", name)
	}
}
