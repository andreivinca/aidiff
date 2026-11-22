package main

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/localhist"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3palette"
)

func TestNamesTarget(t *testing.T) {
	parse := func(args ...string) options {
		opts, err := parseArgs(append(noConfigArgs(t), args...))
		require.NoError(t, err)
		return opts
	}
	for _, args := range [][]string{{}, {"--terminal"}, {"--no-terminal"}, {"--wrap", "--theme", "nord"}, {"-I", "app/"}, {"--description", "x"}} {
		assert.False(t, parse(args...).namesTarget(), "%v names nothing to review", args)
	}
	for _, args := range [][]string{{"."}, {"main"}, {"a", "b"}, {"--staged"}, {"--all-files"}, {"--only", "a.go"}, {"--stdin"},
		{"--t3", "local"}, {"--annotations", "a.md"}, {"-o", "out.md"}} {
		assert.True(t, parse(args...).namesTarget(), "%v names a review", args)
	}
}

func TestStartTab_TheSavedCurrentElseTheNextThatCanComeBack(t *testing.T) {
	reach := func(dir string) bool { return dir != "/gone" }
	project := t3palette.Tab{Server: "ssh://devbox", ProjectID: "p1", ProjectTitle: "Sabra"}
	odd := t3palette.Tab{Server: "ftp://old", ProjectID: "p2"}

	dir, tab := startTab([]t3palette.Tab{{Dir: "/a"}, {Dir: "/b"}, project}, 1, reach)
	assert.Equal(t, "/b", dir, "the tab on screen when the last run ended")
	assert.Nil(t, tab)

	dir, tab = startTab([]t3palette.Tab{{Dir: "/a"}, project, {Dir: "/c"}}, 1, reach)
	assert.Empty(t, dir)
	require.NotNil(t, tab)
	assert.Equal(t, project, *tab, "a T3 project: it connects once the program runs")

	dir, _ = startTab([]t3palette.Tab{{Dir: "/a"}, {Dir: "/gone"}, odd, {Dir: "/d"}}, 1, reach)
	assert.Equal(t, "/d", dir, "a folder gone and a server aidiff cannot name are passed by")
	dir, _ = startTab([]t3palette.Tab{{Dir: "/a"}, {Dir: "/b"}, {Dir: "/gone"}}, 2, reach)
	assert.Equal(t, "/a", dir, "past the last tab, from the first")
	dir, _ = startTab([]t3palette.Tab{{Dir: "/a"}, {Dir: "/b"}}, -1, reach)
	assert.Equal(t, "/a", dir, "none on screen: the first")

	dir, tab = startTab([]t3palette.Tab{{Dir: "/gone"}, odd}, 0, reach)
	assert.Empty(t, dir, "none can come back")
	assert.Nil(t, tab)
	dir, tab = startTab(nil, -1, reach)
	assert.Empty(t, dir)
	assert.Nil(t, tab)
}

// startFixture is a scratch home whose tabs.json holds repositories a and b
// (b on screen) and a plain folder c, and a folder elsewhere to start in.
type startFixture struct {
	a, b, c, elsewhere string
	store              localhist.Tabs
}

func newStartFixture(t *testing.T) startFixture {
	t.Helper()
	tabsHome(t)
	root := t.TempDir()
	f := startFixture{a: filepath.Join(root, "a"), b: filepath.Join(root, "b"), c: filepath.Join(root, "c"), elsewhere: filepath.Join(root, "elsewhere")}
	for _, d := range []string{f.a, f.b} {
		require.NoError(t, os.MkdirAll(d, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(d, "f.txt"), []byte("one\n"), 0o600))
		gitIn(t, d, "init", "-q", "-b", "main")
		gitIn(t, d, "add", ".")
		gitIn(t, d, "commit", "-qm", "init")
		require.NoError(t, os.WriteFile(filepath.Join(d, "f.txt"), []byte("two\n"), 0o600))
	}
	require.NoError(t, os.MkdirAll(f.c, 0o750))
	require.NoError(t, os.MkdirAll(f.elsewhere, 0o750))
	f.store = tabsStore()
	require.NoError(t, f.store.Save([]localhist.Tab{
		{Kind: localhist.TabLocal, Dir: f.a}, {Kind: localhist.TabLocal, Dir: f.b}, {Kind: localhist.TabLocal, Dir: f.c},
	}, 1))
	return f
}

// plan is planStart for args, run in dir.
func (f startFixture) plan(t *testing.T, dir string, args ...string) runStart {
	t.Helper()
	t.Chdir(dir)
	opts, err := parseArgs(append(noConfigArgs(t), args...))
	require.NoError(t, err)
	plan, err := planStart(opts, f.store)
	require.NoError(t, err)
	return plan
}

// runPalette builds the palette and split of a run that started as plan
// says, as run does, and returns them with the run's source.
func runPalette(t *testing.T, plan runStart) (*t3palette.Palette, splitview.Model, sourceSetup) {
	t.Helper()
	src, err := plan.source()
	require.NoError(t, err)
	deps := newReviewDeps(plan.opts)
	local, diffs := newLocalDiff(deps, src, ""), &localDiffs{}
	t3p := t3palette.New(t3palette.Config{Local: newLocalConfig(deps, src, diffs, local), Tabs: plan.tabs, StartTab: plan.tab}, nil)
	t.Cleanup(t3p.Close)
	var model tea.Model
	if plan.tab == nil {
		model, err = deps.newModel(src, annotation.NewStore(), "")
		require.NoError(t, err)
	}
	tabs, recent := t3p.StartTabs()
	split := splitview.New(model, splitview.Config{Workspace: t3p.Workspace(), Tabs: tabs, Recent: recent, Sessions: t3p.Sessions(),
		Opening: t3p.Opening()})
	return t3p, split, src
}

