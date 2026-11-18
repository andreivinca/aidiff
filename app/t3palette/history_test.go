package t3palette

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
)

// localFake records what the palette asks of this machine's folders.
type localFake struct {
	mu         sync.Mutex
	remembered []string
	opened     []string
	watching   map[string]int // live refreshes running, by folder
	saved      []string       // the folders whose annotations were filed
}

// config is a LocalConfig over f: history lists dirs, the run starts in
// start ("" for no folder).
func (f *localFake) config(start string, dirs ...string) LocalConfig {
	f.watching = map[string]int{}
	watch := func(dir string) func(Program) func() {
		return func(Program) func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.watching[dir]++
			return func() {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.watching[dir]--
			}
		}
	}
	cfg := LocalConfig{
		History: func() []string { return dirs },
		Remember: func(dir string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.remembered = append(f.remembered, dir)
		},
		Open: func(dir string) (LocalReview, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.opened = append(f.opened, dir)
			return LocalReview{Model: fakeModel{store: annotation.NewStore()}, Watch: watch(dir)}, nil
		},
		SaveHistory: func(dir, _ string, _ []string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.saved = append(f.saved, dir)
		},
	}
	if start != "" {
		cfg.Start = &LocalWorkspace{Dir: start, Watch: watch(start)}
	}
	return cfg
}

// watches is how many live refreshes of dir run.
func (f *localFake) watches(dir string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.watching[dir]
}

func TestHistory_FoldersAreWorkspaces(t *testing.T) {
	root := t.TempDir()
	a, b, gone := filepath.Join(root, "alpha"), filepath.Join(root, "beta"), filepath.Join(root, "gone")
	for _, d := range []string{a, b} {
		require.NoError(t, os.MkdirAll(d, 0o750))
	}
	f := &localFake{}
	h := newHarness(t, Config{NoChat: true, LocalName: "andrei-pc", Local: f.config(a, b, gone, a)}, nil)
	sb := h.p.Sidebar()
	require.True(t, sb.Available(), "this machine is selected: its history shows")
	assert.Equal(t, localKey(a), h.p.Workspace(), "the run's folder is a workspace of its own")
	assert.Equal(t, a, h.p.LocalDir())
	assert.Equal(t, Place{Dir: a}, h.p.TerminalPlace(), "a terminal opens in the folder on screen")

	view := ansi.Strip(sb.View(40, 14, true))
	assert.Contains(t, view, " History\n andrei-pc")
	assert.Contains(t, view, " 1 ▸ alpha", "the run's folder on top, marked")
	assert.Contains(t, view, " 2   beta")
	assert.NotContains(t, view, "gone", "a folder no longer there is left out")
	assert.Contains(t, view, "open folder")
	h.settle()

	// the run's own review leaves, with notes, for beta
	var inner tea.Model = annotatedModel()
	msg := pick(t, sb, 2)
	require.NotNil(t, msg.Model, "a folder not seen yet is opened")
	inner = h.swap(msg, inner)
	h.settle()
	assert.Equal(t, b, h.p.LocalDir())
	assert.Equal(t, Place{Dir: b}, h.p.TerminalPlace(), "beta's terminals open in beta, not where the run started")
	assert.Equal(t, localKey(b), h.p.Workspace())
	assert.Equal(t, []string{b}, f.opened)
	assert.Contains(t, f.remembered, b, "the folder opened is recorded as opened now")
	assert.Equal(t, []string{a}, f.saved, "alpha's notes are filed under alpha")
	assert.Empty(t, h.history(), "not as a T3 review's")
	assert.Equal(t, 1, f.watches(b), "beta is refreshed live")

	back := pick(t, sb, 1)
	assert.Nil(t, back.Model, "a folder seen before comes back as its workspace, nothing rebuilt")
	inner = h.swap(back, inner)
	h.settle()
	assert.Equal(t, a, h.p.LocalDir())
	assert.Equal(t, 1, f.watches(a), "alpha's refresh runs again")
	assert.Zero(t, f.watches(b), "beta's stopped")
	assert.Equal(t, []string{b}, f.opened)

	cmd, hide := sb.Select(1)
	assert.Nil(t, cmd, "the folder on screen stays as it is")
	assert.True(t, hide)
	cmd, hide = sb.Select(3)
	assert.False(t, hide)
	assert.Equal(t, []string{"there is no folder 3 in the history"}, flashes(run(cmd)))

	// a T3 project comes on; the folder waits for it
	s := newFakeSession("ssh://s", twoProjectShell())
	h.connect(t, "ssh://s", s)
	_, err := h.p.fetchShell(t.Context(), s)
	require.NoError(t, err)
	assert.Contains(t, ansi.Strip(sb.View(30, 14, false)), "Alpha", "a live server: its projects show")
	inner = h.swap(pick(t, sb, 1), inner) // its thread's watch runs until stopped: no settle
	assert.Empty(t, h.p.LocalDir(), "a T3 review is on screen")
	assert.Eventually(t, func() bool { return f.watches(a) == 0 }, time.Second, time.Millisecond)

	run(func() tea.Msg { h.p.useLocal(); return nil })
	inner = h.swap(pick(t, sb, 2), inner)
	h.settle()
	assert.Equal(t, b, h.p.LocalDir(), "beta comes back from the history")
	assert.Equal(t, Place{Dir: b}, h.p.TerminalPlace(), "the local folder wins over the live server's project")
	assert.Nil(t, h.p.OnScreen())
	assert.Equal(t, 1, f.watches(b))
	_ = inner
}

func TestHistory_NoFolderRun(t *testing.T) {
	dir := t.TempDir()
	f := &localFake{}
	h := newHarness(t, Config{NoChat: true, Local: f.config("", dir)}, nil)
	assert.Empty(t, h.p.Workspace(), "a run of no folder, stdin say, stays unnamed")
	assert.Empty(t, h.p.LocalDir())
	msg := pick(t, h.p.Sidebar(), 1)
	assert.Equal(t, localKey(dir), msg.Key)
	h.swap(msg, fakeModel{store: annotation.NewStore()})
	assert.Equal(t, dir, h.p.LocalDir())
}
