package termpane

import (
	"syscall"
	"time"
)

// killGrace is how long processes get to exit after SIGHUP before SIGKILL.
const killGrace = time.Second

// killPoll is how often killSession looks whether the session has emptied.
const killPoll = 20 * time.Millisecond

// killSession hangs up every live process in the child's session (the child is
// the session leader, so the session id is its pid), waits up to killGrace for
// them to go, then kills the survivors. It must run before the leader is
// reaped: the unreaped leader keeps the session id from being reused.
func (p *Pane) killSession() {
	sid := p.cmd.Process.Pid
	if signalSession(sid, syscall.SIGHUP) == 0 {
		return
	}
	deadline := time.Now().Add(killGrace)
	for time.Now().Before(deadline) {
		if len(sessionMembers(sid)) == 0 {
			return
		}
		time.Sleep(killPoll)
	}
	signalSession(sid, syscall.SIGKILL)
}

// signalSession sends sig to every live process in session sid and reports
// how many it found.
func signalSession(sid int, sig syscall.Signal) int {
	pids := sessionMembers(sid)
	for _, pid := range pids {
		_ = syscall.Kill(pid, sig)
	}
	return len(pids)
}
