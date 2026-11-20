package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptions_ShowUntrackedAtStart(t *testing.T) {
	mk := func(untracked, noUntracked, staged bool, base, against string) options {
		var o options
		o.Untracked, o.NoUntracked, o.Staged = untracked, noUntracked, staged
		o.Refs.Base, o.Refs.Against = base, against
		return o
	}
	cases := []struct {
		name string
		opts options
		want bool
	}{
		{"working tree, no flags", mk(false, false, false, "", ""), true},
		{"working tree against a ref", mk(false, false, false, "main", ""), true},
		{"--no-untracked", mk(false, true, false, "", ""), false},
		{"--no-untracked against a ref", mk(false, true, false, "main", ""), false},
		{"--staged", mk(false, false, true, "", ""), false},
		{"--staged --untracked", mk(true, false, true, "", ""), true},
		{"two refs", mk(false, false, false, "main", "feature"), false},
		{"dot-dot range", mk(false, false, false, "main..feature", ""), false},
		{"two refs --untracked", mk(true, false, false, "main", "feature"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.opts.showUntrackedAtStart())
		})
	}
}
