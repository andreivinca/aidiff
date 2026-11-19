package main

import (
	"context"
	"errors"
	"testing"

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

func TestPaletteFiles(t *testing.T) {
	assert.Nil(t, paletteFiles(nil, "x"), "nothing to search without a review")

	var query string
	r := fakeReview{found: diff.FoundPaths{Paths: []string{"app/ui/model.go", "go.mod"}, Truncated: true}, query: &query}
	msg, ok := paletteFiles(r, "mod")().(splitview.FilesMsg)
	require.True(t, ok)
	assert.Equal(t, "mod", query)
	assert.Equal(t, "mod", msg.Query)
	assert.Equal(t, []splitview.PickerItem{{Label: "model.go", Detail: "app/ui", Value: "app/ui/model.go"}, {Label: "go.mod", Value: "go.mod"}}, msg.Items,
		"the name, its folder beside it")
	assert.Contains(t, msg.Note, "first 2 matches")
	assert.Equal(t, ui.OpenFileMsg{Path: "app/ui/model.go", Focus: true}, msg.OnPick(msg.Items[0])())

	r.err = errors.New("search files: offline")
	msg, _ = paletteFiles(r, "mod")().(splitview.FilesMsg)
	assert.Empty(t, msg.Items)
	assert.Equal(t, "search files: offline", msg.Note)
}

func TestCarryFile(t *testing.T) {
	assert.Nil(t, carryFile(fakeReview{}, nil), "no file on screen, nothing to carry")
	cmd := carryFile(fakeReview{file: "src/a.go"}, nil)
	require.NotNil(t, cmd)
	assert.Equal(t, ui.OpenFileMsg{Path: "src/a.go"}, cmd(), "selected in the new review, the focus left as it is")
}
