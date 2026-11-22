package splitview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openingSessions is fakeSessions whose Open opens a workspace not opened
// yet with a model of its own, and records what each opening did.
type openingSessions struct {
	*fakeSessions
	built    map[string]*[]tea.Msg      // what each opened model received, by workspace
	switches map[string]WorkspaceSwitch // what the opening switch did, by workspace
	failing  map[string]bool            // Open says why it cannot open instead
}

func newOpeningSessions(info map[string]SessionInfo) *openingSessions {
	return &openingSessions{
		fakeSessions: &fakeSessions{info: info},
		built:        map[string]*[]tea.Msg{},
		switches:     map[string]WorkspaceSwitch{},
		failing:      map[string]bool{},
	}
}

func (o *openingSessions) Open(key string) tea.Cmd {
	o.opened = append(o.opened, key)
	if o.failing[key] {
		return func() tea.Msg { return FlashMsg{Text: key + ": cannot open"} }
	}
	got := []tea.Msg{}
	o.built[key] = &got
	return func() tea.Msg {
		return SwitchWorkspaceMsg{Key: key, Model: initLoader{got: &got}, Done: func(sw WorkspaceSwitch) { o.switches[key] = sw }}
	}
}

// newUnopenedHarness is a 100x30 split in workspace a, with tabs x, a and
// y, of which x and y are not opened yet.
func newUnopenedHarness(t *testing.T, sessions *openingSessions, tweak func(*Config)) *harness {
	t.Helper()
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Workspace = "a"
		cfg.Tabs = []string{"x", "a", "y"}
		cfg.Sessions = sessions
		if tweak != nil {
			tweak(&cfg)
		}
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return hs
}

// openReplies feeds the switches cmd produces back to the split, as the
// program does once Open's work lands, and returns the other messages.
func (hs *harness) openReplies(cmd tea.Cmd) []tea.Msg {
	var rest []tea.Msg
	for _, msg := range flatten(cmd) {
		if sw, ok := msg.(SwitchWorkspaceMsg); ok {
			hs.deliver(hs.send(sw)) // its Init's reply reaches it too
			continue
		}
		rest = append(rest, msg)
	}
	return rest
}

func TestUnopened_TabsComeInOrderAroundTheWorkspace(t *testing.T) {
	s := newOpeningSessions(map[string]SessionInfo{"x": {Name: "xray", Machine: "local"}, "a": {Name: "alpha"}, "y": {Name: "yak", Machine: "devbox T3", Note: "not connected"}})
	hs := newUnopenedHarness(t, s, nil)
	assert.Equal(t, []string{"x", "a", "y"}, hs.m.tabKeys(), "the tabs as given, the workspace on screen in its place")
	assert.Equal(t, []string{"x", "a", "y"}, hs.m.Tabs())
	assert.Equal(t, "a", hs.m.Workspace(), "the run's own workspace is on screen")
	assert.Equal(t, 1, hs.m.top, "two tabs or more: the strip shows")
	assert.Equal(t, "▌1 xray · local ×  ▌2 alpha ×  ▌3 yak · devbox T3 · not connected ×", strings.TrimRight(hs.stripLine(), " "))
	assert.Empty(t, s.opened, "nothing is opened at the start")
	assert.Equal(t, []string{"a", "x", "y"}, hs.m.sessionKeys(), "the switcher lists them after the one on screen, in tab order")

	hs = newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Workspace, cfg.Tabs, cfg.Sessions = "b", []string{"x", "", "x", "y"}, s
		return cfg
	})
	assert.Equal(t, []string{"x", "y", "b"}, hs.m.tabKeys(), "a workspace not among them is appended; blanks and repeats are not tabs")

	hs = newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Workspace, cfg.Tabs = "b", []string{"x", "y"}
		return cfg
	})
	assert.Equal(t, []string{"b"}, hs.m.tabKeys(), "without Sessions nothing could open them: ignored")

	hs = newUnopenedHarness(t, s, func(cfg *Config) { cfg.Recent = []string{"y", "gone"} })
	assert.Equal(t, []string{"a", "y", "x"}, hs.m.sessionKeys(), "the run before's last used comes right after the one on screen")
}

func TestUnopened_TabKeyOpensItInPlace(t *testing.T) {
	s := newOpeningSessions(nil)
	hs := newUnopenedHarness(t, s, nil)
	cmd := hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	assert.Equal(t, "a", hs.m.Workspace(), "not shown as parked: it has nothing to show")
	assert.Equal(t, []string{"x"}, s.opened, "Open opens it")
	assert.Empty(t, s.done, "no Done of a switch made by the split")

	hs.openReplies(cmd)
	assert.Equal(t, "x", hs.m.Workspace())
	assert.Equal(t, []string{"x", "a", "y"}, hs.m.tabKeys(), "in its place, not appended")
	assert.True(t, s.switches["x"].Created, "a new workspace for the program")
	assert.Nil(t, s.switches["x"].Replaced, "the stand-in held nothing to retire")
	assert.NotNil(t, s.switches["x"].Left, "the workspace left is parked")
	assert.True(t, s.switches["x"].LeftKept)
	assert.Contains(t, *s.built["x"], loadedMsg{n: 7}, "its model is initialized and its replies reach it")
	assert.Contains(t, *s.built["x"], tea.WindowSizeMsg{Width: 100, Height: 29}, "and sized under the strip")
	assert.Equal(t, []string{"x", "a", "y"}, hs.m.sessionKeys(), "it counts as used")

	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	assert.Equal(t, "a", hs.m.Workspace(), "an opened one comes back as parked")
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	assert.Equal(t, "x", hs.m.Workspace())
	assert.Equal(t, []string{"x"}, s.opened, "and is never opened twice")
}

