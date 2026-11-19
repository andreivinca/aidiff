package editor

import (
	"context"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
)

// RemoteCommand prepares an editor invocation, on this machine, that edits
// files on an SSH host: dir (an absolute path on host) and file in it
// (relative to dir; "" for the folder alone), at line when it is above 0.
// It is for aidiff's e / f12 in a T3 review over SSH, whose files are on the
// host while aidiff and its terminal are here. ok is false when the editor
// ($EDITOR, then $VISUAL, then vi) cannot open a host's files itself, and
// the caller runs one on the host instead.
//
// fresh opens ssh://host/path[:line] (its own SSH connection, with the
// files, its terminal and its language servers on the host).
func (e Editor) RemoteCommand(host, dir, file string, line int) (cmd *exec.Cmd, ok bool) {
	if host == "" || !path.IsAbs(dir) {
		return nil, false
	}
	argv := e.resolve()
	if filepath.Base(argv[0]) != "fresh" {
		return nil, false
	}
	target := dir
	if file != "" {
		target = path.Join(dir, file)
		if line > 0 {
			target += ":" + strconv.Itoa(line)
		}
	}
	argv = append(argv, "ssh://"+host+target)
	//nolint:gosec // user-controlled editor binary by design (resolved from $EDITOR/$VISUAL)
	return exec.CommandContext(context.Background(), argv[0], argv[1:]...), true
}
