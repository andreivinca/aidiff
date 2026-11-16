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

// waitExited blocks until child pid has exited, leaving it unreaped. It
// watches the process through kqueue, which, unlike wait4, does not reap.
func waitExited(pid int) {
	kq, err := unix.Kqueue()
	if err != nil {
		return
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
		_, err := unix.Kevent(kq, watch, events, nil)
		if !errors.Is(err, unix.EINTR) {
			return // exited, or already gone when registering (ESRCH)
		}
	}
}
