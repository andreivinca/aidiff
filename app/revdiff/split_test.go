package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTerminalOutput_WritesEachSequenceWhole(t *testing.T) {
	var out writeLog
	write := terminalOutput(&out)
	write("\x1b[>11u")
	write("\x1b[<u")
	assert.Equal(t, []string{"\x1b[>11u", "\x1b[<u"}, out.writes, "one Write per sequence")
}

// writeLog records each Write.
type writeLog struct {
	writes []string
}

func (w *writeLog) Write(p []byte) (int, error) {
	w.writes = append(w.writes, string(bytes.Clone(p)))
	return len(p), nil
}
