package diff

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
	r.SetDirectoryWatcher(func(dirs []string) {
		watched = dirs
	})
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
	defer func() {
		require.NoError(t, os.Chmod(blocked, 0o700)) //nolint:gosec // restore directory access for test cleanup
	}()
	entries, err := NewFolderReader(root, nil, nil).ChangedFiles("", false)
	require.NoError(t, err, "an unopened unreadable directory must not prevent browsing its parent")
	assert.Equal(t, []FileEntry{{Path: "unreadable", IsDir: true}}, entries)
}

func TestFolderReaderClosesUnreadableOpenFolder(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(root, "gone")
	require.NoError(t, os.Mkdir(gone, 0o700))
	r := NewFolderReader(root, nil, nil)
	r.SetDirectory("gone", true)
	// the parent still lists the open folder, but reading it fails
	require.NoError(t, os.Chmod(gone, 0))
	defer func() {
		require.NoError(t, os.Chmod(gone, 0o700)) //nolint:gosec // restore directory access for test cleanup
	}()

	entries, err := r.ChangedFiles("", false)
	require.NoError(t, err, "an unreadable open folder must not fail the whole listing")
	assert.Equal(t, []FileEntry{{Path: "gone", IsDir: true}}, entries, "the folder is listed closed")

	require.NoError(t, os.Chmod(gone, 0o700)) //nolint:gosec // readable again
	require.NoError(t, os.WriteFile(filepath.Join(gone, "file"), nil, 0o600))
	entries, err = r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []FileEntry{{Path: "gone", IsDir: true}}, entries, "the folder was forgotten, not reopened")
}

func TestFolderReaderMissingRootFails(t *testing.T) {
	_, err := NewFolderReader(filepath.Join(t.TempDir(), "missing"), nil, nil).ChangedFiles("", false)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestFolderReaderSetDirectoryDoesNotWaitForWatcher(t *testing.T) {
	root := t.TempDir()
	r := NewFolderReader(root, nil, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	r.SetDirectoryWatcher(func([]string) {
		close(entered)
		<-release
	})
	listed := make(chan error, 1)
	go func() {
		_, err := r.ChangedFiles("", false)
		listed <- err
	}()
	<-entered
	toggled := make(chan struct{})
	go func() {
		r.SetDirectory("src", true)
		close(toggled)
	}()
	select {
	case <-toggled:
	case <-time.After(5 * time.Second):
		t.Fatal("SetDirectory blocked behind the directory watcher")
	}
	close(release)
	require.NoError(t, <-listed)
}

func TestFolderReaderKeepsNewestListingWatched(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0o700))
	r := NewFolderReader(root, nil, nil)
	var watched [][]string
	r.SetDirectoryWatcher(func(dirs []string) {
		watched = append(watched, dirs)
	})
	r.reportWatched(2, []string{"newer"})
	r.reportWatched(1, []string{"older"})
	assert.Equal(t, [][]string{{"newer"}}, watched, "a listing that started earlier must not replace a later one")

	r.SetDirectoryWatcher(nil)
	_, err := r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Len(t, watched, 1, "a detached watcher is not called")
}
