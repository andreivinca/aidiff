package editor

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// RemoteCommand prepares an editor invocation, on this machine, that edits
// files on an SSH host: dir (an absolute path on host) and file in it
// (relative to dir; "" for the folder alone), at line when it is above 0.
// It is for aidiff's e / f12 in a T3 review over SSH, whose files are on the
// host while aidiff and its terminal are here. ok is false when the editor
// ($EDITOR, then $VISUAL, then vi) cannot open a host's files itself, and
// the caller runs one on the host instead.
//
// fresh opens ssh://host/path[:line], with the files, its terminal and its
// language servers on the host. It runs the system ssh; with control, the
// ControlMaster socket of a connection to host already open (the T3
// tunnel's), its ssh shares that connection, as the terminal panes do,
// instead of logging in again (sharedSSH).
func (e Editor) RemoteCommand(host, control, dir, file string, line int) (cmd *exec.Cmd, ok bool) {
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
	cmd = exec.CommandContext(context.Background(), argv[0], argv[1:]...)
	if bin := e.sharedSSH(control); bin != "" {
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return cmd, true
}

// sharedSSH is a folder holding an ssh, put first on the editor's PATH,
// that runs the system ssh on the connection behind the ControlMaster socket
// control: no second login, and no second setup of the host's forwards
// (ClearAllForwardings), as the terminal panes share it. It lives beside
// the socket, in the tunnel's private folder, and goes with it. When the
// socket is gone ssh connects on its own. "" when there is no socket, or the
// script cannot be written, leaving the editor its own connection.
func (Editor) sharedSSH(control string) string {
	if control == "" {
		return ""
	}
	real, err := exec.LookPath("ssh")
	if err != nil {
		return ""
	}
	bin := filepath.Join(filepath.Dir(control), "bin")
	script := "#!/bin/sh\nexec " + shellQuote(real) + " -o ControlMaster=no -o ControlPath=" + shellQuote(control) +
		" -o ClearAllForwardings=yes \"$@\"\n"
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return ""
	}
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o700); err != nil { //nolint:gosec // a script of ours, in the tunnel's private folder
		return ""
	}
	return bin
}

// shellQuote quotes s as one word for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
