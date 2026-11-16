package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/keymap"
	"github.com/umputun/revdiff/app/ui/sidepane"
)

// folderTestModel browses root with a FolderTree, listed once.
func folderTestModel(t *testing.T, root string) (Model, *diff.FolderReader) {
	t.Helper()
	reader := diff.NewFolderReader(root, nil, nil)
	m := testModel(nil, nil)
	m.diffRenderer = reader
	m.tree = sidepane.NewFolderTree(reader)
	m.layout.viewport = newDiffViewport(80, 30)
	m.filesLoadSeq++
	return runLoads(t, m, m.loadFiles()), reader
}

func TestFolderEnterOpensAndClosesTheSelectedFolder(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "only.go"), []byte("hello"), 0o600))
	m, _ := folderTestModel(t, root)
	require.False(t, m.file.singleFile)
	require.Equal(t, "src", m.selectedDirectory())
	m.layout.focus = paneTree

	// enter must reach the folder handler, rather than annotation or pane actions
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runLoads(t, next.(Model), cmd)
	assert.Equal(t, "src", m.selectedDirectory(), "opening keeps the cursor on the folder")
	assert.Equal(t, []string{"src/only.go"}, m.tree.VisibleFiles())
	assert.False(t, m.file.singleFile, "one listed file must not hide the folder tree")
	m.tree.Move(sidepane.MotionDown)
	assert.Equal(t, "src/only.go", m.tree.SelectedFile())

	m.tree.Move(sidepane.MotionUp)
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runLoads(t, next.(Model), cmd)
	assert.Empty(t, m.tree.VisibleFiles())
	assert.Equal(t, "src", m.selectedDirectory())
}

func TestFolderScrollActionsOpenAndClose(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "file"), nil, 0o600))
	m, _ := folderTestModel(t, root)

	next, cmd := m.handleTreeAction(keymap.ActionScrollRight)
	m = runLoads(t, next.(Model), cmd)
	assert.Equal(t, []string{"src/file"}, m.tree.VisibleFiles(), "scroll right opens")
	_, cmd = m.handleTreeAction(keymap.ActionScrollRight)
	assert.Nil(t, cmd, "an open folder stays open without a reload")

	next, cmd = m.handleTreeAction(keymap.ActionScrollLeft)
	m = runLoads(t, next.(Model), cmd)
	assert.Empty(t, m.tree.VisibleFiles(), "scroll left closes")
	_, cmd = m.handleTreeAction(keymap.ActionScrollLeft)
	assert.Nil(t, cmd, "a closed folder stays closed without a reload")
}

func TestFolderCollapseKeepsReviewedMarks(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "file"), []byte("hello"), 0o600))
	m, reader := folderTestModel(t, root)
	reader.SetDirectory("src", true)
	m.filesLoadSeq++
	m = runLoads(t, m, m.loadFiles())
	m.tree.SetReviewed("src/file", "fingerprint")
	m.tree.Move(sidepane.MotionFirst)
	m.layout.focus = paneTree

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runLoads(t, next.(Model), cmd)
	assert.Empty(t, m.tree.VisibleFiles())
	assert.True(t, m.tree.IsReviewed("src/file"), "closing a folder only hides its marks")
}

func TestFolderRowsStayOutOfTheReview(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "readme"), nil, 0o600))
	m, _ := folderTestModel(t, root)
	assert.Equal(t, []diff.FileEntry{{Path: "readme"}}, m.review.entries, "folders are not files under review")
	assert.Equal(t, 1, m.tree.TotalFiles())
}

func TestFolderRefreshWithCursorOnFolderReloadsShownFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0o700))
	path := filepath.Join(root, "src", "file")
	require.NoError(t, os.WriteFile(path, []byte("one\n"), 0o600))
	m, reader := folderTestModel(t, root)
	m.reload.applicable = true
	reader.SetDirectory("src", true)
	m.filesLoadSeq++
	m = runLoads(t, m, m.loadFiles())
	m.tree.Move(sidepane.MotionDown)
	next, cmd := m.loadSelectedIfChanged()
	m = runLoads(t, next.(Model), cmd)
	require.Equal(t, "src/file", m.file.name)
	m.tree.Move(sidepane.MotionUp)
	require.Equal(t, "src", m.selectedDirectory())

	require.NoError(t, os.WriteFile(path, []byte("zero\none\n"), 0o600))
	m, cmd = refresh(t, m)
	m = runLoads(t, m, cmd)
	assert.Equal(t, "zero", m.file.lines[0].Content, "the displayed file was reloaded")
	assert.Equal(t, "src", m.selectedDirectory(), "the tree cursor stays on the folder")
	assert.Nil(t, m.refresh.anchor)
	assert.True(t, m.CanRefresh())
}
