// Package termpane runs a command inside a pseudo-terminal and keeps an
// in-memory terminal emulator of its screen, so a TUI can draw a live
// terminal inside one of its panes.
package termpane

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// frameInterval caps how often the pane asks the UI to redraw while the
// child is producing output. The first change after a quiet spell is
// reported at once (a keystroke echo must not wait for a tick); only
// follow-ups within the same frame are held back and coalesced.
const frameInterval = 16 * time.Millisecond

// scrollbackLines is how many lines that scrolled off the top are kept.
const scrollbackLines = 5000

// Pane is a running command attached to a pseudo-terminal plus the emulated
// screen it draws into. All methods are safe for concurrent use.
type Pane struct {
	// mu guards every emulator access. vt.SafeEmulator is not enough: its
	// CellAt hands out pointers into the live buffer after unlocking, so a
	// frame copied cell by cell would race the output reader.
	mu   sync.Mutex
	emu  *vt.Emulator
	ptmx *os.File
	cmd  *exec.Cmd

	// screenGen counts screen changes (output, resize) and frame is the last
	// Render result; both guarded by mu. Render reuses the frame while the
	// screen is unchanged, so redraws caused by anything but this pane (a key
	// press before its echo, the diff pane) skip the cell-by-cell rebuild.
	screenGen uint64
	frame     renderedFrame

	dirty      atomic.Bool
	exited     atomic.Bool
	kick       chan struct{} // wakes notifyLoop after dirty is set
	updates    chan struct{}
	done       chan struct{}
	readerDone chan struct{} // closed when readOutput stops writing to emu

	closeOnce sync.Once
}

// Start launches argv in dir inside a new pseudo-terminal of the given size.
// An empty argv runs $SHELL (falling back to /bin/sh).
func Start(argv []string, dir string, width, height int) (*Pane, error) {
	if len(argv) == 0 {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		argv = []string{shell}
	}
	width, height = max(width, 1), max(height, 1)

	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...) //nolint:gosec // user-chosen command, run on purpose; it lives until Close
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(height), Cols: uint16(width)}) //nolint:gosec // sizes are small positive ints
	if err != nil {
		return nil, fmt.Errorf("start %s in pty: %w", argv[0], err)
	}

	emu := vt.NewEmulator(width, height)
	emu.SetScrollbackSize(scrollbackLines)

	p := &Pane{
		emu:        emu,
		ptmx:       ptmx,
		cmd:        cmd,
		kick:       make(chan struct{}, 1),
		updates:    make(chan struct{}, 1),
		done:       make(chan struct{}),
		readerDone: make(chan struct{}),
	}
	go p.readOutput()
	go p.forwardInput()
	go p.notifyLoop()
	go func() {
		_ = cmd.Wait()
		p.exited.Store(true)
		p.markDirty()
	}()
	return p, nil
}

// readOutput copies everything the child writes into the emulator.
func (p *Pane) readOutput() {
	defer close(p.readerDone)
	buf := make([]byte, 32*1024)
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			p.mu.Lock()
			_, _ = p.emu.Write(buf[:n])
			p.screenGen++
			p.mu.Unlock()
			p.markDirty()
		}
		if err != nil {
			p.exited.Store(true)
			p.markDirty()
			return
		}
	}
}

// forwardInput copies keys and the emulator's own replies (cursor position
// reports, device attributes) to the child.
func (p *Pane) forwardInput() {
	_, err := io.Copy(p.ptmx, p.emu)
	if err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) {
		p.markDirty()
	}
}

// markDirty records a screen change and wakes notifyLoop.
func (p *Pane) markDirty() {
	p.dirty.Store(true)
	select {
	case p.kick <- struct{}{}:
	default: // a wake-up is already pending
	}
}

// notifyLoop turns bursts of output into at most one update per frame. It
// sleeps until woken, so an idle pane costs nothing, and reports a change
// immediately unless an update already went out less than a frame ago.
func (p *Pane) notifyLoop() {
	var last time.Time
	for {
		select {
		case <-p.done:
			return
		case <-p.kick:
		}
		if wait := frameInterval - time.Since(last); wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-p.done:
				t.Stop()
				return
			case <-t.C:
			}
		}
		if !p.dirty.Swap(false) {
			continue // already reported by a previous pass
		}
		select {
		case p.updates <- struct{}{}:
		default: // an update is already queued
		}
		last = time.Now()
	}
}

// Updates delivers a value whenever the screen changed since the last one.
func (p *Pane) Updates() <-chan struct{} { return p.updates }

// Exited reports whether the child process has ended.
func (p *Pane) Exited() bool { return p.exited.Load() }

// SendText writes raw bytes to the child as if typed.
func (p *Pane) SendText(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.emu.SendText(s)
}

// SendKey encodes a special key (arrows, home, function keys...) the way the
// child currently expects it, honoring application cursor-key mode.
func (p *Pane) SendKey(k uv.KeyPressEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.emu.SendKey(k)
}

