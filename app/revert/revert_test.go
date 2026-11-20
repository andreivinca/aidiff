package revert

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
)

// repo is a git repository with a.txt ("one".."five") committed.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q")
	write(t, dir, "a.txt", "one\ntwo\nthree\nfour\nfive\n")
	run(t, dir, "add", ".")
	run(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	return dir
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return string(out)
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644))
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	return string(b)
}

// diffOf is the review's lines of name against the index, as the UI holds them.
func diffOf(t *testing.T, dir, name string) []diff.DiffLine {
	t.Helper()
	lines, err := diff.NewGit(dir).FileDiff(diff.FileDiffRequest{Path: name})
	require.NoError(t, err)
	return lines
}

// blocks are the [start, end] bounds of the change blocks of lines.
func blocks(lines []diff.DiffLine) [][2]int {
	var out [][2]int
	for i := 0; i < len(lines); i++ {
		ct := lines[i].ChangeType
		if ct != diff.ChangeAdd && ct != diff.ChangeRemove {
			continue
		}
		j := i
		for j+1 < len(lines) && (lines[j+1].ChangeType == diff.ChangeAdd || lines[j+1].ChangeType == diff.ChangeRemove) {
			j++
		}
		out = append(out, [2]int{i, j})
		i = j
	}
	return out
}

func TestRevertChange_OneBlockOfMany(t *testing.T) {
	dir := repo(t)
	write(t, dir, "a.txt", "ONE\ntwo\nthree\nfour\nFIVE\nsix\n")
	lines := diffOf(t, dir, "a.txt")
	b := blocks(lines)
	require.Len(t, b, 2)

	g := Git{Root: dir}
	entry := diff.FileEntry{Path: "a.txt", Status: diff.FileModified}
	require.NoError(t, g.RevertChange(entry, lines, b[1][0], b[1][1]))
	assert.Equal(t, "ONE\ntwo\nthree\nfour\nfive\n", read(t, dir, "a.txt"), "the second block only")

	lines = diffOf(t, dir, "a.txt")
	b = blocks(lines)
	require.Len(t, b, 1)
	require.NoError(t, g.RevertChange(entry, lines, b[0][0], b[0][1]))
	assert.Equal(t, "one\ntwo\nthree\nfour\nfive\n", read(t, dir, "a.txt"))
	assert.Empty(t, run(t, dir, "status", "--porcelain"))
}

func TestRevertChange_PureDeletionBelowAnInsertion(t *testing.T) {
	dir := repo(t)
	write(t, dir, "a.txt", "zero\none\ntwo\nfour\nfive\n") // a line added on top, "three" removed
	lines := diffOf(t, dir, "a.txt")
	b := blocks(lines)
	require.Len(t, b, 2)
	require.NoError(t, Git{Root: dir}.RevertChange(diff.FileEntry{Path: "a.txt", Status: diff.FileModified}, lines, b[1][0], b[1][1]))
	assert.Equal(t, "zero\none\ntwo\nthree\nfour\nfive\n", read(t, dir, "a.txt"))
}

func TestRevertChange_StaleFileIsRefused(t *testing.T) {
	dir := repo(t)
	write(t, dir, "a.txt", "ONE\ntwo\nthree\nfour\nfive\n")
	lines := diffOf(t, dir, "a.txt")
	b := blocks(lines)
	write(t, dir, "a.txt", "uno\ntwo\nthree\nfour\nfive\n") // changed again after the review loaded
	err := Git{Root: dir}.RevertChange(diff.FileEntry{Path: "a.txt", Status: diff.FileModified}, lines, b[0][0], b[0][1])
	require.ErrorIs(t, err, ErrStale)
	assert.Equal(t, "uno\ntwo\nthree\nfour\nfive\n", read(t, dir, "a.txt"), "left as it is")
}

