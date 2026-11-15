package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/t3client"
)

func TestParseArgs_T3Flags(t *testing.T) {
	opts, err := parseArgs(append(noConfigArgs(t), "--t3", "ssh://boxdev", "--t3-thread", "abc"))
	require.NoError(t, err)
	assert.True(t, opts.t3Mode())
	assert.Equal(t, "ssh://boxdev", opts.T3)
	assert.Equal(t, t3client.SourceBranchRange, opts.t3SourceKind(), "branch is the default")

	opts, err = parseArgs(append(noConfigArgs(t), "--t3=local", "--t3-source=uncommitted"))
	require.NoError(t, err)
	assert.Equal(t, t3client.SourceWorkingTree, opts.t3SourceKind())

	opts, err = parseArgs(noConfigArgs(t))
	require.NoError(t, err)
	assert.False(t, opts.t3Mode())

	_, err = parseArgs(append(noConfigArgs(t), "--t3=local", "--t3-source=bogus"))
	require.Error(t, err)
}

func TestParseArgs_T3Conflicts(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "refs", args: []string{"HEAD~1"}, want: "--t3 cannot be used with refs"},
		{name: "staged", args: []string{"--staged"}, want: "--t3 cannot be used with --staged"},
		{name: "only", args: []string{"--only", "a.go"}, want: "--t3 cannot be used with --only"},
		{name: "all-files", args: []string{"--all-files"}, want: "--t3 cannot be used with --all-files"},
		{name: "stdin", args: []string{"--stdin"}, want: "--t3 cannot be used with --stdin"},
		{name: "include", args: []string{"--include", "src"}, want: "--t3 cannot be used with --include"},
		{name: "exclude", args: []string{"--exclude", "vendor"}, want: "--t3 cannot be used with --exclude"},
		{name: "annotations", args: []string{"--annotations", "/tmp/a.md"}, want: "--t3 cannot be used with --annotations"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append(append(noConfigArgs(t), "--t3", "local"), tt.args...)
			_, err := parseArgs(args)
			require.ErrorContains(t, err, tt.want)
		})
	}

	_, err := parseArgs(append(noConfigArgs(t), "--t3-thread", "x", "--staged"))
	require.ErrorContains(t, err, "--t3 cannot be used with --staged", "--t3-thread alone selects T3 mode")
}

func TestT3Trunc(t *testing.T) {
	assert.Equal(t, "short", trunc("short", 10))
	assert.Equal(t, "abcd…", trunc("abcdefgh", 5))
}
