package diff

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectVCS_Git(t *testing.T) {
	dir := setupTestRepo(t)

	vcs, root := DetectVCS(dir)
	assert.Equal(t, VCSGit, vcs)
	assert.Equal(t, dir, root)
}

func TestDetectVCS_Hg(t *testing.T) {
	dir := t.TempDir()
	err := os.Mkdir(filepath.Join(dir, ".hg"), 0o750)
	require.NoError(t, err)

	vcs, root := DetectVCS(dir)
	assert.Equal(t, VCSHg, vcs)
	assert.Equal(t, dir, root)
}

func TestDetectVCS_Jj(t *testing.T) {
	dir := t.TempDir()
	err := os.Mkdir(filepath.Join(dir, ".jj"), 0o750)
	require.NoError(t, err)

	vcs, root := DetectVCS(dir)
	assert.Equal(t, VCSJJ, vcs)
	assert.Equal(t, dir, root)
}

func TestDetectVCS_GitTakesPrecedenceOverHg(t *testing.T) {
	dir := setupTestRepo(t)
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".hg"), 0o750))

	vcs, root := DetectVCS(dir)
	assert.Equal(t, VCSGit, vcs)
	assert.Equal(t, dir, root)
}

// TestDetectVCS_JjTakesPrecedenceOverGit covers colocated jj+git repos.
// Jujutsu often coexists with .git (colocated mode); treat it as jj to avoid
// jj-unsafe git diff operations and to surface the actual working-copy model.
func TestDetectVCS_JjTakesPrecedenceOverGit(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".jj"), 0o750))
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o750))

	vcs, root := DetectVCS(dir)
	assert.Equal(t, VCSJJ, vcs)
	assert.Equal(t, dir, root)
}

func TestDetectVCS_WalksUp(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".hg"), 0o750))

	sub := filepath.Join(dir, "deep", "nested")
	require.NoError(t, os.MkdirAll(sub, 0o750))

	vcs, root := DetectVCS(sub)
	assert.Equal(t, VCSHg, vcs)
	assert.Equal(t, dir, root)
}

func TestDetectVCS_GitWorktree(t *testing.T) {
	// in git worktrees and submodules, .git is a file (not a directory)
	repo := setupTestRepo(t)
	gitCmd(t, repo, "commit", "--allow-empty", "-m", "initial")
	dir := filepath.Join(t.TempDir(), "worktree")
	gitCmd(t, repo, "worktree", "add", "--detach", dir)

	vcs, root := DetectVCS(dir)
	assert.Equal(t, VCSGit, vcs)
	assert.Equal(t, dir, root)
}

func TestDetectVCS_InvalidGitMetadata(t *testing.T) {
	for _, kind := range []string{"empty directory", "unrelated file", "stale worktree"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			meta := filepath.Join(dir, ".git")
			switch kind {
			case "empty directory":
				require.NoError(t, os.Mkdir(meta, 0o750))
			case "unrelated file":
				require.NoError(t, os.WriteFile(meta, []byte("not a repository\n"), 0o600))
			case "stale worktree":
				require.NoError(t, os.WriteFile(meta, []byte("gitdir: /nonexistent/aidiff-test\n"), 0o600))
			}
			sub := filepath.Join(dir, "folder")
			require.NoError(t, os.Mkdir(sub, 0o750))
			vcs, root := DetectVCS(sub)
			assert.Equal(t, VCSNone, vcs)
			assert.Empty(t, root)
		})
	}
}

func TestDetectVCS_None(t *testing.T) {
	dir := t.TempDir()
	vcs, root := DetectVCS(dir)
	assert.Equal(t, VCSNone, vcs)
	assert.Empty(t, root)
}
