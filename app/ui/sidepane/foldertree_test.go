package sidepane

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/ui/style"
)

// fakeFolderSource records the folders a FolderTree opens and closes.
type fakeFolderSource struct {
	set map[string]bool
}

func (f *fakeFolderSource) SetDirectory(path string, expanded bool) {
	if f.set == nil {
		f.set = make(map[string]bool)
	}
	f.set[path] = expanded
}

// listFolder feeds a listing to the tree the way the model does: folder rows
// through SetFolders, files through Rebuild.
func listFolder(tree *FolderTree, entries []diff.FileEntry) {
	var folders, files []diff.FileEntry
	for _, e := range entries {
		if e.IsDir {
			folders = append(folders, e)
		} else {
			files = append(files, e)
		}
	}
	tree.SetFolders(folders)
	tree.Rebuild(files)
}

func TestFolderTree_StartsAboveFilesAndPreservesSelection(t *testing.T) {
	entries := make([]diff.FileEntry, 0, 41)
	for i := range 40 {
		entries = append(entries, diff.FileEntry{Path: fmt.Sprintf("folder-%02d", i), IsDir: true})
	}
	entries = append(entries, diff.FileEntry{Path: "readme.txt"})
	tree := NewFolderTree(&fakeFolderSource{})
	listFolder(tree, entries)
	tree.EnsureVisible(10)
	dir, expanded := tree.SelectedDirectory()
	assert.Equal(t, "folder-00", dir)
	assert.False(t, expanded)
	assert.Empty(t, tree.SelectedFile(), "a folder row is not a file")
	assert.Equal(t, 0, tree.ScrollState().Offset, "the files must not scroll the folders out of view")

	require.True(t, tree.SelectByPath("readme.txt"))
	tree.EnsureVisible(10)
	before := tree.ScrollState().Offset
	listFolder(tree, entries)
	tree.EnsureVisible(10)
	assert.Equal(t, "readme.txt", tree.SelectedFile(), "a reload keeps the selected file")
	assert.Equal(t, before, tree.ScrollState().Offset, "at the same row")
}

func TestFolderTree_RowsAndNavigation(t *testing.T) {
	tree := NewFolderTree(&fakeFolderSource{})
	listFolder(tree, []diff.FileEntry{
		{Path: "b.txt"},
		{Path: "src", IsDir: true, Expanded: true},
		{Path: "src/main.go"},
		{Path: "src/deep", IsDir: true},
		{Path: "a.txt"},
	})
	res := style.PlainResolver()
	out := ansi.Strip(tree.Render(FileTreeRender{Width: 40, Height: 10, Resolver: res, Renderer: style.NewRenderer(res)}))
	rows := strings.Split(out, "\n")
	require.Len(t, rows, 5)
	assert.Contains(t, rows[0], "▾ src/", "folders come first")
	assert.Contains(t, rows[1], "  ▸ deep/", "children are indented below their folder")
	assert.Contains(t, rows[2], "main.go")
	assert.Contains(t, rows[3], "a.txt")
	assert.Contains(t, rows[4], "b.txt")

	tree.Move(MotionDown)
	dir, expanded := tree.SelectedDirectory()
	assert.Equal(t, "src/deep", dir, "the cursor stops on folder rows")
	assert.False(t, expanded)
	tree.Move(MotionLast)
	assert.Equal(t, "b.txt", tree.SelectedFile())
	tree.Move(MotionPageUp, 3)
	dir, _ = tree.SelectedDirectory()
	assert.Equal(t, "src/deep", dir, "a page counts folder rows too")
	tree.StepFile(DirectionNext)
	assert.Equal(t, "src/main.go", tree.SelectedFile(), "file stepping skips folders")
}

func TestFolderTree_SetDirectoryGoesToTheSource(t *testing.T) {
	source := &fakeFolderSource{}
	tree := NewFolderTree(source)
	tree.SetDirectory("src", true)
	assert.Equal(t, map[string]bool{"src": true}, source.set)
}

func TestFolderTree_ClosedFolderKeepsReviewedMarks(t *testing.T) {
	tree := NewFolderTree(&fakeFolderSource{})
	listFolder(tree, []diff.FileEntry{{Path: "src", IsDir: true, Expanded: true}, {Path: "src/file"}, {Path: "gone"}})
	tree.SetReviewed("src/file", "fp")
	tree.SetReviewed("gone", "fp")

	listFolder(tree, []diff.FileEntry{{Path: "src", IsDir: true}})
	tree.ReconcileReviewed(map[string]string{"src/file": "fp"}, map[string]string{})
	assert.True(t, tree.IsReviewed("src/file"), "hidden under a closed folder, not removed")
	assert.False(t, tree.IsReviewed("gone"), "a file that left the listing loses its mark")
}

func TestFolderTree_FilterShowsFilesThenRestoresFolders(t *testing.T) {
	tree := NewFolderTree(&fakeFolderSource{})
	listFolder(tree, []diff.FileEntry{{Path: "src", IsDir: true, Expanded: true}, {Path: "src/a"}, {Path: "src/b"}})
	tree.ToggleFilter(map[string]bool{"src/b": true})
	require.True(t, tree.FilterActive())
	assert.Equal(t, "src/b", tree.SelectedFile())
	dir, _ := tree.SelectedDirectory()
	assert.Empty(t, dir, "no folder rows while filtered")

	tree.ToggleFilter(nil)
	assert.Equal(t, []string{"src/a", "src/b"}, tree.VisibleFiles())
	tree.Move(MotionFirst)
	dir, _ = tree.SelectedDirectory()
	assert.Equal(t, "src", dir, "the folder rows are back")
}

func TestFolderTree_EmptyFolder(t *testing.T) {
	tree := NewFolderTree(&fakeFolderSource{})
	listFolder(tree, nil)
	res := style.PlainResolver()
	assert.Equal(t, "  empty folder", tree.Render(FileTreeRender{Width: 40, Height: 10, Resolver: res, Renderer: style.NewRenderer(res)}))
	tree.Move(MotionDown)
	assert.Empty(t, tree.SelectedFile())
}

func TestFolderTree_FolderNamesAreSanitized(t *testing.T) {
	tree := NewFolderTree(&fakeFolderSource{})
	listFolder(tree, []diff.FileEntry{
		{Path: "bad\nname\x1b[31m", IsDir: true},
		{Path: "a.txt"},
	})
	res := style.PlainResolver()
	out := tree.Render(FileTreeRender{Width: 40, Height: 10, Resolver: res, Renderer: style.NewRenderer(res)})
	rows := strings.Split(out, "\n")
	require.Len(t, rows, 2, "one row per entry")
	assert.Contains(t, rows[0], "badname[31m/")
	assert.NotContains(t, out, "\x1b[31m", "no escape from the name reaches the terminal")
}
