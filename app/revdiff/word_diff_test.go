package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptions_WordDiffOnByDefault(t *testing.T) {
	assert.True(t, options{}.wordDiff(), "on by default")
	assert.False(t, options{NoWordDiff: true}.wordDiff(), "--no-word-diff turns it off")
	assert.True(t, options{WordDiff: true}.wordDiff())

	opts, err := parseArgs([]string{"--no-word-diff"})
	require.NoError(t, err)
	assert.False(t, opts.wordDiff(), "the flag parses")
}
