package splitview

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ranMsg is what a test command's work produces.
type ranMsg struct {
	what string
}

// ran is a command whose work produces ranMsg{what}.
func ran(what string) tea.Cmd {
	return func() tea.Msg {
		return ranMsg{what}
	}
}

// newCommandHarness is a split with no terminal open whose palette carries
// the given outside commands.
func newCommandHarness(t *testing.T, cmds func() []Command) *harness {
	t.Helper()
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Commands = cmds
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return hs
}

func TestSplit_PaletteAsksForCommandsOnlyWhenTheyMayChange(t *testing.T) {
	calls := 0
	hs := newCommandHarness(t, func() []Command {
		calls++
		return []Command{{Name: "T3: Connect…"}, {Name: "T3: Projects…"}}
	})
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	assert.Equal(t, 1, calls, "asked when the palette opens")
	_ = hs.m.View()
	hs.send(tea.KeyMsg{Type: tea.KeyDown})
	hs.send(tea.KeyMsg{Type: tea.KeyUp})
	_ = hs.m.View()
	assert.Equal(t, 1, calls, "not for drawing or moving the selection")
	hs.key("t")
	assert.Equal(t, 2, calls, "again when the filter changes")
	hs.send(ranMsg{"state changed"})
	assert.Equal(t, 3, calls, "and when the wrapped model handled a message")
}

func TestSplit_PaletteNavigationKeys(t *testing.T) {
	hs := newCommandHarness(t, func() []Command { return []Command{{Name: "Middle"}} }) // Open Terminal, Middle, Quit
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	for _, step := range []struct {
		key  tea.KeyMsg
		want int
	}{
		{tea.KeyMsg{Type: tea.KeyDown}, 1},
		{tea.KeyMsg{Type: tea.KeyTab}, 2},
		{tea.KeyMsg{Type: tea.KeyCtrlJ}, 0},
		{tea.KeyMsg{Type: tea.KeyUp}, 2},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, 1},
		{tea.KeyMsg{Type: tea.KeyCtrlK}, 0},
	} {
		hs.send(step.key)
		assert.Equal(t, step.want, hs.m.palette.sel, "after %s", step.key)
	}
}

func TestSplit_PaletteGroups(t *testing.T) {
	cmds := []Command{
		{Name: "Thread A", Group: "Threads"},
		{Name: "Action"},
		{Name: "Thread B", Group: "Threads"},
	}
	hs := newCommandHarness(t, func() []Command { return cmds })
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	assert.Equal(t, []string{"Open Terminal", "Action", "Quit", "Thread A", "Thread B"}, names(hs.m.palette.rows),
		"ungrouped commands come first, then each group's together")

	view := strings.Split(ansi.Strip(hs.m.View()), "\n")
	title := slices.IndexFunc(view, func(l string) bool { return strings.Contains(l, "Threads ─") })
	require.Positive(t, title, "the group has a title line")
	assert.Contains(t, view[title-1], "Quit")
	assert.Contains(t, view[title+1], "Thread A")

	hs.key("act")
	assert.NotContains(t, ansi.Strip(hs.m.View()), "Threads ─", "a group with no match has no title")

	many := make([]Command, 40)
	for i := range many {
		many[i] = Command{Name: fmt.Sprintf("Thread %02d", i), Group: "Threads"}
	}
	hs = newCommandHarness(t, func() []Command { return many })
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	hs.send(tea.KeyMsg{Type: tea.KeyUp})
	view = strings.Split(ansi.Strip(hs.m.View()), "\n")
	assert.Contains(t, strings.Join(view, "\n"), "Thread 39", "the selection at the bottom stays on screen")
	assert.NotContains(t, strings.Join(view, "\n"), "Threads ─", "its title scrolled away")
}

func TestSplit_OutsideCommands(t *testing.T) {
	var gotInner tea.Model
	hs := newCommandHarness(t, func() []Command {
		return []Command{{Name: "T3: Connect…", Detail: "ssh://box", Run: func(inner tea.Model) tea.Cmd {
			gotInner = inner
			return ran("connect")
		}}}
	})
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	assert.Equal(t, []string{"Open Terminal", "T3: Connect…", "Quit"}, names(hs.m.palette.rows),
		"outside commands sit between the built-ins and Quit")
	view := ansi.Strip(hs.m.View())
	assert.Contains(t, view, "T3: Connect…")
	assert.Contains(t, view, "ssh://box", "the detail is shown")

	hs.key("t3")
	cmd := hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Equal(t, ranMsg{"connect"}, cmd(), "enter returns the command's work")
	assert.IsType(t, innerModel{}, gotInner, "the command sees the wrapped model")
	assert.False(t, hs.m.palette.open)
}