func TestPlanStart_PlainRunStartsOnTheSavedTab(t *testing.T) {
	f := newStartFixture(t)
	plan := f.plan(t, f.elsewhere)
	assert.Equal(t, f.b, plan.opts.folder, "the tab on screen when the last run ended, not the folder it was started in")
	assert.Nil(t, plan.tab)

	t3p, split, src := runPalette(t, plan)
	assert.Equal(t, f.b, src.gitRoot, "built as a run started there builds it")
	assert.Equal(t, f.b, t3p.LocalDir(), "on screen")
	assert.Len(t, split.Tabs(), 3, "the saved tabs, and no tab for the folder it was started in")
	assert.Equal(t, split.Tabs()[1], split.Workspace(), "on screen in its place")

	plan = f.plan(t, f.elsewhere, "--terminal")
	assert.Equal(t, f.b, plan.opts.folder, "--terminal names nothing to review")
}

func TestPlanStart_SavedTabGoneFallsBack(t *testing.T) {
	f := newStartFixture(t)
	require.NoError(t, os.RemoveAll(f.b))
	plan := f.plan(t, f.elsewhere)
	assert.Equal(t, f.c, plan.opts.folder, "the next tab that can come back")

	require.NoError(t, os.RemoveAll(f.a))
	require.NoError(t, os.RemoveAll(f.c))
	plan = f.plan(t, f.elsewhere)
	assert.Empty(t, plan.opts.folder, "none can: the working directory, as before")
	assert.Nil(t, plan.tab)
}

func TestPlanStart_SavedT3Tab(t *testing.T) {
	f := newStartFixture(t)
	require.NoError(t, f.store.Save([]localhist.Tab{
		{Kind: localhist.TabLocal, Dir: f.a},
		{Kind: localhist.TabT3, Server: "ssh://devbox", Machine: "devbox", Project: "p1", Title: "Sabra", Thread: "th-1"},
	}, 1))
	plan := f.plan(t, f.elsewhere, "--terminal")
	require.NotNil(t, plan.tab, "a T3 project on screen: it opens once its server answers")
	assert.Equal(t, t3palette.Tab{Server: "ssh://devbox", Machine: "devbox", ProjectID: "p1", ProjectTitle: "Sabra", ThreadID: "th-1"}, plan.tab.Tab)
	assert.True(t, plan.tab.Terminal, "--terminal opens one with it once it is on screen")
	assert.Equal(t, f.elsewhere, plan.tab.Fallback, "with nothing left, the working directory")
	assert.Empty(t, plan.opts.folder)

	t3p, split, src := runPalette(t, plan)
	assert.Nil(t, src.renderer, "nothing to set up before its server answers")
	assert.True(t, t3p.Opening(), "the split shows it opening")
	assert.Equal(t, split.Tabs()[1], split.Workspace(), "on screen in its place")
	assert.Len(t, split.Tabs(), 2, "and no tab for the folder it was started in")
	assert.Contains(t, split.View(), "Sabra", "its stand-in names it")
}

func TestPlanStart_FolderArgument(t *testing.T) {
	f := newStartFixture(t)
	plan := f.plan(t, f.b, ".")
	assert.Equal(t, f.b, plan.opts.folder)
	_, split, _ := runPalette(t, plan)
	assert.Len(t, split.Tabs(), 3, "a saved tab: its own")
	assert.Equal(t, split.Tabs()[1], split.Workspace(), "on screen, in its place")

	plan = f.plan(t, f.elsewhere, ".")
	assert.Equal(t, f.elsewhere, plan.opts.folder)
	t3p, split, _ := runPalette(t, plan)
	require.Len(t, split.Tabs(), 4, "a folder not saved: appended")
	assert.Equal(t, split.Tabs()[3], split.Workspace(), "and on screen")
	assert.Equal(t, f.elsewhere, t3p.LocalDir())

	plan = f.plan(t, f.elsewhere, f.a)
	assert.Equal(t, f.a, plan.opts.folder, "a folder named by its path")
}

func TestPlanStart_TargetOrNoTabsAsBefore(t *testing.T) {
	f := newStartFixture(t)
	plan := f.plan(t, f.b, "HEAD")
	assert.Empty(t, plan.opts.folder, "a ref reviews the folder the run is in, as before")
	assert.Equal(t, "HEAD", plan.opts.ref())
	_, split, src := runPalette(t, plan)
	assert.Equal(t, f.b, src.gitRoot)
	assert.Len(t, split.Tabs(), 3, "the saved tabs around it")

	plan = f.plan(t, f.b, "--staged")
	assert.Empty(t, plan.opts.folder)

	require.NoError(t, os.Remove(f.store.Path))
	plan = f.plan(t, f.elsewhere)
	assert.Empty(t, plan.opts.folder, "no saved tabs: the working directory")
	assert.Nil(t, plan.tab)

	require.NoError(t, os.WriteFile(f.store.Path, []byte("{broken"), 0o600))
	plan = f.plan(t, f.elsewhere)
	assert.Empty(t, plan.opts.folder, "a corrupt file: the working directory")
	assert.Nil(t, plan.tab)
	t3p, _, _ := runPalette(t, plan)
	assert.Equal(t, f.elsewhere, t3p.LocalDir())
}
