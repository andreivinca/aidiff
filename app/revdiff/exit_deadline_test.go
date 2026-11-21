package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDeadline is an exitDeadline that records its exit instead of exiting.
type testDeadline struct {
	*exitDeadline
	mu     sync.Mutex
	codes  []int
	exited chan struct{}
	log    bytes.Buffer
}

func newTestDeadline(t *testing.T) *testDeadline {
	t.Helper()
	td := &testDeadline{exited: make(chan struct{}, 1)}
	td.exitDeadline = &exitDeadline{
		dir: t.TempDir(),
		exit: func(code int) {
			td.mu.Lock()
			td.codes = append(td.codes, code)
			td.mu.Unlock()
			td.exited <- struct{}{}
		},
		log:  &syncWriter{w: &td.log, mu: &td.mu},
		dump: func(w io.Writer) error { _, err := io.WriteString(w, "goroutine 1 [running]:\n"); return err },
	}
	return td
}

type syncWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func TestExitDeadline_ExpiresWithDump(t *testing.T) {
	td := newTestDeadline(t)
	td.arm(10 * time.Millisecond)
	select {
	case <-td.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("deadline never expired")
	}
	td.mu.Lock()
	defer td.mu.Unlock()
	assert.Equal(t, []int{1}, td.codes)
	dumps, err := filepath.Glob(filepath.Join(td.dir, "aidiff-shutdown-*.txt"))
	require.NoError(t, err)
	require.Len(t, dumps, 1)
	assert.Contains(t, td.log.String(), dumps[0], "the note names the file")
	data, err := os.ReadFile(dumps[0])
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "goroutine 1"))
}

func TestExitDeadline_DumpFollowsNoPlantedLink(t *testing.T) {
	td := newTestDeadline(t)
	victim := filepath.Join(t.TempDir(), "victim")
	require.NoError(t, os.WriteFile(victim, []byte("keep"), 0o600))
	// the name the dump once had, predictable from the pid
	require.NoError(t, os.Symlink(victim, filepath.Join(td.dir, "aidiff-shutdown-"+strconv.Itoa(os.Getpid())+".txt")))
	td.expireNow()
	<-td.exited
	data, err := os.ReadFile(victim) //nolint:gosec // test temp dir
	require.NoError(t, err)
	assert.Equal(t, "keep", string(data), "the dump goes to a new file of its own")
}

func TestExitDeadline_ExitsWithTheCodeFinalizeComputed(t *testing.T) {
	td := newTestDeadline(t)
	code, err := td.result(exitCodeAnnotations, nil)
	require.NoError(t, err)
	assert.Equal(t, exitCodeAnnotations, code, "the result is passed on")
	td.expireNow() // a close after finalize hung
	<-td.exited
	td.mu.Lock()
	assert.Equal(t, []int{exitCodeAnnotations}, td.codes, "-o is written: the launcher still reads annotations")
	td.mu.Unlock()

	td = newTestDeadline(t)
	_, err = td.result(0, errors.New("write output: disk full"))
	require.Error(t, err)
	td.expireNow()
	<-td.exited
	td.mu.Lock()
	defer td.mu.Unlock()
	assert.Equal(t, []int{1}, td.codes, "a failed finalize exits 1")
}

func TestExitDeadline_DisarmPreventsExit(t *testing.T) {
	td := newTestDeadline(t)
	td.arm(20 * time.Millisecond)
	td.disarm()
	select {
	case <-td.exited:
		t.Fatal("a disarmed deadline exited")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestExitDeadline_ArmAfterDisarmDoesNothing(t *testing.T) {
	td := newTestDeadline(t)
	td.disarm()
	td.arm(time.Millisecond)
	select {
	case <-td.exited:
		t.Fatal("arming after disarm exited")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDumpGoroutines(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, dumpGoroutines(&buf))
	assert.Contains(t, buf.String(), "TestDumpGoroutines")
}

func TestShutdownGuard_FirstSignalArmsDeadline(t *testing.T) {
	td := newTestDeadline(t)
	g := shutdownGuard{deadline: td.exitDeadline}
	quits := 0
	g.trigger(func() { quits++ })
	assert.Equal(t, 1, quits)
	td.exitDeadline.mu.Lock()
	armed := td.timer != nil
	td.exitDeadline.mu.Unlock()
	assert.True(t, armed, "a quit that never lands must still end the process")
	td.disarm()
}

func TestShutdownGuard_SecondSignalExitsAtOnce(t *testing.T) {
	td := newTestDeadline(t)
	g := shutdownGuard{deadline: td.exitDeadline}
	quits := 0
	g.trigger(func() { quits++ })
	g.trigger(func() { quits++ })
	assert.Equal(t, 1, quits, "the quit itself runs once")
	select {
	case <-td.exited:
	case <-time.After(time.Second):
		t.Fatal("a second signal did not exit")
	}
	td.mu.Lock()
	defer td.mu.Unlock()
	assert.Equal(t, []int{1}, td.codes)
}

func TestShutdownGuard_NoDeadlineOnlyQuits(t *testing.T) {
	var g shutdownGuard
	quits := 0
	g.trigger(func() { quits++ })
	g.trigger(func() { quits++ })
	assert.Equal(t, 1, quits)
}
