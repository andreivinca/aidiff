package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/ui/sidepane"
)

func TestLazyFolderEnterAndCollapse(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src/only.go"), []byte("hello"), 0600))
	reader := diff.NewFolderReader(root, nil, nil)
	m := testModel(nil, nil)
	m.diffRenderer = reader
	entries, err := reader.ChangedFiles("", false)
	require.NoError(t, err)
	model, _ := m.handleFilesLoaded(filesLoadedMsg{seq: m.filesLoadSeq, entries: entries})
	m = model.(Model)
	require.False(t, m.file.singleFile)
	require.Equal(t, "src", m.selectedDirectory())
	// Enter must reach the folder handler, rather than annotation or pane actions.
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	require.NotNil(t, cmd)
	model, _ = m.Update(cmd())
	m = model.(Model)
	assert.Equal(t, "src", m.selectedDirectory(), "expansion preserves the folder cursor")
	assert.Equal(t, []string{"src/only.go"}, m.tree.VisibleFiles())
	assert.False(t, m.file.singleFile, "one loaded file must not hide unopened directories")
	m.tree.Move(sidepane.MotionDown)
	assert.Equal(t, "src/only.go", m.tree.SelectedFile())
	m.tree.Move(sidepane.MotionUp)
	model, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	require.NotNil(t, cmd)
	model, _ = m.Update(cmd())
	m = model.(Model)
	assert.Empty(t, m.tree.VisibleFiles())
	assert.Equal(t, "src", m.selectedDirectory())
}

func TestLazyFolderCollapseKeepsReviewedMarks(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src/file"), []byte("hello"), 0600))
	reader := diff.NewFolderReader(root, nil, nil)
	reader.SetDirectory("src", true)
	entries, err := reader.ChangedFiles("", false)
	require.NoError(t, err)
	m := testModel(nil, nil)
	m.diffRenderer = reader
	m.tree = sidepane.NewFileTree(entries)
	m.tree.SetReviewed("src/file", "fingerprint")
	m.tree.Move(sidepane.MotionFirst)
	model, cmd := m.toggleSelectedDirectory()
	m = model.(Model)
	require.NotNil(t, cmd)
	model, _ = m.Update(cmd())
	m = model.(Model)
	assert.True(t, m.tree.IsReviewed("src/file"))
	assert.Empty(t, m.tree.VisibleFiles())
}
