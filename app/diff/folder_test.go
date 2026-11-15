package diff

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFolderReaderLazyBrowsing(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"src/deep", ".git/objects", ".hidden"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o700))
	}
	for _, file := range []string{"readme", "src/main.go", "src/deep/file"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, file), []byte("hello\n"), 0o600))
	}
	require.NoError(t, os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "link")))
	r := NewFolderReader(root, nil, nil)
	var watched []string
	r.SetDirectoryWatcher(func(dirs []string) { watched = dirs })
	entries, err := r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []FileEntry{{Path: ".hidden", IsDir: true}, {Path: "link"}, {Path: "readme"}, {Path: "src", IsDir: true}}, entries)
	assert.Equal(t, []string{root}, watched)
	r.SetDirectory("src", true)
	entries, err = r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Contains(t, entries, FileEntry{Path: "src/deep", IsDir: true})
	assert.Contains(t, entries, FileEntry{Path: "src/main.go"})
	assert.NotContains(t, FileEntryPaths(entries), "src/deep/file")
	assert.ElementsMatch(t, []string{root, filepath.Join(root, "src")}, watched)
	r.SetDirectory("src/deep", true)
	entries, err = r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Contains(t, FileEntryPaths(entries), "src/deep/file")
	r.SetDirectory("src", false)
	_, err = r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []string{root}, watched)
	r.SetDirectory("src", true)
	entries, err = r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.NotContains(t, FileEntryPaths(entries), "src/deep/file", "closed descendants stay closed when the parent reopens")
}

func TestFolderReaderFiltersKeepAncestors(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src", "deep"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "deep", "file"), nil, 0o600))
	r := NewFolderReader(root, []string{"src/deep/file"}, []string{"src/ignored"})
	entries, err := r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []FileEntry{{Path: "src", IsDir: true}}, entries)
	r.SetDirectory("src", true)
	entries, err = r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Contains(t, entries, FileEntry{Path: "src/deep", IsDir: true})
}

func TestFolderReaderDoesNotOpenUnreadableChildren(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "unreadable")
	require.NoError(t, os.Mkdir(blocked, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(blocked, "private"), nil, 0o600))
	require.NoError(t, os.Chmod(blocked, 0))
	defer func() { require.NoError(t, os.Chmod(blocked, 0o700)) }() //nolint:gosec // restore directory access for test cleanup
	entries, err := NewFolderReader(root, nil, nil).ChangedFiles("", false)
	require.NoError(t, err, "an unopened unreadable directory must not prevent browsing its parent")
	assert.Equal(t, []FileEntry{{Path: "unreadable", IsDir: true}}, entries)
}
