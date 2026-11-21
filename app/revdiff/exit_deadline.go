package main

import (
	"fmt"
	"io"
	"os"
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
	code  *int // the exit code finalize computed, nil before it did (result)
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

// result records the exit code finalize computed and passes it on. The steps
// after finalize (closing the T3 connection, stopping the watchers) run under
// the deadline too, and by then the -o output is written: an exit cut short
// there exits with this code, so a launcher reading 10 (annotations) still
// gets it. An error leaves the cut-short exit at 1.
func (d *exitDeadline) result(code int, err error) (int, error) {
	if d != nil && err == nil {
		d.mu.Lock()
		d.code = &code
		d.mu.Unlock()
	}
	return code, err
}

func (d *exitDeadline) expire() {
	d.mu.Lock()
	if d.done {
		d.mu.Unlock()
		return
	}
	d.done = true
	code := 1
	if d.code != nil {
		code = *d.code
	}
	d.mu.Unlock()
	note := "aidiff: exit did not finish; quitting anyway"
	if path, err := d.writeDump(); err == nil {
		note += ", goroutine stacks saved to " + path
	}
	_, _ = fmt.Fprintln(d.log, note)
	d.exit(code)
}

// writeDump saves the goroutine stacks to a new file of a random name in
// d.dir, returning its path; a fixed name in a shared temp directory could be
// a link another user planted.
func (d *exitDeadline) writeDump() (string, error) {
	f, err := os.CreateTemp(d.dir, "aidiff-shutdown-*.txt")
	if err != nil {
		return "", fmt.Errorf("create goroutine dump: %w", err)
	}
	if err := d.dump(f); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close goroutine dump: %w", err)
	}
	return f.Name(), nil
}

// dumpGoroutines writes every goroutine's stack, as a crash would print it.
func dumpGoroutines(w io.Writer) error {
	if err := pprof.Lookup("goroutine").WriteTo(w, 2); err != nil {
		return fmt.Errorf("write goroutine dump: %w", err)
	}
	return nil
}