func TestRevertChange_CRLFKept(t *testing.T) {
	dir := repo(t)
	write(t, dir, "w.txt", "a\r\nb\r\n")
	run(t, dir, "add", "w.txt")
	write(t, dir, "w.txt", "a\r\nB\r\n")
	lines := diffOf(t, dir, "w.txt")
	b := blocks(lines)
	require.NoError(t, Git{Root: dir}.RevertChange(diff.FileEntry{Path: "w.txt", Status: diff.FileModified}, lines, b[0][0], b[0][1]))
	assert.Equal(t, "a\r\nb\r\n", read(t, dir, "w.txt"))
}

func TestRevertChange_NewFileEmptiedIsRemoved(t *testing.T) {
	dir := repo(t)
	write(t, dir, "new.txt", "x\ny\n")
	lines, err := diff.ReadFileAsAdded(filepath.Join(dir, "new.txt"))
	require.NoError(t, err)
	require.NoError(t, Git{Root: dir}.RevertChange(diff.FileEntry{Path: "new.txt", Status: diff.FileUntracked}, lines, 0, len(lines)-1))
	assert.NoFileExists(t, filepath.Join(dir, "new.txt"))
}

func TestRevertFile(t *testing.T) {
	dir := repo(t)
	write(t, dir, "a.txt", "changed\n")
	write(t, dir, "new.txt", "x\n")
	g := Git{Root: dir}
	require.NoError(t, g.RevertFile(diff.FileEntry{Path: "a.txt", Status: diff.FileModified}))
	require.NoError(t, g.RevertFile(diff.FileEntry{Path: "new.txt", Status: diff.FileUntracked}))
	assert.Empty(t, run(t, dir, "status", "--porcelain"))

	require.NoError(t, os.Remove(filepath.Join(dir, "a.txt")))
	require.NoError(t, g.RevertFile(diff.FileEntry{Path: "a.txt", Status: diff.FileDeleted}))
	assert.Equal(t, "one\ntwo\nthree\nfour\nfive\n", read(t, dir, "a.txt"))
}

func TestRevertFile_AgainstHEADUnstages(t *testing.T) {
	dir := repo(t)
	write(t, dir, "a.txt", "staged\n")
	write(t, dir, "b.txt", "added\n")
	run(t, dir, "add", ".")
	g := Git{Root: dir, Ref: "HEAD"}
	require.NoError(t, g.RevertFile(diff.FileEntry{Path: "a.txt", Status: diff.FileModified}))
	require.NoError(t, g.RevertFile(diff.FileEntry{Path: "b.txt", Status: diff.FileAdded}))
	assert.Empty(t, run(t, dir, "status", "--porcelain"), "nothing staged or changed is left")
}

func TestRevertFile_Rename(t *testing.T) {
	dir := repo(t)
	run(t, dir, "mv", "a.txt", "b.txt")
	require.NoError(t, Git{Root: dir, Ref: "HEAD"}.RevertFile(diff.FileEntry{Path: "b.txt", OldPath: "a.txt", Status: diff.FileRenamed}))
	assert.Empty(t, run(t, dir, "status", "--porcelain"))
}

func TestAbs_RefusesPathsOutsideTheRepository(t *testing.T) {
	g := Git{Root: "/repo"}
	for _, p := range []string{"../x", "/etc/passwd", ".."} {
		_, err := g.abs(p)
		assert.Error(t, err, p)
	}
	got, err := g.abs("a/b.txt")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/repo", "a", "b.txt"), got)
}

func TestRevertChange_CompactDiff(t *testing.T) {
	dir := repo(t)
	write(t, dir, "a.txt", "zero\none\ntwo\nthree\nfive\n") // a line added on top, "four" removed, far apart
	lines, err := diff.NewGit(dir).FileDiff(diff.FileDiffRequest{Path: "a.txt", ContextLines: 1})
	require.NoError(t, err)
	b := blocks(lines)
	require.Len(t, b, 2)
	require.NoError(t, Git{Root: dir}.RevertChange(diff.FileEntry{Path: "a.txt", Status: diff.FileModified}, lines, b[1][0], b[1][1]))
	assert.Equal(t, "zero\none\ntwo\nthree\nfour\nfive\n", read(t, dir, "a.txt"))
}
