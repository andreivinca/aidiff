package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/ui/mocks"
)

// runLoads executes cmd and feeds the load results it produces back through
// Update, following the commands they return, and returns the settled model.
// Only loader messages are fed: these tests issue no timers.
func runLoads(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = runLoads(t, m, c)
		}
	case filesLoadedMsg, fileLoadedMsg, commitsLoadedMsg, blameLoadedMsg, reviewFingerprintLoadedMsg:
		next, nextCmd := m.Update(msg)
		m = runLoads(t, next.(Model), nextCmd)
	}
	return m
}

// contextLines builds a full-context file from its lines.
func contextLines(lines ...string) []diff.DiffLine {
	out := make([]diff.DiffLine, len(lines))
	for i, content := range lines {
		out[i] = diff.DiffLine{OldNum: i + 1, NewNum: i + 1, Content: content, ChangeType: diff.ChangeContext}
	}
	return out
}

// refreshTestModel loads files with the first one displayed and refreshing enabled.
func refreshTestModel(t *testing.T, files []string, diffs map[string][]diff.DiffLine) Model {
	t.Helper()
	m := testModel(files, diffs)
	m.reload.applicable = true
	m.layout.viewport = newDiffViewport(80, 30)
	m = runLoads(t, m, m.Init())
	require.Equal(t, files[0], m.file.name)
	require.True(t, m.CanRefresh())
	return m
}

func refresh(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(RefreshMsg{})
	return next.(Model), cmd
}

func TestRefresh_KeepsListOnScreenAndBlocksWhileLoading(t *testing.T) {
	diffs := map[string][]diff.DiffLine{"a.go": contextLines("one", "two"), "b.go": contextLines("bee")}
	m := refreshTestModel(t, []string{"a.go", "b.go"}, diffs)

	m, cmd := refresh(t, m)
	require.NotNil(t, cmd)
	assert.False(t, m.filesLoaded, "the reload is a real one: the file list is loading")
	assert.False(t, m.CanRefresh(), "no second refresh while one is loading")
	view := ansi.Strip(m.View())
	assert.NotContains(t, view, "loading files...")
	assert.Contains(t, view, "b.go", "the current list stays on screen")

	m = runLoads(t, m, cmd)
	assert.True(t, m.CanRefresh(), "the refresh finished")
	assert.Equal(t, "a.go", m.file.name)
}

func TestRefresh_ReloadKeyStillShowsLoading(t *testing.T) {
	m := refreshTestModel(t, []string{"a.go", "b.go"}, map[string][]diff.DiffLine{"a.go": contextLines("one")})
	m, _ = refresh(t, m)
	m.triggerReload() // R during a refresh: the refresh's list no longer loads
	assert.Equal(t, "loading files...", m.View())
}

