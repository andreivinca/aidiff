package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIgnoredAmong(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", root).Run())
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n"), 0o600))
	app := filepath.Join(root, "app")
	for _, d := range []string{"node_modules/x", "src"} {
		require.NoError(t, os.MkdirAll(filepath.Join(app, d), 0o750))
	}
	check := ignoredAmong(root)
	modules, src := filepath.Join(app, "node_modules"), filepath.Join(app, "src")
	assert.Equal(t, []string{modules, filepath.Join(modules, "x")},
		check(t.Context(), []string{modules, src, filepath.Join(modules, "x")}), "one call answers for every folder given")
	assert.Empty(t, check(t.Context(), []string{src}), "none ignored")
}
