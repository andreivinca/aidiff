package termpane

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitFor polls the plain-text screen until it contains want.
func waitFor(t *testing.T, p *Pane, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if screen := ansi.Strip(p.Render(false)); strings.Contains(screen, want) {
			return screen
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("screen never contained %q; last screen:\n%s", want, ansi.Strip(p.Render(false)))
	return ""
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
	p, err := Start([]string{"sh", "-c", "stty -echo; cat -v"}, t.TempDir(), 40, 6)
	require.NoError(t, err)
	defer p.Close()

	time.Sleep(200 * time.Millisecond) // let stty run before typing
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

func TestPane_CloseLeavesNoProcessBehind(t *testing.T) {
	// a background job in its own process group that ignores SIGHUP: closing
	// the pty alone would leave it running.
	p, err := Start([]string{"sh", "-c", `trap "" HUP; sleep 300 & echo "PID=$!"; wait`}, t.TempDir(), 40, 4)
	require.NoError(t, err)

	screen := waitFor(t, p, "PID=")
	var pid int
	for _, f := range strings.Fields(screen) {
		if strings.HasPrefix(f, "PID=") {
			pid, err = strconv.Atoi(strings.TrimPrefix(f, "PID="))
			require.NoError(t, err)
		}
	}
	require.NotZero(t, pid)
	require.Contains(t, sessionMembers(p.cmd.Process.Pid), pid, "the job belongs to the terminal's session")

	start := time.Now()
	p.Close()
	assert.Less(t, time.Since(start), killGrace+time.Second, "close does not hang")

	deadline := time.Now().Add(2 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	assert.False(t, alive(pid), "the SIGHUP-ignoring job was killed")
}

// alive reports whether pid exists and is not a zombie.
func alive(pid int) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	end := bytes.LastIndexByte(stat, ')')
	fields := strings.Fields(string(stat[end+1:]))
	return len(fields) > 0 && fields[0] != "Z"
}
