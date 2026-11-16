package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync"
	"time"
)

// shutdownLimit is how long the exit may take once the TUI has ended. It
// covers closing terminal panes (a second each at most), the T3 connection's
// close handshake (up to ten seconds) and saving history.
const shutdownLimit = 20 * time.Second

// exitDeadline bounds the exit: a step that never returns would otherwise keep
// the process alive with no terminal, and with it the T3 SSH tunnel, the port
// forwards it holds and the terminals sharing its connection. When the limit
// passes, the goroutine stacks are saved to a file, for finding the step, and
// the process exits; the tunnel's watchdog then ends ssh.
type exitDeadline struct {
	mu    sync.Mutex
	timer *time.Timer
	done  bool
	dir   string         // where the goroutine dump goes
	exit  func(code int) // os.Exit outside tests
	log   io.Writer      // the note saying so; stderr outside tests
	dump  func(io.Writer) error
}

// newExitDeadline builds a deadline that dumps to the temp directory and
// exits the process.
func newExitDeadline() *exitDeadline {
	return &exitDeadline{dir: os.TempDir(), exit: os.Exit, log: os.Stderr, dump: dumpGoroutines}
}

// arm starts the countdown; arming twice keeps the first countdown. A nil
// deadline does nothing.
func (d *exitDeadline) arm(limit time.Duration) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.done || d.timer != nil {
		return
	}
	d.timer = time.AfterFunc(limit, d.expire)
}

// disarm stops the countdown once the exit finished in time.
func (d *exitDeadline) disarm() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.done = true
	if d.timer != nil {
		d.timer.Stop()
	}
}

// expireNow gives up on the exit at once, unless it already finished. A nil
// deadline does nothing.
func (d *exitDeadline) expireNow() {
	if d == nil {
		return
	}
	d.expire()
}

func (d *exitDeadline) expire() {
	d.mu.Lock()
	if d.done {
		d.mu.Unlock()
		return
	}
	d.done = true
	d.mu.Unlock()
	path := filepath.Join(d.dir, fmt.Sprintf("aidiff-shutdown-%d.txt", os.Getpid()))
	note := "aidiff: exit did not finish; quitting anyway"
	if err := d.writeDump(path); err == nil {
		note += ", goroutine stacks saved to " + path
	}
	_, _ = fmt.Fprintln(d.log, note)
	d.exit(1)
}

func (d *exitDeadline) writeDump(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // path built from the temp dir and our pid
	if err != nil {
		return fmt.Errorf("create goroutine dump: %w", err)
	}
	if err := d.dump(f); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close goroutine dump: %w", err)
	}
	return nil
}

// dumpGoroutines writes every goroutine's stack, as a crash would print it.
func dumpGoroutines(w io.Writer) error {
	if err := pprof.Lookup("goroutine").WriteTo(w, 2); err != nil {
		return fmt.Errorf("write goroutine dump: %w", err)
	}
	return nil
}
