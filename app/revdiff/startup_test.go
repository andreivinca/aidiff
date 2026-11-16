package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

// The child reaches its test body only after Bubble Tea's package initializer.
// Deliberately never answer terminal queries: a real unsupported terminal must
// reach the app without termenv's upstream five-second wait (see the termenv
// patch in docs/DEVELOPMENT.md).
func TestStartupSilentTerminal(t *testing.T) {
	if os.Getenv("AIDIFF_SILENT_TERMINAL_HELPER") == "1" {
		_, err := fmt.Fprintln(os.Stdout, "silent-terminal-ready")
		require.NoError(t, err)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestStartupSilentTerminal$") //nolint:gosec // rerun this test binary with a fixed test selector
	cmd.Env = mergeEnv(map[string]string{"AIDIFF_SILENT_TERMINAL_HELPER": "1", "TERM": "xterm-256color", "TMUX": "", "TERM_PROGRAM": "", "COLORFGBG": ""})
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 100})
	require.NoError(t, err)
	defer terminal.Close()
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	output := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(terminal)
		output <- string(data)
	}()
	select {
	case text := <-output:
		require.Contains(t, text, "silent-terminal-ready")
	case <-time.After(3 * time.Second):
		t.Fatal("startup stalled waiting for a terminal color response")
	}
}
