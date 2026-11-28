package remotegit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/t3client"
)

// localRun runs git here, as t3client.RunOn would on the machine.
func localRun(t *testing.T) {
	t.Helper()
	old := run
	run = func(ctx context.Context, _, dir string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", append(append([]string{}, gitOptions...), args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
		var stderr []byte
		out, err := cmd.Output()
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
			return out, &t3client.RunError{Code: ee.ExitCode(), Stderr: string(stderr)}
		}
		return out, err
	}
	t.Cleanup(func() { run = old })
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func write(t *testing.T, path, text string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
}

func TestRenderer_ReviewsWhatDiffersFromHEAD(t *testing.T) {
	localRun(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "a.txt"), "one\ntwo\n")
	write(t, filepath.Join(dir, "gone.txt"), "bye\n")
	write(t, filepath.Join(dir, "old name.txt"), "same\nsame\nsame\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "init")
	write(t, filepath.Join(dir, "a.txt"), "one\nTWO\n")
	require.NoError(t, os.Remove(filepath.Join(dir, "gone.txt")))
	git(t, dir, "mv", "old name.txt", "new name.txt")
	write(t, filepath.Join(dir, "sub", "new file.txt"), "fresh\n")
	write(t, filepath.Join(dir, "staged.txt"), "added\n")
	git(t, dir, "add", "staged.txt")

	root, err := Resolve(context.Background(), "box", filepath.Join(dir, "sub"))
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	assert.Equal(t, want, root, "the repository's top folder")

	r := New("box", root)
	files, err := r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.ElementsMatch(t, []diff.FileEntry{
		{Path: "a.txt", Status: diff.FileModified},
		{Path: "gone.txt", Status: diff.FileDeleted},
		{Path: "new name.txt", OldPath: "old name.txt", Status: diff.FileRenamed},
		{Path: "staged.txt", Status: diff.FileAdded},
		{Path: "sub/new file.txt", Status: diff.FileAdded},
	}, files)

	lines, err := r.FileDiff(diff.FileDiffRequest{Path: "a.txt"})
	require.NoError(t, err)
	assert.Equal(t, []diff.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "one", ChangeType: diff.ChangeContext},
		{OldNum: 2, Content: "two", ChangeType: diff.ChangeRemove},
		{NewNum: 2, Content: "TWO", ChangeType: diff.ChangeAdd},
	}, lines, "whole file, as a local review's default")

	lines, err = r.FileDiff(diff.FileDiffRequest{Path: "sub/new file.txt"})
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Equal(t, diff.DiffLine{NewNum: 1, Content: "fresh", ChangeType: diff.ChangeAdd}, lines[0], "an untracked file, added whole")

	lines, err = r.FileDiff(diff.FileDiffRequest{Path: "gone.txt"})
	require.NoError(t, err)
	assert.Equal(t, []diff.DiffLine{{OldNum: 1, Content: "bye", ChangeType: diff.ChangeRemove}}, lines)

	lines, err = r.FileDiff(diff.FileDiffRequest{Path: "new name.txt", OldPath: "old name.txt"})
	require.NoError(t, err)
	assert.Len(t, lines, 3, "a pure rename shows its lines unchanged")

	lines, err = r.FileDiff(diff.FileDiffRequest{Path: "staged.txt"})
	require.NoError(t, err)
	assert.Equal(t, []diff.DiffLine{{NewNum: 1, Content: "added", ChangeType: diff.ChangeAdd}}, lines)
}

func TestRenderer_RepositoryWithoutCommits(t *testing.T) {
	localRun(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "x.txt"), "x\n")
	git(t, dir, "add", "x.txt")
	r := New("box", dir)
	files, err := r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []diff.FileEntry{{Path: "x.txt", Status: diff.FileAdded}}, files)
	lines, err := r.FileDiff(diff.FileDiffRequest{Path: "x.txt"})
	require.NoError(t, err)
	assert.Equal(t, []diff.DiffLine{{NewNum: 1, Content: "x", ChangeType: diff.ChangeAdd}}, lines, "against the empty tree")
}

func TestResolve_NotARepository(t *testing.T) {
	localRun(t)
	_, err := Resolve(context.Background(), "box", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a git repository")
}

func TestRenderer_WatchCallsWhenTheReviewChanges(t *testing.T) {
	localRun(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "init")
	write(t, filepath.Join(dir, "a.txt"), "two\n")

	changed := make(chan struct{}, 10)
	stop := New("box", dir).Watch(20*time.Millisecond, func() { changed <- struct{}{} })
	defer stop()
	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, changed, "the first check is the baseline")

	write(t, filepath.Join(dir, "a.txt"), "three\n") // still " M": only the lines tell
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("an edit to a modified file was not noticed")
	}
	write(t, filepath.Join(dir, "b.txt"), "new\n")
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("a new file was not noticed")
	}
}