func TestRefresh_WaitsForWorkInProgress(t *testing.T) {
	m := refreshTestModel(t, []string{"a.go", "b.go"}, map[string][]diff.DiffLine{"a.go": contextLines("one")})
	tests := []struct {
		name  string
		setup func(m *Model)
	}{
		{name: "file selection loading", setup: func(m *Model) {
			m.file.requestedPath = "b.go"
		}},
		{name: "mark reviewed pending", setup: func(m *Model) {
			m.reviewed.pending["a.go"] = 1
		}},
		{name: "cross-file hunk jump", setup: func(m *Model) {
			m.nav.pendingHunkJump = &hunkJump{forward: true}
		}},
		{name: "cross-file motion", setup: func(m *Model) {
			m.nav.pendingBoundaryJump = &boundaryJump{forward: true}
		}},
		{name: "annotation list jump", setup: func(m *Model) {
			m.pendingAnnotJump = &annotation.Annotation{File: "b.go"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			busy := m
			busy.reviewed.pending = make(map[string]uint64)
			tt.setup(&busy)
			assert.False(t, busy.CanRefresh())
			_, cmd := refresh(t, busy)
			assert.Nil(t, cmd, "a refresh that cannot be applied does nothing")
		})
	}
}

func TestRefresh_SelectionInFlightSurvives(t *testing.T) {
	diffs := map[string][]diff.DiffLine{"a.go": contextLines("one"), "b.go": contextLines("bee")}
	m := refreshTestModel(t, []string{"a.go", "b.go"}, diffs)
	m.layout.focus = paneTree
	next, loadB := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = next.(Model)
	require.Equal(t, "b.go", m.tree.SelectedFile())
	require.NotNil(t, loadB)

	m, cmd := refresh(t, m) // the watcher fires before b.go arrives: held
	assert.Nil(t, cmd)
	m = runLoads(t, m, loadB)
	assert.Equal(t, "b.go", m.file.name)
	assert.Equal(t, "b.go", m.tree.SelectedFile())
}

func TestRefresh_KeepsCursorLineAndViewState(t *testing.T) {
	wide := "\treturn target // " + strings.Repeat("wide ", 40) // wider than the pane, so it scrolls
	lines := []string{"func a() {", "}", "", "func b() {", wide, "}", ""}
	diffs := map[string][]diff.DiffLine{"a.go": contextLines(lines...), "b.go": contextLines("bee")}
	m := refreshTestModel(t, []string{"a.go", "b.go"}, diffs)
	m.nav.diffCursor = 4
	m.search.term = "target"
	m.search.matches = []int{4}
	m.layout.scrollX = 3

	diffs["a.go"] = contextLines(append([]string{"// header", ""}, lines...)...) // two lines above the cursor
	m, cmd := refresh(t, m)
	m = runLoads(t, m, cmd)

	assert.Equal(t, 6, m.nav.diffCursor, "the cursor follows its line down")
	assert.Equal(t, "target", m.search.term, "the search survives")
	assert.Equal(t, []int{6}, m.search.matches, "and matches the reloaded lines")
	assert.Equal(t, 3, m.layout.scrollX, "the horizontal scroll survives")
}

func TestRefresh_FindsMovedCommonLineBelow(t *testing.T) {
	// "}" appears above and below the cursor; after one line is added above,
	// the cursor's "}" is one line down, not the one above it.
	lines := []string{"a {", "}", "b {", "}", "c"}
	diffs := map[string][]diff.DiffLine{"a.go": contextLines(lines...), "b.go": contextLines("bee")}
	m := refreshTestModel(t, []string{"a.go", "b.go"}, diffs)
	m.nav.diffCursor = 3

	diffs["a.go"] = contextLines(append([]string{"new"}, lines...)...)
	m, cmd := refresh(t, m)
	m = runLoads(t, m, cmd)
	assert.Equal(t, 4, m.nav.diffCursor)
}

func TestRefresh_KeepsExpandedHunks(t *testing.T) {
	changed := func(prefix ...string) []diff.DiffLine {
		out := contextLines(prefix...)
		n := len(out)
		return append(out,
			diff.DiffLine{OldNum: n + 1, Content: "old", ChangeType: diff.ChangeRemove},
			diff.DiffLine{NewNum: n + 1, Content: "new", ChangeType: diff.ChangeAdd},
		)
	}
	diffs := map[string][]diff.DiffLine{"a.go": changed("ctx"), "b.go": contextLines("bee")}
	m := refreshTestModel(t, []string{"a.go", "b.go"}, diffs)
	m.modes.collapsed.enabled = true
	m.modes.collapsed.expandedHunks[1] = true

	diffs["a.go"] = changed("added", "ctx")
	m, cmd := refresh(t, m)
	m = runLoads(t, m, cmd)
	assert.Equal(t, map[int]bool{2: true}, m.modes.collapsed.expandedHunks, "the hunk stays expanded where it moved")
}

func TestRefresh_AnchorOnlyAppliesToTheDisplayedFile(t *testing.T) {
	diffs := map[string][]diff.DiffLine{"a.go": contextLines("one", "two", "three"), "b.go": contextLines("b1", "b2", "b3")}
	m := refreshTestModel(t, []string{"a.go", "b.go"}, diffs)
	m.nav.diffCursor = 2

	// a.go is gone: the reload lands on b.go, which must open at the top
	m.diffRenderer = &mocks.RendererMock{
		ChangedFilesFunc: func(string, bool) ([]diff.FileEntry, error) {
			return []diff.FileEntry{{Path: "b.go"}}, nil
		},
		FileDiffFunc: func(req diff.FileDiffRequest) ([]diff.DiffLine, error) {
			return diffs[req.Path], nil
		},
	}
	m, cmd := refresh(t, m)
	m = runLoads(t, m, cmd)
	assert.Equal(t, "b.go", m.file.name)
	assert.Equal(t, 0, m.nav.diffCursor)
	assert.Nil(t, m.refresh.anchor)
}

func TestRefresh_ReloadsShownFileWhenTreeSelectsNone(t *testing.T) {
	diffs := map[string][]diff.DiffLine{"a.go": contextLines("one", "two")}
	m := refreshTestModel(t, []string{"a.go", "b.go"}, diffs)
	m.nav.diffCursor = 1
	m.tree.SetReviewed("a.go", "fp")
	m.tree.SetReviewed("b.go", "fp")
	m.tree.ToggleUnreviewedFilter() // every file reviewed: the tree selects none
	require.Empty(t, m.tree.SelectedFile())

	diffs["a.go"] = contextLines("zero", "one", "two")
	m, cmd := refresh(t, m)
	m = runLoads(t, m, cmd)
	assert.Equal(t, "zero", m.file.lines[0].Content, "the displayed file was reloaded")
	assert.Equal(t, 2, m.nav.diffCursor)
	assert.True(t, m.CanRefresh())
}

func TestNearestLine_PrefersBelowAtEqualDistance(t *testing.T) {
	m := testModel(nil, nil)
	m.file.lines = contextLines("x", "y", "x")
	match := func(i int) bool {
		return m.file.lines[i].Content == "x"
	}
	assert.Equal(t, 2, m.nearestLine(1, match))
	assert.Equal(t, -1, m.nearestLine(1, func(int) bool {
		return false
	}))
	assert.Equal(t, 0, m.nearestLine(0, match))
}

func keyRunes(t *testing.T, m Model, keys string) Model {
	t.Helper()
	for _, r := range keys {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	return m
}

func TestRefresh_KeepsCursorMovedWhileLoading(t *testing.T) {
	lines := []string{"l0", "l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9"}
	diffs := map[string][]diff.DiffLine{"a.go": contextLines(lines...), "b.go": contextLines("bee")}
	m := refreshTestModel(t, []string{"a.go", "b.go"}, diffs)
	m.layout.focus = paneDiff
	m.nav.diffCursor = 3

	m, cmd := refresh(t, m)
	m = keyRunes(t, m, "jjjjj") // moved while the reload runs
	require.Equal(t, 8, m.nav.diffCursor)
	m = runLoads(t, m, cmd)
	assert.Equal(t, 8, m.nav.diffCursor, "the reload does not undo the moves")
}

func TestRefresh_AnnotationStartedWhileLoadingKeepsItsLine(t *testing.T) {
	lines := []string{"l0", "l1", "l2", "l3", "l4", "l5", "l6"}
	diffs := map[string][]diff.DiffLine{"a.go": contextLines(lines...), "b.go": contextLines("bee")}
	m := refreshTestModel(t, []string{"a.go", "b.go"}, diffs)
	m.layout.focus = paneDiff
	m.nav.diffCursor = 1

	diffs["a.go"] = contextLines(append([]string{"new0", "new1"}, lines...)...) // two lines above, written meanwhile
	m, cmd := refresh(t, m)
	m = keyRunes(t, m, "jjjja") // to l5, then annotate it while the reload runs
	require.True(t, m.annot.annotating)
	m = keyRunes(t, m, "note")
	m = runLoads(t, m, cmd)

	require.True(t, m.annot.annotating, "the annotation being typed survives the refresh")
	assert.Equal(t, "note", m.annot.input.Value())
	assert.Equal(t, "l5", m.file.lines[m.nav.diffCursor].Content, "on the line it was started on")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	got := m.store.Get("a.go")
	require.Len(t, got, 1)
	assert.Equal(t, 8, got[0].Line, "l5's line number after the reload")
}

func TestRefresh_KeepsCursorOnFileAnnotation(t *testing.T) {
	diffs := map[string][]diff.DiffLine{"a.go": contextLines("one", "two", "three"), "b.go": contextLines("bee")}
	m := refreshTestModel(t, []string{"a.go", "b.go"}, diffs)
	m.store.Add(annotation.Annotation{File: "a.go", Line: 0, Comment: "file note"})
	m.nav.diffCursor = -1
	m.search.term = "two"
	m.search.matches = []int{1}

	diffs["a.go"] = contextLines("zero", "one", "two", "three")
	m, cmd := refresh(t, m)
	m = runLoads(t, m, cmd)
	assert.Equal(t, -1, m.nav.diffCursor, "still on the file-level annotation")
	assert.Equal(t, "two", m.search.term, "the search survives")
	assert.Equal(t, []int{2}, m.search.matches)
}

func TestRefresh_KeepsCursorOnLineAnnotation(t *testing.T) {
	diffs := map[string][]diff.DiffLine{"a.go": contextLines("one", "two", "three"), "b.go": contextLines("bee")}
	m := refreshTestModel(t, []string{"a.go", "b.go"}, diffs)
	m.store.Add(annotation.Annotation{File: "a.go", Line: 2, Type: " ", Comment: "note"})
	m.nav.diffCursor = 1
	m.annot.cursorOnAnnotation = true

	diffs["a.go"] = contextLines("one", "two", "three", "four") // a line below
	m, cmd := refresh(t, m)
	m = runLoads(t, m, cmd)
	assert.Equal(t, 1, m.nav.diffCursor)
	assert.True(t, m.annot.cursorOnAnnotation, "still on the annotation, not its line")
}