func TestSplit_Picker(t *testing.T) {
	hs := newCommandHarness(t, nil)
	var picked PickerItem
	var typed string
	hs.send(ShowPickerMsg{
		Title: "threads on box",
		Items: []PickerItem{
			{Label: "Fix login", Detail: "sabra · idle · main", Value: 1},
			{Label: "Add icons", Detail: "sabra · running", Value: 2},
		},
		OnPick: func(it PickerItem) tea.Cmd {
			picked = it
			return ran("pick")
		},
		OnText: func(s string) tea.Cmd {
			typed = s
			return nil
		},
		TextLabel: "Connect to %s",
	})
	require.True(t, hs.m.palette.open)
	view := ansi.Strip(hs.m.View())
	assert.Contains(t, view, "threads on box")
	assert.Contains(t, view, "Fix login")
	assert.Contains(t, view, "sabra · running", "details are shown")

	hs.key("running")
	assert.Equal(t, []string{"Add icons", "Connect to running"}, names(hs.m.palette.rows),
		"details are matched too, and the typed text is offered as a last row")
	cmd := hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Equal(t, ranMsg{"pick"}, cmd())
	assert.Equal(t, 2, picked.Value)
	assert.False(t, hs.m.palette.open)

	hs.send(ShowPickerMsg{Items: []PickerItem{{Label: "local"}}, OnText: func(s string) tea.Cmd {
		typed = s
		return nil
	}})
	hs.key("local")
	assert.Equal(t, []string{"local"}, names(hs.m.palette.rows), "text equal to a label adds no row")
	hs.send(tea.KeyMsg{Type: tea.KeyBackspace})
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("host\n"), Paste: true})
	assert.Equal(t, []string{"locahost"}, names(hs.m.palette.rows), "pasted text counts, line breaks dropped")
	hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "locahost", typed)

	hs.send(ShowPickerMsg{Items: []PickerItem{{Label: "x"}}})
	hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, hs.m.palette.open, "esc closes a picker")
	for _, msg := range *hs.inner {
		_, isKey := msg.(tea.KeyMsg)
		assert.False(t, isKey, "no key reached the wrapped model while a picker was open: %v", msg)
	}
}

func TestSplit_Prompt(t *testing.T) {
	hs := newCommandHarness(t, nil)
	var submitted string
	hs.send(ShowPromptMsg{Title: "message to Fix login", Placeholder: "what next?", OnSubmit: func(s string) tea.Cmd {
		submitted = s
		return ran("sent")
	}})
	view := ansi.Strip(hs.m.View())
	assert.Contains(t, view, "message to Fix login")
	assert.Contains(t, view, "what next?", "the placeholder shows while empty")

	assert.Nil(t, hs.send(tea.KeyMsg{Type: tea.KeyEnter}), "enter on an empty prompt does nothing")
	assert.True(t, hs.m.palette.open)

	hs.key("run")
	hs.send(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("the\ntests"), Paste: true})
	assert.NotContains(t, ansi.Strip(hs.m.View()), "what next?")
	cmd := hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Equal(t, ranMsg{"sent"}, cmd())
	assert.Equal(t, "run the tests", submitted, "one space per space key, pasted line breaks flattened")
	assert.False(t, hs.m.palette.open)

	called := false
	hs.send(ShowPromptMsg{OnSubmit: func(string) tea.Cmd {
		called = true
		return nil
	}})
	hs.key("x")
	hs.send(tea.KeyMsg{Type: tea.KeyEsc})
	assert.False(t, hs.m.palette.open, "esc cancels")
	assert.False(t, called, "esc does not submit")
}

