package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// changesModel is a review of files, its file list loaded.
func changesModel(t *testing.T, files ...string) Model {
	t.Helper()
	m := testModel(files, nil)
	m.layout.viewport = newDiffViewport(80, 30)
	m.filesLoadSeq++
	return runLoads(t, m, m.loadFiles())
}

func TestOpenFileSelectsAListedFile(t *testing.T) {
	m := changesModel(t, "a.go", "src/b.go", "src/c.go")
	require.Equal(t, "a.go", m.file.name)
	m.layout.focus = paneTree

	next, cmd := m.Update(OpenFileMsg{Path: "src/c.go", Focus: true})
	m = runLoads(t, next.(Model), cmd)
	assert.Equal(t, "src/c.go", m.tree.SelectedFile())
	assert.Equal(t, "src/c.go", m.file.name, "the file is shown")
	assert.Equal(t, paneDiff, m.layout.focus, "Focus moves to the diff")

	next, cmd = m.Update(OpenFileMsg{Path: "missing.go"})
	m = runLoads(t, next.(Model), cmd)
	assert.Equal(t, "src/c.go", m.file.name, "a file the review does not list changes nothing")
	assert.Empty(t, m.openFile.Path, "and is not kept for later")
}

func TestOpenFileWaitsForTheFileList(t *testing.T) {
	m := testModel([]string{"a.go", "b.go"}, nil)
	m.filesLoaded = false // as a new review starts
	m.layout.viewport = newDiffViewport(80, 30)
	m.layout.focus = paneTree
	next, cmd := m.Update(OpenFileMsg{Path: "b.go"})
	require.Nil(t, cmd)
	m = next.(Model)

	m.filesLoadSeq++
	m = runLoads(t, m, m.loadFiles())
	assert.Equal(t, "b.go", m.file.name, "the first list selects the file instead of the first one")
	assert.Equal(t, paneTree, m.layout.focus, "the focus stays without Focus")

	m = testModel([]string{"a.go"}, nil)
	m.filesLoaded = false
	m.layout.viewport = newDiffViewport(80, 30)
	next, _ = m.Update(OpenFileMsg{Path: "gone.go"})
	m = next.(Model)
	m.filesLoadSeq++
	m = runLoads(t, m, m.loadFiles())
	assert.Equal(t, "a.go", m.file.name, "a file not in the list leaves the first one")
}

func TestOpenFileOpensTheFoldersAboveIt(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src", "deep"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "top.txt"), []byte("top"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "deep", "x.go"), []byte("x"), 0o600))
	m, _ := folderTestModel(t, root)
	require.NotContains(t, m.tree.VisibleFiles(), "src/deep/x.go")

	next, cmd := m.Update(OpenFileMsg{Path: "src/deep/x.go", Focus: true})
	m = runLoads(t, next.(Model), cmd)
	assert.Contains(t, m.tree.VisibleFiles(), "src/deep/x.go", "its folders are open")
	assert.Equal(t, "src/deep/x.go", m.tree.SelectedFile())
	assert.Equal(t, "src/deep/x.go", m.file.name)
}

func TestFindFiles(t *testing.T) {
	m := changesModel(t, "README.md", "app/main.go", "app/ui/model.go")
	found, err := m.FindFiles("main", 10)(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"app/main.go"}, found.Paths, "a review of changes ranks its own files")
	found, err = m.FindFiles("", 2)(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"README.md", "app/main.go"}, found.Paths[:2])
	assert.True(t, found.Truncated)

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "closed"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "closed", "needle.txt"), nil, 0o600))
	fm, _ := folderTestModel(t, root)
	found, err = fm.FindFiles("needle", 10)(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"closed/needle.txt"}, found.Paths, "a folder browser finds files of closed folders")
}