func TestUnopened_ClickAndSwitcherOpenIt(t *testing.T) {
	s := newOpeningSessions(nil)
	hs := newUnopenedHarness(t, s, nil)
	cmd := hs.clickAt(hs.zoneOf(t, "y", false).from+1, 0)
	assert.Equal(t, "a", hs.m.Workspace())
	assert.Equal(t, []string{"y"}, s.opened, "a click on its tab opens it")
	hs.openReplies(cmd)
	assert.Equal(t, "y", hs.m.Workspace())

	hs = newUnopenedHarness(t, s, nil)
	s.opened = nil
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}, Alt: true})
	require.True(t, hs.m.switcher.open)
	assert.Equal(t, "x", hs.selected())
	assert.Equal(t, "a", hs.m.Workspace(), "the switcher passes it by: nothing to show yet")
	assert.Empty(t, s.opened, "nor is it opened in passing")
	cmd = hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, []string{"x"}, s.opened, "chosen, it opens")
	assert.Equal(t, "a", hs.m.Workspace(), "the screen stays where it was meanwhile")
	hs.openReplies(cmd)
	assert.Equal(t, "x", hs.m.Workspace())
	assert.Equal(t, []string{"x", "a", "y"}, hs.m.tabKeys())
}

func TestUnopened_FailingToOpenKeepsTheTab(t *testing.T) {
	s := newOpeningSessions(nil)
	s.failing["y"] = true
	hs := newUnopenedHarness(t, s, nil)
	rest := hs.openReplies(hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}, Alt: true}))
	assert.Equal(t, []tea.Msg{FlashMsg{Text: "y: cannot open"}}, rest, "the program says why")
	assert.Equal(t, "a", hs.m.Workspace())
	assert.Equal(t, []string{"x", "a", "y"}, hs.m.tabKeys(), "the tab stays")
	delete(s.failing, "y")
	hs.openReplies(hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}, Alt: true}))
	assert.Equal(t, "y", hs.m.Workspace(), "showing it again tries again")
	assert.Equal(t, []string{"y", "y"}, s.opened)
}

func TestUnopened_ClosingAsksNothing(t *testing.T) {
	s := newOpeningSessions(nil)
	hs := newUnopenedHarness(t, s, nil)
	hs.clickAt(hs.zoneOf(t, "x", true).from, 0)
	assert.Nil(t, hs.m.confirm, "nothing in it to lose: no question")
	assert.Equal(t, []string{"a", "y"}, hs.m.tabKeys())
	assert.Equal(t, []string{"x"}, s.closed, "the program hears of it")
	assert.True(t, isUnopened(s.closedModels[0]), "with the stand-in as its model")
	assert.NotContains(t, hs.m.sessionKeys(), "x")
	assert.Empty(t, s.opened, "and it was never opened")

	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}, Alt: true})
	assert.NotNil(t, hs.m.confirm, "an opened tab still asks")
}

func TestUnopened_DropTabMsg(t *testing.T) {
	s := newOpeningSessions(nil)
	hs := newUnopenedHarness(t, s, nil)
	hs.send(DropTabMsg{Key: "y"})
	assert.Equal(t, []string{"x", "a"}, hs.m.tabKeys(), "a tab not opened yet goes")
	assert.Equal(t, []string{"y"}, s.closed)
	hs.send(DropTabMsg{Key: "a"})
	hs.send(DropTabMsg{Key: "nothing"})
	assert.Equal(t, []string{"x", "a"}, hs.m.tabKeys(), "an opened one, or none, is left alone")

	hs.openReplies(hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true}))
	hs.send(DropTabMsg{Key: "x"})
	assert.Equal(t, []string{"x", "a"}, hs.m.tabKeys(), "one opened since is left alone")
}

