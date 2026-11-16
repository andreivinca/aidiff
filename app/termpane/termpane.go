// Package termpane runs a command inside a pseudo-terminal and keeps an
// in-memory terminal emulator of its screen, so a TUI can draw a live
// terminal inside one of its panes.
package termpane

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// frameInterval caps how often the pane asks the UI to redraw while the
// child is producing output. The first change after a quiet spell is
// reported at once (a keystroke echo must not wait for a tick); only
// follow-ups within the same frame are held back and coalesced.
const frameInterval = 16 * time.Millisecond

// scrollbackLines is how many lines that scrolled off the top the emulator
// keeps. The pane never shows them, but the emulator cannot turn its
// scrollback off (zero means its 10000-line default), so keep the minimum.
const scrollbackLines = 1

// readBufSize is how much child output one read takes in.
const readBufSize = 32 * 1024

// ioStopWait bounds how long Close waits for the I/O goroutines once the pty
// is closed, so a wedged read cannot hang the UI.
const ioStopWait = time.Second

// Pane is a running command attached to a pseudo-terminal plus the emulated
// screen it draws into. All methods are safe for concurrent use.
//
// Input never waits on the child: keys, pastes and the emulator's own replies
// go through the emulator's in-memory pipe into an unbounded queue, and a
// separate goroutine writes that queue to the pty. A child that stops reading
// its input (or a paste larger than the pty buffer) therefore stalls only that
// writer, never a caller holding mu.
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

	input inputQueue

	dirty      atomic.Bool
	exited     atomic.Bool
	kick       chan struct{} // wakes notifyLoop after dirty is set
	updates    chan struct{} // closed when notifyLoop ends, after Close
	done       chan struct{} // closed by Close
	readerDone chan struct{} // closed when readOutput stops writing to emu
	writerDone chan struct{} // closed when writeInput stops writing to the pty
	leaderGone chan struct{} // closed once the child has exited (not yet reaped)

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

	// no context: the child lives until Close, which ends its whole session;
	// a context would only kill the leader and leave its jobs behind.
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec,noctx // user-chosen command, run on purpose; Close ends it
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(height), Cols: uint16(width)}) //nolint:gosec // sizes are small positive ints
	if err != nil {
		return nil, fmt.Errorf("start %s in pty: %w", argv[0], err)
	}

	p := newPane(ptmx, width, height)
	p.cmd = cmd
	p.startIO()
	go p.watchLeader()
	return p, nil
}

// newPane builds a pane around an open pty master; startIO sets it running.
func newPane(ptmx *os.File, width, height int) *Pane {
	emu := vt.NewEmulator(width, height)
	emu.SetScrollbackSize(scrollbackLines)
	return &Pane{
		emu:        emu,
		ptmx:       ptmx,
		input:      newInputQueue(),
		kick:       make(chan struct{}, 1),
		updates:    make(chan struct{}, 1),
		done:       make(chan struct{}),
		readerDone: make(chan struct{}),
		writerDone: make(chan struct{}),
		leaderGone: make(chan struct{}),
	}
}

// startIO starts the goroutines that move bytes between the pty, the
// emulator and the UI.
func (p *Pane) startIO() {
	go p.readOutput()
	go p.queueInput()
	go p.writeInput()
	go p.notifyLoop()
}

// watchLeader reports the child's exit without reaping it: while the leader
// is an unreaped zombie its pid, which is also the session and process group
// id, cannot be reused, so Close can still signal the session safely.
func (p *Pane) watchLeader() {
	defer close(p.leaderGone)
	waitExited(p.cmd.Process.Pid)
	p.exited.Store(true)
	p.markDirty()
}

// readOutput copies everything the child writes into the emulator.
func (p *Pane) readOutput() {
	defer close(p.readerDone)
	buf := make([]byte, readBufSize)
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

// queueInput drains the emulator's input pipe (keys, pastes, and replies such
// as cursor position reports) into the input queue. It never waits on the
// child, so a pipe write made under mu always completes. It ends when Close
// closes the pipe.
func (p *Pane) queueInput() {
	buf := make([]byte, readBufSize)
	for {
		n, err := p.emu.Read(buf)
		if n > 0 {
			p.input.push(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// writeInput writes queued input to the child. When the pty refuses input
// the child can no longer be reached: the queue drops everything from then
// on and the pane reports itself exited.
func (p *Pane) writeInput() {
	defer close(p.writerDone)
	for {
		select {
		case <-p.done:
			return
		case <-p.input.ready:
		}
		if _, err := p.ptmx.Write(p.input.take()); err != nil {
			p.input.discard()
			p.exited.Store(true)
			p.markDirty()
			return
		}
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
// immediately unless an update already went out less than a frame ago. It
// closes updates when Close stops it, so a waiting receiver is released.
func (p *Pane) notifyLoop() {
	defer close(p.updates)
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

// Updates delivers a value whenever the screen changed since the last one,
// and is closed once the pane is closed.
func (p *Pane) Updates() <-chan struct{} {
	return p.updates
}

// Exited reports whether the child process has ended.
func (p *Pane) Exited() bool {
	return p.exited.Load()
}

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

// Close ends everything started in the terminal and releases the pty. Like
// closing a terminal window it sends SIGHUP, but to every process in the
// child's session (jobs and background processes included, which live in
// their own process groups), then SIGKILLs whatever ignored it. The child is
// reaped only after that, so the session id cannot have been reused.
func (p *Pane) Close() {
	p.closeOnce.Do(func() {
		close(p.done)
		p.killSession()
		p.reapLeader()
		p.stopIO()
	})
}

// reapLeader collects the child's exit status once killSession has ended it.
// Should the child still be running (its session could not be listed), it is
// killed directly; it is still unreaped, so its pid is still its own.
func (p *Pane) reapLeader() {
	grace := time.NewTimer(killGrace)
	defer grace.Stop()
	select {
	case <-p.leaderGone:
	case <-grace.C:
		_ = p.cmd.Process.Kill()
		<-p.leaderGone
	}
	_ = p.cmd.Wait()
}

// stopIO closes the pty and the emulator's input pipe and waits for the I/O
// goroutines to end.
func (p *Pane) stopIO() {
	_ = p.ptmx.Close() // unblocks readOutput and a writeInput stuck on a full pty
	// Emulator.Close flips an unlocked flag that Read/Write also check, so it
	// would race the copy goroutines. Closing only the input pipe (an
	// io.PipeWriter, safe across goroutines) ends queueInput with EOF.
	if pw, ok := p.emu.InputPipe().(*io.PipeWriter); ok {
		_ = pw.Close()
	}
	timeout := time.NewTimer(ioStopWait)
	defer timeout.Stop()
	for _, done := range []chan struct{}{p.readerDone, p.writerDone} {
		select {
		case <-done:
		case <-timeout.C:
			return
		}
	}
}
