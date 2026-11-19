package editor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEditor_ProjectCommand(t *testing.T) {
	t.Setenv("EDITOR", "fresh --no-plugins")
	dir := t.TempDir()
	cmd, err := Editor{}.ProjectCommand(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"fresh", "--no-plugins"}, cmd.Args, "no file is passed")
	assert.Equal(t, dir, cmd.Dir, "it starts in the project folder")

	file := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	_, err = Editor{}.ProjectCommand(file)
	require.ErrorIs(t, err, ErrProjectNotDir)
	_, err = Editor{}.ProjectCommand(filepath.Join(dir, "missing"))
	require.Error(t, err)
}
