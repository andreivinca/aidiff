package main

import (
	"bytes"
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
	path := filepath.Join(td.dir, "aidiff-shutdown-"+strconv.Itoa(os.Getpid())+".txt")
	assert.Contains(t, td.log.String(), path)
	data, err := os.ReadFile(path) //nolint:gosec // test temp dir
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "goroutine 1"))
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
