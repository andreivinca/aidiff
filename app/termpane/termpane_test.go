package termpane

import (
	"os"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// screen is the pane's plain text.
func screen(p *Pane) string {
	return ansi.Strip(p.Render(false))
}

// waitFor polls the plain-text screen until it contains want.
func waitFor(t *testing.T, p *Pane, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(screen(p), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("screen never contained %q; last screen:\n%s", want, screen(p))
}

func TestPane_RendersChildOutput(t *testing.T) {
	p, err := Start([]string{"sh", "-c", `printf 'hello \033[31mred\033[0m'; sleep 5`}, t.TempDir(), 30, 4)
	require.NoError(t, err)
	defer p.Close()

	waitFor(t, p, "hello red")

	out := p.Render(false)
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 4, "render must always be exactly height lines")
	for i, l := range lines {
		assert.Equal(t, 30, ansi.StringWidth(l), "line %d must be exactly width cells", i)
	}
	assert.Contains(t, out, "\x1b[31m", "colors from the child survive rendering")

	select {
	case <-p.Updates():
	case <-time.After(time.Second):
		t.Fatal("no update notification after output")
	}
}

func TestPane_InputReachesChild(t *testing.T) {
	p, err := Start([]string{"sh"}, t.TempDir(), 40, 6)
	require.NoError(t, err)
	defer p.Close()

	p.SendText("echo $((6*7))\r")
	waitFor(t, p, "42")
}

func TestPane_SpecialKeysAreEncoded(t *testing.T) {
	// cat -v echoes control sequences visibly: an up arrow arrives as ^[[A.
	p, err := Start([]string{"sh", "-c", "stty -echo; echo READY; cat -v"}, t.TempDir(), 40, 6)
	require.NoError(t, err)
	defer p.Close()

	waitFor(t, p, "READY") // stty has run: typing is no longer echoed raw
	p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	p.SendText("\r")
	waitFor(t, p, "^[[A")
}

func TestPane_ResizeAndExit(t *testing.T) {
	p, err := Start([]string{"sh", "-c", "stty size; exit 0"}, t.TempDir(), 50, 7)
	require.NoError(t, err)
	defer p.Close()

	waitFor(t, p, "7 50")
	deadline := time.Now().Add(3 * time.Second)
	for !p.Exited() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	assert.True(t, p.Exited())

	p.Resize(20, 3)
	assert.Len(t, strings.Split(p.Render(false), "\n"), 3)
}

func TestPane_CursorDrawnOnlyWhenRequested(t *testing.T) {
	p, err := Start([]string{"sh", "-c", "printf ab; sleep 5"}, t.TempDir(), 10, 2)
	require.NoError(t, err)
	defer p.Close()

	waitFor(t, p, "ab")
	assert.NotContains(t, p.Render(false), "\x1b[7m")
	assert.Contains(t, p.Render(true), "\x1b[7m")
}

// notifyOnly builds a Pane with just the notification plumbing running.
func notifyOnly(t *testing.T) *Pane {
	t.Helper()
	p := &Pane{kick: make(chan struct{}, 1), updates: make(chan struct{}, 1), done: make(chan struct{})}
	go p.notifyLoop()
	t.Cleanup(func() {
		close(p.done)
	})
	return p
}

func TestPane_NotifyReportsFirstChangeAtOnce(t *testing.T) {
	p := notifyOnly(t)

	select {
	case <-p.Updates():
		t.Fatal("an unchanged pane must not ask for a redraw")
	case <-time.After(3 * frameInterval):
	}

	// after a quiet spell a change (a keystroke echo) must not wait for a tick
	start := time.Now()
	p.markDirty()
	select {
	case <-p.Updates():
	case <-time.After(time.Second):
		t.Fatal("no update after a change")
	}
	assert.Less(t, time.Since(start), frameInterval, "the first change after idle is reported without waiting a frame")
}

func TestPane_NotifyCoalescesBursts(t *testing.T) {
	p := notifyOnly(t)

	p.markDirty()
	<-p.Updates()
	first := time.Now()

	// a burst right after an update is held to the next frame and merged
	for range 5 {
		p.markDirty()
	}
	select {
	case <-p.Updates():
	case <-time.After(time.Second):
		t.Fatal("burst never reported")
	}
	assert.GreaterOrEqual(t, time.Since(first), frameInterval, "updates are at most one per frame")

	select {
	case <-p.Updates():
		t.Fatal("a burst must collapse into one update")
	case <-time.After(3 * frameInterval):
	}
}

func TestPane_RenderReusesUnchangedFrame(t *testing.T) {
	p, err := Start([]string{"sh", "-c", `printf 'one'; read x; printf ' two'; sleep 5`}, t.TempDir(), 20, 3)
	require.NoError(t, err)
	defer p.Close()

	waitFor(t, p, "one")
	a, b := p.Render(true), p.Render(true)
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, p.Render(false), "cursor visibility is part of the cached frame")

	p.SendText("\r")
	waitFor(t, p, "two") // output invalidates the cached frame
}

// returnsWithin reports whether f returns within d.
func returnsWithin(d time.Duration, f func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func TestPane_InputNeverWaitsOnTheChild(t *testing.T) {
	// sleep never reads its input: in raw mode (canonical mode drops what
	// does not fit instead) writing to the pty blocks once its input buffer
	// is full. Typing and drawing must carry on regardless.
	p, err := Start([]string{"sh", "-c", "stty raw -echo; echo READY; sleep 60"}, t.TempDir(), 40, 6)
	require.NoError(t, err)
	waitFor(t, p, "READY")

	paste := strings.Repeat("x", 256*1024)
	require.True(t, returnsWithin(2*time.Second, func() {
		p.Paste(paste)
	}), "a paste larger than the pty buffer does not block")
	require.True(t, returnsWithin(2*time.Second, func() {
		p.SendText("more")
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
		p.Resize(30, 5)
		_ = p.Render(true)
	}), "nor does anything after it")
	assert.True(t, returnsWithin(killGrace+2*time.Second, p.Close), "close does not hang on the stuck input")
}

func TestPane_RefusedInputIsDropped(t *testing.T) {
	// the read end of a pipe stands in for a pty that refuses writes
	r, w, err := os.Pipe()
	require.NoError(t, err)
	p := newPane(r, 10, 2)
	p.startIO()
	t.Cleanup(func() {
		close(p.done)
		_ = w.Close()
		p.stopIO()
	})

	p.SendText("x")
	require.Eventually(t, p.Exited, time.Second, 10*time.Millisecond, "an unreachable child counts as exited")
	assert.True(t, returnsWithin(time.Second, func() {
		for range 100 {
			p.SendText(strings.Repeat("y", 1024))
		}
	}), "later input is dropped, not left blocking")
}

func TestPane_UpdatesClosedAfterClose(t *testing.T) {
	p, err := Start([]string{"sleep", "60"}, t.TempDir(), 10, 2)
	require.NoError(t, err)
	p.Close()

	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-p.Updates():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("updates stays open after Close: a waiting receiver would leak")
		}
	}
}
