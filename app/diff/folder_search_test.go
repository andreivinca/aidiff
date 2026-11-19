package diff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFiles creates files (slash paths) under root.
func writeFiles(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(f), 0o600))
	}
}

func TestFolderReader_SearchFilesOutsideGit(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, "a/b/needle.go", "a/other.go", ".git/needle", "skip/needle.txt")
	r := NewFolderReader(root, nil, []string{"skip"})

	found, err := r.SearchFiles(context.Background(), "needle", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"a/b/needle.go"}, found.Paths, "folders never opened are searched; VCS metadata and excluded paths are not")

	found, err = r.SearchFiles(context.Background(), "", 10)
	require.NoError(t, err)
	assert.Empty(t, found.Paths, "an empty query finds nothing")
}

func TestFolderReader_SearchFilesInGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	writeFiles(t, root, ".gitignore", "src/tracked.go", "src/untracked.go", "build/ignored.go")
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("build/\n"), 0o600))
	for _, args := range [][]string{{"init", "-q"}, {"add", ".gitignore", "src/tracked.go"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	r := NewFolderReader(root, nil, nil)
	found, err := r.SearchFiles(context.Background(), "tracked", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"src/tracked.go", "src/untracked.go"}, found.Paths, "tracked and untracked files")
	found, err = r.SearchFiles(context.Background(), "ignored", 10)
	require.NoError(t, err)
	assert.Empty(t, found.Paths, "ignored ones are left out")
}
