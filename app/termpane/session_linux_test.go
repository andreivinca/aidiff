package termpane

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
