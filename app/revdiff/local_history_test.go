package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/t3palette"
)

func TestFolderOptions_DropTheRunsOwnTarget(t *testing.T) {
	opts := options{Only: []string{"a.go"}, AllFiles: true, Stdin: true, T3: "local", Annotations: "x.md", Wrap: true,
		Staged: true, Include: []string{"app/"}, Exclude: []string{"vendor/"}}
	opts.Refs.Base, opts.Refs.Against = "main", "dev"
	got := folderOptions(opts)
	assert.Empty(t, got.ref())
	assert.False(t, got.Staged, "--staged compared the run's own repository")
	assert.Empty(t, got.Only)
	assert.Empty(t, got.Include, "--include narrowed the run's own folder")
	assert.Empty(t, got.Exclude)
	assert.False(t, got.AllFiles)
	assert.False(t, got.Stdin)
	assert.Empty(t, got.T3)
	assert.Empty(t, got.Annotations)
	assert.True(t, got.Wrap, "how reviews look stays")

	plain := t.TempDir()
	_, err := setupFolderSource(folderOptions(options{Staged: true}), plain)
	require.NoError(t, err, "a plain folder opens from the history of a --staged run")
}

func TestLocalHistory_FiledAsDiffViewLeftIt(t *testing.T) {
	work, forkPoint := branchRepo(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(work)
	histDir := t.TempDir()
	opts, err := parseArgs(append(noConfigArgs(t), "--history-dir", histDir))
	require.NoError(t, err)
	src, err := setupSource(opts)
	require.NoError(t, err)
	deps := newReviewDeps(opts)
	l := newLocalDiff(deps, src, "")
	require.NotNil(t, l)
	diffs := &localDiffs{}
	cfg := newLocalConfig(deps, src, diffs, l)
	require.NotNil(t, cfg.Start)
	t3p := t3palette.New(t3palette.Config{Local: cfg}, nil)
	defer t3p.Close()

	filed := func() (ref string, staged bool) {
		h := src.exitHistory(opts, t3p, diffs)
		return h.opts.ref(), h.opts.Staged
	}
	ref, staged := filed()
	assert.Empty(t, ref, "the run's own comparison: the unstaged changes")
	assert.False(t, staged)

	store := annotation.NewStore()
	runCmd(l.show(localChoice{staged: true, label: "staged"}, store))
	_, staged = filed()
	assert.True(t, staged, "Diff View… switched to the staged changes")

	runCmd(l.show(localChoice{ref: "origin/main", label: "vs origin/main"}, store))
	ref, staged = filed()
	assert.Equal(t, forkPoint, ref, "the branch as the review compares it: from where they parted")
	assert.False(t, staged)

	cfg.SaveHistory(cfg.Start.Dir, "a.txt:1 note\n", []string{"a.txt"})
	saved, err := filepath.Glob(filepath.Join(histDir, "*", "*.md"))
	require.NoError(t, err)
	require.Len(t, saved, 1)
	data, err := os.ReadFile(saved[0]) //nolint:gosec // test-owned path
	require.NoError(t, err)
	assert.Contains(t, string(data), "refs: "+forkPoint+"\n", "a workspace leaving the screen is filed the same way")

	runCmd(l.showTree(store))
	ref, staged = filed()
	assert.Empty(t, ref, "the file tree shows the working tree")
	assert.False(t, staged)
}

func TestSetupFolderSource(t *testing.T) {
	plain := t.TempDir()
	src, err := setupFolderSource(options{}, plain)
	require.NoError(t, err)
	assert.NotNil(t, src.folder, "a plain folder is browsed")
	assert.Equal(t, plain, src.historyDir(options{}))

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", repo).Run())
	sub := filepath.Join(repo, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o750))
	src, err = setupFolderSource(options{}, sub)
	require.NoError(t, err)
	assert.Equal(t, diff.VCSGit, src.vcsType)
	root, err := filepath.EvalSymlinks(repo)
	require.NoError(t, err)
	got, err := filepath.EvalSymlinks(src.historyDir(options{}))
	require.NoError(t, err)
	assert.Equal(t, root, got, "a repository is listed by its root")

	assert.Empty(t, src.historyDir(options{Stdin: true}), "stdin has no folder")
	h, err := folderHistory(options{}, sub)
	require.NoError(t, err)
	assert.NotEmpty(t, h.gitRoot)
}
