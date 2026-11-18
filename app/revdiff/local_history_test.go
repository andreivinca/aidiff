package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
)

func TestFolderOptions_DropTheRunsOwnTarget(t *testing.T) {
	opts := options{Only: []string{"a.go"}, AllFiles: true, Stdin: true, T3: "local", Annotations: "x.md", Wrap: true}
	opts.Refs.Base, opts.Refs.Against = "main", "dev"
	got := folderOptions(opts)
	assert.Empty(t, got.ref())
	assert.Empty(t, got.Only)
	assert.False(t, got.AllFiles)
	assert.False(t, got.Stdin)
	assert.Empty(t, got.T3)
	assert.Empty(t, got.Annotations)
	assert.True(t, got.Wrap, "how reviews look stays")
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
