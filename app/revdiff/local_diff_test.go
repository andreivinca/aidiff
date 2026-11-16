package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/ui"
)

// branchRepo is a clone on branch feature, which tracks origin/feature and
// has one commit more than it, plus an uncommitted edit; main gained a
// commit after feature branched off. It returns the clone and the commit
// feature branched from.
func branchRepo(t *testing.T) (work, forkPoint string) {
	t.Helper()
	dir := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec // test-controlled arguments
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	commit := func(name string) {
		require.NoError(t, os.WriteFile(filepath.Join(work, name), []byte(name+"\n"), 0o600))
		git(work, "add", name)
		git(work, "commit", "-qm", name)
	}
	origin := filepath.Join(dir, "origin.git")
	work = filepath.Join(dir, "work")
	git(dir, "init", "-q", "--bare", "-b", "main", origin)
	git(dir, "init", "-q", "-b", "main", work)
	commit("a.txt")
	git(work, "remote", "add", "origin", origin)
	git(work, "push", "-q", "-u", "origin", "main")
	git(work, "remote", "set-head", "origin", "main")
	forkPoint = git(work, "rev-parse", "HEAD")
	git(work, "checkout", "-q", "-b", "feature")
	commit("pushed.txt")
	git(work, "push", "-q", "-u", "origin", "feature")
	commit("unpushed.txt")
	git(work, "checkout", "-q", "main")
	commit("main-only.txt")
	git(work, "push", "-q", "origin", "main")
	git(work, "checkout", "-q", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(work, "a.txt"), []byte("edited\n"), 0o600))
	return work, forkPoint
}

func TestLocalGit(t *testing.T) {
	work, forkPoint := branchRepo(t)
	g := localGit{dir: work}
	ctx := context.Background()
	refs, err := g.refs(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []localRef{
		{name: "feature", current: true}, {name: "main"},
		{name: "origin/feature", remote: true}, {name: "origin/main", remote: true},
	}, refs, "local and remote branches, without origin/HEAD")
	assert.Equal(t, "origin/feature", g.upstream(ctx))
	assert.Equal(t, "origin/main", g.defaultBranch(ctx))
	base, err := g.mergeBase(ctx, "origin/main")
	require.NoError(t, err)
	assert.Equal(t, forkPoint, base, "where feature left main, not main's tip")
	_, err = g.mergeBase(ctx, "no-such-branch")
	require.ErrorContains(t, err, "no common commit with no-such-branch")
}

// runCmd runs cmd and the commands its batch holds, returning their messages.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestLocalDiff(t *testing.T) {
	work, _ := branchRepo(t)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(work)
	opts, err := parseArgs(noConfigArgs(t))
	require.NoError(t, err)
	src, err := setupSource(opts)
	require.NoError(t, err)
	l := newLocalDiff(newReviewDeps(opts), src, "")
	require.NotNil(t, l)
	assert.Equal(t, splitview.Command{Name: "Diff…", Detail: "unstaged"}, withoutRun(l.command()), "the run's own diff")

	picker, ok := l.pick(nil)().(splitview.ShowPickerMsg)
	require.True(t, ok)
	var labels, details []string
	for _, it := range picker.Items {
		labels, details = append(labels, it.Label), append(details, it.Detail)
	}
	assert.Equal(t, []string{"Uncommitted changes", "Unstaged changes", "Staged changes", "Unpushed changes", "Branch changes",
		"Against a branch…"}, labels)
	assert.Equal(t, "vs origin/feature, uncommitted included", details[3])
	assert.Equal(t, "vs origin/main, uncommitted included", details[4])

	var replaced bool
	for _, m := range runCmd(picker.OnPick(picker.Items[4])) {
		if r, ok := m.(splitview.ReplaceInnerMsg); ok {
			_, replaced = r.Model.(ui.Model)
		}
	}
	assert.True(t, replaced, "the review is rebuilt")
	assert.Equal(t, "vs origin/main", l.command().Detail)

	branches, ok := runCmd(picker.OnPick(picker.Items[5]))[0].(splitview.ShowPickerMsg)
	require.True(t, ok)
	var names []string
	for _, it := range branches.Items {
		names = append(names, it.Label)
	}
	assert.ElementsMatch(t, []string{"main", "origin/feature", "origin/main"}, names, "every branch but the current one")
	msgs := runCmd(branches.OnText("no-such-ref"))
	assert.Contains(t, msgs, splitview.FlashMsg{Text: "Diff: no common commit with no-such-ref: git merge-base: fatal: Not a valid object name no-such-ref"})
	assert.Equal(t, "vs origin/main", l.command().Detail, "a failed switch keeps the review as it was")
}

func TestNewLocalDiff_OnlyForGitChanges(t *testing.T) {
	assert.Nil(t, newLocalDiff(reviewDeps{}, sourceSetup{}, ""), "no repository")
	git := sourceSetup{}
	git.vcsType = diff.VCSGit
	assert.NotNil(t, newLocalDiff(reviewDeps{}, git, ""))
	deps := reviewDeps{}
	deps.opts.Stdin = true
	assert.Nil(t, newLocalDiff(deps, git, ""), "stdin has nothing to compare")
	deps = reviewDeps{}
	deps.opts.AllFiles = true
	assert.Nil(t, newLocalDiff(deps, git, ""), "every file is not a diff")
}

func withoutRun(c splitview.Command) splitview.Command {
	c.Run = nil
	return c
}
