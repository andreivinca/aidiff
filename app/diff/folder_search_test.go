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

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func TestFolderReader_SearchFilesCachesAnEmptyListing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	gitIn(t, root, "init", "-q")
	r := NewFolderReader(root, nil, nil)
	found, err := r.SearchFiles(context.Background(), "late", 10)
	require.NoError(t, err)
	assert.Empty(t, found.Paths)

	writeFiles(t, root, "late.go")
	found, err = r.SearchFiles(context.Background(), "late", 10)
	require.NoError(t, err)
	assert.Empty(t, found.Paths, "the empty listing serves the next keystroke too: git is not run again")
}

func TestFolderReader_SearchFilesListsAConflictOnce(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	writeFiles(t, root, "clash.go")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-qm", "base")
	gitIn(t, root, "checkout", "-qb", "other")
	require.NoError(t, os.WriteFile(filepath.Join(root, "clash.go"), []byte("other"), 0o600))
	gitIn(t, root, "commit", "-qam", "other")
	gitIn(t, root, "checkout", "-q", "main")
	require.NoError(t, os.WriteFile(filepath.Join(root, "clash.go"), []byte("main"), 0o600))
	gitIn(t, root, "commit", "-qam", "main")
	cmd := exec.Command("git", "merge", "-q", "other")
	cmd.Dir = root
	require.Error(t, cmd.Run(), "the merge conflicts")

	files, _, err := NewFolderReader(root, nil, nil).listGitFiles(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"clash.go"}, files, "one name for its three stages")
}
