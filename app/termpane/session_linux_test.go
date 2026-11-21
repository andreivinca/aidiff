package termpane

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// jobPID starts script, which prints "PID=<n>" for a job it leaves running,
// and returns the pane and that pid.
func jobPID(t *testing.T, script string) (*Pane, int) {
	t.Helper()
	p, err := Start([]string{"sh", "-c", script}, t.TempDir(), 40, 4)
	require.NoError(t, err)
	waitFor(t, p, "PID=")
	var pid int
	for f := range strings.FieldsSeq(screen(p)) {
		if v, ok := strings.CutPrefix(f, "PID="); ok {
			pid, err = strconv.Atoi(v)
			require.NoError(t, err)
		}
	}
	require.NotZero(t, pid)
	return p, pid
}

// alive reports whether pid exists and is not a zombie.
func alive(pid int) bool {
	_, ok := liveSession(pid)
	return ok
}

// dead reports whether pid has gone.
func dead(pid int) func() bool {
	return func() bool {
		return !alive(pid)
	}
}

func TestPane_CloseLeavesNoProcessBehind(t *testing.T) {
	// a background job in its own process group that ignores SIGHUP: closing
	// the pty alone would leave it running.
	p, pid := jobPID(t, `trap "" HUP; sleep 300 & echo "PID=$!"; wait`)
	require.Contains(t, sessionMembers(p.cmd.Process.Pid), pid, "the job belongs to the terminal's session")

	start := time.Now()
	p.Close()
	assert.Less(t, time.Since(start), killGrace+time.Second, "close does not hang")
	assert.Eventually(t, dead(pid), 2*time.Second, 10*time.Millisecond, "the SIGHUP-ignoring job was killed")
}

func TestPane_CloseAfterLeaderExitKillsItsJobs(t *testing.T) {
	// the leader exits on its own and leaves a job behind: Close still finds
	// the job through the session, which the unreaped leader keeps reserved.
	p, pid := jobPID(t, `trap "" HUP; sleep 300 & echo "PID=$!"`)
	require.Eventually(t, p.Exited, 3*time.Second, 10*time.Millisecond)
	require.True(t, alive(pid), "the job outlives the leader")

	p.Close()
	assert.Eventually(t, dead(pid), 2*time.Second, 10*time.Millisecond, "the job left by the exited leader was killed")
}

func TestPane_CloseReleasesTheReaderWhileADaemonHoldsThePty(t *testing.T) {
	setsid, err := exec.LookPath("setsid")
	if err != nil {
		t.Skip("setsid not installed")
	}
	// the daemon leaves the terminal's session, so Close does not end it,
	// and keeps the pty's other end open
	p, pid := jobPID(t, setsid+` sh -c 'echo "PID=$$"; exec sleep 20' & sleep 0.2; exit 0`)
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	})
	require.True(t, alive(pid))
	require.NotContains(t, sessionMembers(p.cmd.Process.Pid), pid, "the daemon left the terminal's session")
	p.Resize(20, 3) // must not put the master back in blocking mode

	start := time.Now()
	p.Close()
	assert.Less(t, time.Since(start), ioStopWait, "close does not wait out the output reader")
	select {
	case <-p.readerDone:
	case <-time.After(time.Second):
		t.Fatal("the output reader outlives Close while a daemon holds the pty")
	}
	assert.True(t, alive(pid), "the daemon was not the terminal's to end")
}

func TestPane_LeaderExitIsNotGuessedWhenWaitidFails(t *testing.T) {
	orig := waitid
	t.Cleanup(func() {
		waitid = orig
	})
	waitid = func(int, *unix.Siginfo) error {
		return unix.ECHILD // as when the child was reaped elsewhere
	}
	p, err := Start([]string{"sh", "-c", "echo READY; read x"}, t.TempDir(), 30, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)
	waitFor(t, p, "READY")

	select {
	case <-p.leaderGone:
		t.Fatal("the shell still runs, yet its exit was reported")
	case <-time.After(3 * exitPoll):
	}
	assert.False(t, p.Exited())

	p.SendText("\r")
	select {
	case <-p.leaderGone:
	case <-time.After(3 * time.Second):
		t.Fatal("the shell's exit was never noticed")
	}
}
