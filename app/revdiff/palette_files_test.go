package main

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/ui"
)

// fakeReview finds found (or fails with err) and shows file.
type fakeReview struct {
	tea.Model
	file  string
	found diff.FoundPaths
	err   error
	query *string
}

func (f fakeReview) FindFiles(query string, limit int) func(context.Context) (diff.FoundPaths, error) {
	*f.query = query
	return func(context.Context) (diff.FoundPaths, error) { return f.found, f.err }
}

func (f fakeReview) CurrentFile() string { return f.file }

// changesReview is a fakeReview that lists the changed files.
type changesReview struct{ fakeReview }

func (changesReview) ListsChanges() bool { return true }

// fakeProject finds paths in the whole project.
type fakeProject struct {
	paths []string
	err   error
}

func (p fakeProject) SearchFiles(_ context.Context, query string, limit int) (diff.FoundPaths, error) {
	return diff.RankPaths(query, p.paths, limit), p.err
}

func TestPaletteFiles(t *testing.T) {
	f := &paletteFinder{}
	assert.Nil(t, f.files(nil, "x"), "nothing to search without a review")

	var query string
	r := fakeReview{found: diff.FoundPaths{Paths: []string{"app/ui/model.go", "go.mod"}, Truncated: true}, query: &query}
	msg, ok := f.files(r, "mod")().(splitview.FilesMsg)
	require.True(t, ok)
	assert.Equal(t, "mod", query)
	assert.Equal(t, "mod", msg.Query)
	assert.Equal(t, []splitview.PickerItem{{Label: "model.go", Detail: "app/ui", Value: "app/ui/model.go"}, {Label: "go.mod", Value: "go.mod"}}, msg.Items,
		"the name, its folder beside it")
	assert.Contains(t, msg.Note, "first 2 matches")
	assert.Equal(t, ui.OpenFileMsg{Path: "app/ui/model.go", Focus: true}, msg.OnPick(msg.Items[0])())

	r.err = errors.New("search files: offline")
	msg, _ = f.files(r, "mod")().(splitview.FilesMsg)
	assert.Empty(t, msg.Items)
	assert.Equal(t, "search files: offline", msg.Note)
}

func TestPaletteFiles_ChangedFirstThenTheProject(t *testing.T) {
	var query string
	shown := 0
	proj := fakeProject{paths: []string{"app/ui/model.go", "app/ui/model_test.go", "README.md"}}
	f := &paletteFinder{project: func(tea.Model) projectFiles {
		return projectFiles{search: proj, show: func() tea.Cmd { shown++; return func() tea.Msg { return "switched" } }}
	}}
	r := changesReview{fakeReview{found: diff.FoundPaths{Paths: []string{"app/ui/model.go"}}, query: &query}}

	msg, _ := f.files(r, "model")().(splitview.FilesMsg)
	assert.Equal(t, []splitview.PickerItem{
		{Label: "model.go", Detail: "app/ui", Value: "app/ui/model.go"},
		{Label: "model_test.go", Detail: "app/ui", Value: projectPick("app/ui/model_test.go"), Group: projectGroup},
	}, msg.Items, "the changed file on top, the project's others under the rule, none twice")

	assert.Equal(t, "switched", msg.OnPick(msg.Items[1])(), "a project file switches to the file tree")
	assert.Equal(t, 1, shown)
	assert.Equal(t, ui.OpenFileMsg{Path: "app/ui/model_test.go", Focus: true}, f.carry(fakeReview{file: "app/ui/model.go"}, nil)(),
		"and is shown there, not the file the review showed")
	assert.Equal(t, ui.OpenFileMsg{Path: "app/ui/model.go"}, f.carry(fakeReview{file: "app/ui/model.go"}, nil)(), "once")

	r.found = diff.FoundPaths{}
	msg, _ = f.files(r, "readme")().(splitview.FilesMsg)
	assert.Equal(t, []splitview.PickerItem{{Label: noChangedMatch}, {Label: "README.md", Value: projectPick("README.md"), Group: projectGroup}}, msg.Items,
		"no changed file: a row says so, the project's still under the rule")
	assert.Nil(t, msg.OnPick(msg.Items[0]))

	msg, _ = f.files(r, "")().(splitview.FilesMsg)
	assert.Empty(t, msg.Items, "an empty query: no project search")

	proj.err = errors.New("offline")
	r.found = diff.FoundPaths{Paths: []string{"app/ui/model.go"}}
	msg, _ = f.files(r, "model")().(splitview.FilesMsg)
	assert.Len(t, msg.Items, 1, "the changed files still list")
	assert.Equal(t, "project: offline", msg.Note)

	browser := fakeReview{found: diff.FoundPaths{Paths: []string{"a.go"}}, query: &query}
	msg, _ = f.files(browser, "a")().(splitview.FilesMsg)
	assert.Len(t, msg.Items, 1, "a folder browser searches its own folder only")
}

func TestCarryFile(t *testing.T) {
	f := &paletteFinder{}
	assert.Nil(t, f.carry(fakeReview{}, nil), "no file on screen, nothing to carry")
	cmd := f.carry(fakeReview{file: "src/a.go"}, nil)
	require.NotNil(t, cmd)
	assert.Equal(t, ui.OpenFileMsg{Path: "src/a.go"}, cmd(), "selected in the new review, the focus left as it is")

	f.next, f.nextAt = "old.go", time.Now().Add(-time.Hour)
	assert.Equal(t, ui.OpenFileMsg{Path: "src/a.go"}, f.carry(fakeReview{file: "src/a.go"}, nil)(), "a pick whose switch failed long ago expired")
}