func TestUnopened_ClosingTheLastOpenedTabOpensANeighbor(t *testing.T) {
	s := newOpeningSessions(map[string]SessionInfo{"x": {Name: "xray"}})
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Workspace, cfg.Tabs, cfg.Sessions, cfg.OpenOnStart = "a", []string{"a", "x"}, s, true
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	aTerm := hs.term(0)
	cmd := hs.send(closeTabMsg{key: "a"})
	assert.Equal(t, "x", hs.m.Workspace(), "no opened tab left: the one not opened yet takes the screen")
	assert.Equal(t, []string{"x"}, hs.m.tabKeys())
	assert.Equal(t, []string{"a"}, s.closed)
	assert.Contains(t, s.done, "x", "the program hears of the switch")
	assert.Contains(t, ansi.Strip(hs.m.View()), "xray", "its stand-in names it")
	assert.Contains(t, ansi.Strip(hs.m.View()), "opening…", "and says it opens")
	hs.m.closing.Wait()
	assert.True(t, aTerm.isClosed(), "the closed tab's terminal closed")
	assert.Equal(t, []string{"x"}, s.opened, "and it opens from there")

	first := s.built["x"]
	retry := hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	assert.Equal(t, []string{"x", "x"}, s.opened, "its tab key tries again while it has not opened")
	second := s.built["x"]

	hs.openReplies(cmd)
	assert.Equal(t, "x", hs.m.Workspace())
	assert.True(t, s.switches["x"].Created, "opened in place, as a new workspace")
	assert.Nil(t, s.switches["x"].Left, "nothing was left: it was on screen")
	assert.Contains(t, *first, loadedMsg{n: 7})
	assert.Contains(t, *first, tea.WindowSizeMsg{Width: 100, Height: 30}, "sized: the only tab, no strip")
	assert.Equal(t, "built", strings.TrimSpace(ansi.Strip(strings.Split(hs.m.View(), "\n")[0])), "its own view now")

	hs.openReplies(retry) // the retry lands once it opened: the review it opened stays
	assert.True(t, s.switches["x"].Unused, "the retry's model is not put on screen")
	assert.Empty(t, *second, "nor initialized")
}

func TestUnopened_LastTabThatCannotOpenGoesOnceLeft(t *testing.T) {
	s := newOpeningSessions(map[string]SessionInfo{"x": {Name: "xray"}})
	s.failing["x"] = true
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Workspace, cfg.Tabs, cfg.Sessions = "a", []string{"a", "x"}, s
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	hs.send(closeTabMsg{key: "a"})
	require.Equal(t, "x", hs.m.Workspace())
	hs.send(DropTabMsg{Key: "x"}) // the program found it gone
	assert.Equal(t, "x", hs.m.Workspace(), "the last tab stays: something must be on screen")
	assert.Contains(t, ansi.Strip(hs.m.View()), "it cannot be opened", "its stand-in says so")
	assert.Empty(t, hs.m.flash, "no word of the last tab staying: it was not asked to close")
	opened := len(s.opened)
	hs.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	assert.Len(t, s.opened, opened, "its tab key no longer tries")

	b, _ := otherInner()
	hs.send(SwitchWorkspaceMsg{Key: "b", Model: b})
	assert.Equal(t, []string{"b"}, hs.m.tabKeys(), "once another workspace comes, it goes with its tab")
	assert.NotContains(t, hs.m.tabs, "x")
	assert.NotContains(t, hs.m.sessionKeys(), "x")
}

func TestUnopened_RunStartsOnAWorkspaceThatOpens(t *testing.T) {
	s := newOpeningSessions(map[string]SessionInfo{"x": {Name: "xray", Machine: "devbox T3", Note: "connecting…"}})
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Workspace, cfg.Tabs, cfg.Sessions, cfg.Opening = "x", []string{"a", "x"}, s, true
		return cfg
	})
	assert.True(t, isUnopened(hs.m.inner), "the model New was given is not used")
	assert.Empty(t, *hs.inner, "nor initialized")
	initCmd := hs.m.Init()
	assert.Equal(t, []string{"x"}, s.opened, "Init opens the workspace on screen")
	first := s.built["x"]
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	view := ansi.Strip(hs.m.View())
	assert.Contains(t, view, "xray · devbox T3", "the stand-in names it and its machine")
	assert.Contains(t, view, "connecting…", "and how its opening goes, as its tab says")
	assert.Contains(t, hs.stripLine(), "▌2 xray · devbox T3 · connecting… × ")

	s.info["x"] = SessionInfo{Name: "xray", Machine: "devbox T3", Note: "not connected"}
	hs.send(FlashMsg{Text: "T3: open xray: no route"}) // the dial failed: no message reaches the stand-in
	assert.Contains(t, ansi.Strip(hs.m.View()), "not connected", "drawn afresh, never from the cache")
	retry := hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, []string{"x", "x"}, s.opened, "enter tries again")

	hs.openReplies(initCmd)
	assert.Equal(t, "x", hs.m.Workspace())
	assert.Equal(t, []string{"a", "x"}, hs.m.tabKeys(), "in its place")
	assert.True(t, s.switches["x"].Created)
	assert.Contains(t, *first, tea.WindowSizeMsg{Width: 100, Height: 29}, "its review is sized under the strip")
	assert.Equal(t, "built", strings.TrimSpace(ansi.Strip(strings.Split(hs.m.View(), "\n")[1])))
	hs.openReplies(retry)
	assert.True(t, s.switches["x"].Unused, "a late retry puts nothing on screen")

	hs = newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Workspace, cfg.Opening = "x", true
		return cfg
	})
	assert.False(t, isUnopened(hs.m.inner), "without Sessions nothing could open it: the model given is used")
}
