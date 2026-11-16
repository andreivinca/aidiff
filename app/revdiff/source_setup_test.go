package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/ui/sidepane"
)

func TestSetupSource_PlainFolder(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Folder browsing does not need git or another VCS executable.
	t.Setenv("PATH", t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src", "generated"), 0o750))
	for _, name := range []string{"readme.md", "src/main.go", "src/generated/code.go"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("hello\nworld\n"), 0o600))
	}
	tests := []struct {
		name string
		opts options
		want []diff.FileEntry
	}{
		{name: "default", want: []diff.FileEntry{{Path: "readme.md"}, {Path: "src", IsDir: true}}},
		{name: "explicit all files", opts: options{AllFiles: true}, want: []diff.FileEntry{{Path: "readme.md"}, {Path: "src", IsDir: true}}},
		{name: "include and exclude", opts: options{Include: []string{"src"}, Exclude: []string{"src/generated"}}, want: []diff.FileEntry{{Path: "src", IsDir: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, err := setupSource(tt.opts)
			require.NoError(t, err)
			defer src.close()
			assert.Equal(t, dir, src.workDir)
			assert.Equal(t, diff.VCSNone, src.vcsType)
			require.NotNil(t, src.folder)
			assert.Nil(t, src.blamer)
			assert.Nil(t, src.commitLogger)
			assert.Nil(t, src.untrackedFn)
			assert.IsType(t, &sidepane.FolderTree{}, src.newFileTree(nil), "a plain folder gets the folder browser")
			entries, err := src.renderer.ChangedFiles("", false)
			require.NoError(t, err)
			assert.Equal(t, tt.want, entries)
			lines, err := src.renderer.FileDiff(diff.FileDiffRequest{Path: "src/main.go"})
			require.NoError(t, err)
			assert.Equal(t, []diff.DiffLine{
				{OldNum: 1, NewNum: 1, Content: "hello", ChangeType: diff.ChangeContext},
				{OldNum: 2, NewNum: 2, Content: "world", ChangeType: diff.ChangeContext},
			}, lines)
		})
	}
}

func TestSetupSource_OnlyOutsideRepositoryReviewsFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	src, err := setupSource(options{Only: []string{"file.md"}})
	require.NoError(t, err)
	assert.Nil(t, src.folder)
	assert.IsType(t, &diff.FileReader{}, src.renderer)
	assert.IsType(t, &sidepane.FileTree{}, src.newFileTree(nil))
}

func TestSetupSource_PlainFolderRequiresRepositoryForDiffs(t *testing.T) {
	t.Chdir(t.TempDir())
	refOpts := options{}
	refOpts.Refs.Base = "HEAD"
	for _, opts := range []options{refOpts, {Staged: true}} {
		_, err := setupSource(opts)
		require.ErrorContains(t, err, "require a repository")
	}
}

func TestSourceSetup_MachineName(t *testing.T) {
	assert.Equal(t, "local-host", sourceSetup{}.machineName("local-host"))
}

func TestWatchInBackground_StopWaitsForStartAndRunsOnce(t *testing.T) {
	release := make(chan struct{})
	stops := 0
	stop := watchInBackground(func() func() {
		<-release
		return func() {
			stops++
		}
	})
	close(release)
	stop()
	stop()
	assert.Equal(t, 1, stops, "stop runs what start returned, once")
}
