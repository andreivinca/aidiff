package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// command is one run of a clipboard program.
type command struct {
	name  string
	args  []string
	stdin io.Reader // nil: no input
	// limit is the most output bytes accepted. Zero runs the program without
	// reading its output or errors, as a writer must be run: wl-copy and
	// xclip leave a process behind to serve the clipboard, which would hold
	// any pipe open.
	limit int64
}

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
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("%s: %w", c.name, err)
		}
		return nil, nil
	}
	return readOutput(cmd, c)
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
