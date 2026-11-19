package editor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// ErrProjectNotDir is returned when ProjectCommand is given a path that is
// not a folder.
var ErrProjectNotDir = errors.New("project path is not a folder")

// ProjectCommand prepares an editor invocation that opens no file, started
// in the project folder dir, for aidiff's e / f12 with no file on screen.
func (e Editor) ProjectCommand(dir string) (*exec.Cmd, error) {
	stat, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat project folder: %w", err)
	}
	if !stat.IsDir() {
		return nil, ErrProjectNotDir
	}
	argv := e.resolve()
	//nolint:gosec // user-controlled editor binary by design (resolved from $EDITOR/$VISUAL)
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	cmd.Dir = dir
	return cmd, nil
}
