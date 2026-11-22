package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/localhist"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3palette"
)

// tabsInner is a wrapped model that shows nothing.
type tabsInner struct{}

func (tabsInner) Init() tea.Cmd                       { return nil }
func (tabsInner) Update(tea.Msg) (tea.Model, tea.Cmd) { return tabsInner{}, nil }
func (tabsInner) View() string                        { return "" }

// tabsHome points HOME at a fresh folder, so the tabs file is the test's.
func tabsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".config", "revdiff", "tabs.json")
}

func TestOpenTabs_RoundTripThroughTheFile(t *testing.T) {
	path := tabsHome(t)
	store := tabsStore()
	require.Equal(t, path, store.Path, "beside history.json")
	cfg := keptTabs(store)
	tabs := []t3palette.Tab{
		{Dir: "/home/u/aidiff"},
		{Server: "ssh://devbox", Machine: "devbox", ProjectID: "p1", ProjectTitle: "Sabra", ThreadID: "th-1"},
	}
	require.NoError(t, cfg.Save(tabs, 1))
	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	got, cur := cfg.Load()
	assert.Equal(t, tabs, got)
	assert.Equal(t, 1, cur)

	list, _, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, []localhist.Tab{
		{Kind: localhist.TabLocal, Dir: "/home/u/aidiff"},
		{Kind: localhist.TabT3, Server: "ssh://devbox", Machine: "devbox", Project: "p1", Title: "Sabra", Thread: "th-1"},
	}, list, "each tab says what kind it is")
}

func TestOpenTabs_UnreadableFileBringsNoTabs(t *testing.T) {
	path := tabsHome(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{broken"), 0o600))
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	tabs, cur := loadTabs(tabsStore())
	assert.Empty(t, tabs)
	assert.Equal(t, -1, cur)
	assert.Contains(t, logged.String(), "[WARN]")
	assert.Contains(t, logged.String(), "no tabs brought back", "said once")
}

// keptTabs is the palette's tabs config over store, as a run makes it.
func keptTabs(store localhist.Tabs) t3palette.TabsConfig {
	return openTabs(store, func() ([]t3palette.Tab, int) { return loadTabs(store) })
}

// splitOf is the split a run builds over t3p, as newSplit does.
func splitOf(t3p *t3palette.Palette) splitview.Model {
	tabs, recent := t3p.StartTabs()
	return splitview.New(tabsInner{}, splitview.Config{Workspace: t3p.Workspace(), Tabs: tabs, Recent: recent, Sessions: t3p.Sessions()})
}

func TestSaveTabs_TheRunsTabsComeBackAndAreSaved(t *testing.T) {
	path := tabsHome(t)
	root := t.TempDir()
	a, b, c := filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c")
	store := tabsStore()
	require.NoError(t, store.Save([]localhist.Tab{
		{Kind: localhist.TabLocal, Dir: b},
		{Kind: localhist.TabT3, Server: "ssh://devbox", Machine: "devbox", Project: "p1", Title: "Sabra"},
		{Kind: localhist.TabLocal, Dir: a},
	}, 0))

	t3p := t3palette.New(t3palette.Config{Local: t3palette.LocalConfig{Start: &t3palette.LocalWorkspace{Dir: a}}, Tabs: keptTabs(store)}, nil)
	defer t3p.Close()
	split := splitOf(t3p)
	assert.Len(t, split.Tabs(), 3, "the saved tabs come back, the run's folder among them")
	assert.Equal(t, t3p.Workspace(), split.Tabs()[2], "in its saved place")

	saveTabs(t3p, split)
	list, cur, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, []localhist.Tab{
		{Kind: localhist.TabLocal, Dir: b},
		{Kind: localhist.TabT3, Server: "ssh://devbox", Machine: "devbox", Project: "p1", Title: "Sabra"},
		{Kind: localhist.TabLocal, Dir: a},
	}, list, "saved again as they came: none was opened")
	assert.Equal(t, 2, cur, "the run's folder was on screen")

	// a run started elsewhere appends its folder
	t3p2 := t3palette.New(t3palette.Config{Local: t3palette.LocalConfig{Start: &t3palette.LocalWorkspace{Dir: c}}, Tabs: keptTabs(store)}, nil)
	defer t3p2.Close()
	saveTabs(t3p2, splitOf(t3p2))
	list, cur, err = store.Load()
	require.NoError(t, err)
	require.Len(t, list, 4)
	assert.Equal(t, c, list[3].Dir)
	assert.Equal(t, 3, cur)
	assert.FileExists(t, path)
}

func TestSaveTabs_RunOfNoFolderLeavesTheFile(t *testing.T) {
	path := tabsHome(t)
	store := tabsStore()
	require.NoError(t, store.Save([]localhist.Tab{{Kind: localhist.TabLocal, Dir: "/x"}, {Kind: localhist.TabLocal, Dir: "/y"}}, 1))
	before, err := os.ReadFile(path) //nolint:gosec // test-owned path
	require.NoError(t, err)

	// stdin, a compare pair, --only with no repository: no folder to start in
	t3p := t3palette.New(t3palette.Config{Tabs: keptTabs(store)}, nil)
	defer t3p.Close()
	split := splitOf(t3p)
	assert.Empty(t, split.Tabs(), "nothing comes back")
	saveTabs(t3p, split)
	after, err := os.ReadFile(path) //nolint:gosec // test-owned path
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "and nothing is overwritten")

	saveTabs(t3p, tabsInner{})     // a program that did not end with the split
	after, err = os.ReadFile(path) //nolint:gosec // test-owned path
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestStdinRunStartsInNoFolder(t *testing.T) {
	assert.Empty(t, sourceSetup{}.historyDir(options{Stdin: true}), "a stdin review has no folder: its run brings no tabs back")
	assert.Empty(t, sourceSetup{}.historyDir(options{CompareOld: "a", CompareNew: "b"}), "nor does a compare pair")
}
