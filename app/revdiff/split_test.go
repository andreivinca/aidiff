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

// TestSplitKeys_AreDistinct: every split key does one thing, close-tab
// included.
func TestSplitKeys_AreDistinct(t *testing.T) {
	k := splitKeys()
	keys := []string{k.Palette, k.Sidebar, k.Switch, k.SwitchAlt, k.FocusLeftAlt, k.FocusAlt, k.CloseTab}
	seen := map[string]bool{}
	for _, key := range keys {
		assert.NotEmpty(t, key)
		assert.False(t, seen[key], "%s is bound twice", key)
		seen[key] = true
	}
	assert.Equal(t, "alt+w", k.CloseTab)
}
