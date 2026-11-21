package termpane

import (
	"errors"

	"golang.org/x/sys/unix"
)

// darwinZombie is SZOMB from <sys/proc.h>: a process that exited and waits
// to be reaped.
const darwinZombie = 5

// sessionMembers lists the live (non-zombie) processes whose session id is
// sid. It returns nil when the process table cannot be read.
func sessionMembers(sid int) []int {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	var pids []int
	for i := range procs {
		pid := int(procs[i].Proc.P_pid)
		if procs[i].Proc.P_stat == darwinZombie {
			continue
		}
		if s, err := unix.Getsid(pid); err == nil && s == sid {
			pids = append(pids, pid)
		}
	}
	return pids
}

// waitExited blocks until child pid has exited, leaving it unreaped.
func waitExited(pid int) {
	if !watchExit(pid) {
		pollExited(pid) // kqueue cannot watch it (no descriptor left, ...)
	}
}

// watchExit waits for pid's exit through kqueue, which, unlike wait4, does
// not reap, and reports whether it could watch it.
func watchExit(pid int) bool {
	kq, err := unix.Kqueue()
	if err != nil {
		return false
	}
	defer func() {
		_ = unix.Close(kq)
	}()
	watch := []unix.Kevent_t{{
		Ident:  uint64(pid), //nolint:gosec // a pid is positive
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ONESHOT,
		Fflags: unix.NOTE_EXIT,
	}}
	events := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, watch, events, nil)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.ESRCH):
			return true // already gone when registering
		case err != nil:
			return false
		case n > 0 && events[0].Flags&unix.EV_ERROR != 0:
			// a registration error comes back as an event
			return events[0].Data == int64(unix.ESRCH)
		}
		return true // exited
	}
}

// running reports whether pid exists and has not exited; a zombie has.
func running(pid int) bool {
	if errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
		return false
	}
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return true // gone meanwhile, which the next look tells
	}
	return p.Proc.P_stat != darwinZombie
}