func TestSplit_ReplaceInnerAndFlash(t *testing.T) {
	hs := newHarnessOpts(t, 100, 30, false) // no terminal, so keys go to the wrapped model
	hs.send(FlashMsg{Text: "T3: connecting…"})
	assert.Contains(t, ansi.Strip(hs.m.View()), "T3: connecting…")
	hs.key("x")
	assert.NotContains(t, ansi.Strip(hs.m.View()), "T3: connecting…", "a key clears the flash")

	oldSize := hs.lastInnerSize()
	var retired tea.Model
	got2 := []tea.Msg{}
	hs.send(ReplaceInnerMsg{Model: innerModel{got: &got2}, Retire: func(old tea.Model) {
		retired = old
	}})
	assert.IsType(t, innerModel{}, retired, "the old model is handed to Retire")
	require.NotEmpty(t, got2, "the new model receives its size at once")
	assert.Equal(t, oldSize, got2[len(got2)-1], "sized like the model it replaced")
	hs.key("y")
	assert.Contains(t, got2, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}, "keys now reach the new model")
}

func TestSplit_RunCommandMsg(t *testing.T) {
	hs := newCommandHarness(t, func() []Command {
		return []Command{{Name: "T3: Diff View…", Run: func(tea.Model) tea.Cmd { return ran("diff") }}}
	})
	msg := tagMsg(hs.m.wsID, RunCommandMsg{Suffix: "Diff View…"})
	assert.Equal(t, scopedMsg{ws: hs.m.wsID, msg: RunCommandMsg{Suffix: "Diff View…"}}, msg,
		"a model's command reaches the split, not the model, and acts on that model only")

	cmd := hs.send(msg)
	require.NotNil(t, cmd)
	assert.Equal(t, ranMsg{"diff"}, cmd(), "the command whose name ends with the suffix runs")

	assert.Nil(t, hs.send(RunCommandMsg{Suffix: "Nothing…", Missing: "not here"}))
	assert.Equal(t, "not here", hs.m.flash)
}

func TestSplit_RunMsg(t *testing.T) {
	hs := newCommandHarness(t, nil)
	var sawInner tea.Model
	msg := RunMsg{Run: func(inner tea.Model) tea.Cmd {
		sawInner = inner
		return ran("view")
	}}
	_, wrapped := tagMsg(1, msg).(innerMsg)
	assert.False(t, wrapped, "a model's command reaches the split, not the model")
	cmd := hs.send(msg)
	require.NotNil(t, cmd)
	assert.Equal(t, ranMsg{"view"}, cmd())
	assert.IsType(t, innerModel{}, sawInner, "it runs with the wrapped model")
	assert.Nil(t, hs.send(RunMsg{}))
}

func TestSplit_AltKeysOpenThePaletteAndSidebar(t *testing.T) {
	hs := newCommandHarness(t, nil)
	alt := func(r rune) {
		hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true})
	}
	alt('p')
	assert.True(t, hs.m.palette.open, "alt+p opens the palette, as ctrl+p does")
	alt('p')
	assert.False(t, hs.m.palette.open, "and closes it")
	assert.Equal(t, "alt+b", hs.m.cfg.Keys.SidebarAlt)
}

func TestPalette_BadgeDrawnInColorAndMatched(t *testing.T) {
	hs := newHarness(t, 100, 30)
	hs.send(ShowPickerMsg{Title: "threads", Items: []PickerItem{
		{Label: "Side chat", Detail: "aidiff · idle", Badge: "side chat · test"},
		{Label: "Fix login", Detail: "aidiff · idle"},
	}, OnPick: func(PickerItem) tea.Cmd { return nil }})
	require.True(t, hs.m.palette.open)
	inner := 0
	for _, l := range strings.Split(hs.m.View(), "\n") {
		if strings.Contains(ansi.Strip(l), "side chat · test") {
			plain := ansi.Strip(l)
			assert.Contains(t, plain, "Side chat  side chat · test ", "the badge right after the label")
			assert.Contains(t, plain, "aidiff · idle", "the detail still on the right")
			assert.Contains(t, l, hs.m.styles.badge.Render(" side chat · test "), "the badge on its color")
			inner = ansi.StringWidth(plain)
		}
	}
	require.Positive(t, inner, "the badged row is drawn")
	hs.key("test")
	assert.Equal(t, []string{"Side chat"}, names(hs.m.palette.rows), "the filter matches the badge")
}