// Paste sends text, wrapped in bracketed-paste markers when the child asked for them.
func (p *Pane) Paste(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.emu.Paste(s)
}

// SendMouse forwards a mouse event if the child enabled mouse reporting.
func (p *Pane) SendMouse(m vt.Mouse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.emu.SendMouse(m)
}

// Resize changes both the emulated screen and the pty window size.
func (p *Pane) Resize(width, height int) {
	width, height = max(width, 1), max(height, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.emu.Width() == width && p.emu.Height() == height {
		return
	}
	p.emu.Resize(width, height)
	p.screenGen++
	_ = pty.Setsize(p.ptmx, &pty.Winsize{Rows: uint16(height), Cols: uint16(width)}) //nolint:gosec // small positive ints
	p.markDirty()
}

// renderedFrame is a Render result and what it was built from.
type renderedFrame struct {
	valid      bool
	gen        uint64
	drawCursor bool
	out        string
}

// Render returns exactly height lines of exactly width cells. When showCursor
// is set, the cursor cell is drawn in reverse video.
func (p *Pane) Render(showCursor bool) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	drawCursor := showCursor && !p.emu.CursorHidden() && !p.Exited()
	if f := p.frame; f.valid && f.gen == p.screenGen && f.drawCursor == drawCursor {
		return f.out
	}
	out := p.renderLocked(drawCursor)
	p.frame = renderedFrame{valid: true, gen: p.screenGen, drawCursor: drawCursor, out: out}
	return out
}

// renderLocked builds the frame from the emulator cells; mu must be held.
func (p *Pane) renderLocked(drawCursor bool) string {
	w, h := p.emu.Width(), p.emu.Height()
	cur := p.emu.CursorPosition()

	var b strings.Builder
	b.Grow(h * (w + 16))
	line := make(uv.Line, w)
	for y := range h {
		for x := range w {
			if c := p.emu.CellAt(x, y); c != nil {
				line[x] = *c
			} else {
				line[x] = uv.EmptyCell
			}
		}
		if drawCursor && y == cur.Y && cur.X >= 0 && cur.X < w {
			c := &line[cur.X]
			if c.IsZero() || c.Content == "" {
				*c = uv.EmptyCell
				c.Content = " "
			}
			c.Style.Attrs ^= uv.AttrReverse
		}
		rendered := line.Render() + ansi.ResetStyle
		if pad := w - ansi.StringWidth(rendered); pad > 0 {
			rendered += strings.Repeat(" ", pad)
		}
		b.WriteString(rendered)
		if y < h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// killGrace is how long processes get to exit after SIGHUP before SIGKILL.
const killGrace = time.Second

// Close ends everything started in the terminal and releases the pty. Like
// closing a terminal window it sends SIGHUP, but to every process in the
// child's session (jobs and background processes included, which live in
// their own process groups), then SIGKILLs whatever ignored it.
func (p *Pane) Close() {
	p.closeOnce.Do(func() {
		close(p.done)
		if p.cmd.Process != nil {
			p.killSession(p.cmd.Process.Pid)
		}
		_ = p.ptmx.Close() // unblocks readOutput
		// Emulator.Close flips an unlocked flag that Read/Write also check, so it
		// would race the copy goroutines. Closing only the input pipe (an
		// io.PipeWriter, safe across goroutines) ends forwardInput with EOF.
		if pw, ok := p.emu.InputPipe().(*io.PipeWriter); ok {
			_ = pw.Close()
		}
		select {
		case <-p.readerDone:
		case <-time.After(time.Second):
		}
	})
}

// killSession hangs up every process in session sid (the child is the session
// leader, so sid is its pid), waits up to killGrace, then kills survivors.
// Without /proc (non-Linux) it falls back to the child's process group.
func (p *Pane) killSession(sid int) {
	signalAll := func(sig syscall.Signal) int {
		pids := sessionMembers(sid)
		if pids == nil { // no /proc: best effort on the process group
			_ = syscall.Kill(-sid, sig)
			return 0
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, sig)
		}
		return len(pids)
	}

	if signalAll(syscall.SIGHUP) == 0 {
		return
	}
	deadline := time.Now().Add(killGrace)
	for time.Now().Before(deadline) {
		if len(sessionMembers(sid)) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	signalAll(syscall.SIGKILL)
}

// sessionMembers lists live (non-zombie) processes whose session id is sid.
// It returns nil when /proc is unavailable and an empty slice when no
// process matches.
func sessionMembers(sid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	pids := []int{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // exited while scanning
		}
		// fields after the parenthesized command name: state ppid pgrp session ...
		end := bytes.LastIndexByte(stat, ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(stat[end+1:]))
		if len(fields) < 4 || fields[0] == "Z" {
			continue
		}
		if s, err := strconv.Atoi(fields[3]); err == nil && s == sid {
			pids = append(pids, pid)
		}
	}
	return pids
}
