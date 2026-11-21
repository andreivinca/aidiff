package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// command is one run of a clipboard program.
type command struct {
	name  string
	args  []string
	stdin io.Reader // nil: no input
	// limit is the most output bytes accepted. Zero runs the program without
	// reading its output, as a writer must be run: wl-copy and xclip leave a
	// process behind to serve the clipboard, which would hold any pipe open.
	limit int64
}

// maxStderrBytes caps how much of a failed writer's stderr its error quotes.
const maxStderrBytes = 4 << 10

// exitError is a clipboard program that ran and failed.
type exitError struct {
	name   string
	stderr string
	err    error
}

func (e *exitError) Error() string {
	if e.stderr != "" {
		return e.name + ": " + e.stderr
	}
	return e.name + ": " + e.err.Error()
}

func (e *exitError) Unwrap() error {
	return e.err
}

// runCommand runs c and returns its output.
func runCommand(ctx context.Context, c command) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.name, c.args...) //nolint:gosec // a known tool, fixed arguments
	cmd.Stdin = c.stdin
	if c.limit == 0 {
		return nil, runWriter(cmd, c)
	}
	return readOutput(cmd, c)
}

// runWriter runs a writer, its stderr going to an unlinked temp file rather
// than a pipe: the process a writer leaves behind holds what it inherited,
// and a pipe would keep Wait from returning until it exits, while a file
// is read once the writer itself has, and only when it failed.
func runWriter(cmd *exec.Cmd, c command) error {
	stderr, err := os.CreateTemp("", "aidiff-clipboard-*")
	if err != nil {
		return fmt.Errorf("%s: %w", c.name, err)
	}
	_ = os.Remove(stderr.Name()) // gone with the last open handle
	defer func() { _ = stderr.Close() }()
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}
	if _, exited := errors.AsType[*exec.ExitError](runErr); !exited {
		return fmt.Errorf("%s: %w", c.name, runErr)
	}
	msg, _ := io.ReadAll(io.NewSectionReader(stderr, 0, maxStderrBytes))
	return &exitError{name: c.name, stderr: strings.TrimSpace(string(msg)), err: runErr}
}

// readOutput runs cmd and returns its output, refusing more than c.limit bytes.
func readOutput(cmd *exec.Cmd, c command) ([]byte, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", c.name, err)
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, c.limit+1))
	if int64(len(out)) > c.limit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("%s: the clipboard holds more than %d MB", c.name, c.limit>>20)
	}
	if err := cmd.Wait(); err != nil {
		if _, exited := errors.AsType[*exec.ExitError](err); exited {
			return nil, &exitError{name: c.name, stderr: strings.TrimSpace(stderr.String()), err: err}
		}
		return nil, fmt.Errorf("%s: %w", c.name, err)
	}
	if readErr != nil {
		return nil, fmt.Errorf("%s: read output: %w", c.name, readErr)
	}
	return out, nil
}
