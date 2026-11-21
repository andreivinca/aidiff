package termpane

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// sessionMembers lists the live (non-zombie) processes whose session id is
// sid, read from /proc. It returns nil when /proc cannot be read.
func sessionMembers(sid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if s, ok := liveSession(pid); ok && s == sid {
			pids = append(pids, pid)
		}
	}
	return pids
}

// liveSession returns the session id of pid, or false when pid is gone or a
// zombie.
func liveSession(pid int) (int, bool) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false // exited while scanning
	}
	// fields after the parenthesized command name: state ppid pgrp session ...
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, false
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) < 4 || fields[0] == "Z" {
		return 0, false
	}
	sid, err := strconv.Atoi(fields[3])
	if err != nil {
		return 0, false
	}
	return sid, true
}

// waitExited blocks until child pid has exited, leaving it unreaped.
func waitExited(pid int) {
	var info unix.Siginfo
	for {
		err := waitid(pid, &info)
		if err == nil {
			return
		}
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	pollExited(pid) // waitid cannot watch it (ECHILD: reaped elsewhere, SIGCHLD ignored)
}

// waitid waits for child pid to exit without reaping it; a variable so a
// test can make it fail.
var waitid = func(pid int, info *unix.Siginfo) error {
	return unix.Waitid(unix.P_PID, pid, info, unix.WEXITED|unix.WNOWAIT, nil)
}

// running reports whether pid exists and has not exited; a zombie has. With
// /proc unreadable, a process that exists counts as running.
func running(pid int) bool {
	if errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true // gone meanwhile, which the next look tells, or no /proc
	}
	end := bytes.LastIndexByte(stat, ')')
	fields := strings.Fields(string(stat[end+1:]))
	return len(fields) == 0 || fields[0] != "Z"
}
