package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/t3palette"
)

// stubSession names a server; nothing else of it is used.
type stubSession struct {
	t3palette.Session
	label string
}

func (s *stubSession) Label() string {
	return s.label
}

func (s *stubSession) MachineName() string {
	return "box"
}

// stubReview is a review of /srv/wt, the worktree of thread abc, on ssh://box.
func stubReview() *t3palette.Review {
	th := t3client.Thread{ID: "abc", Title: "Fix login"}
	return &t3palette.Review{
		Session: &stubSession{label: "ssh://box"},
		Target:  t3client.Target{Cwd: "/srv/wt", Thread: &th, Project: t3client.Project{Title: "Sabra2"}},
	}
}

func TestSourceSetup_HistoryFilesTheReviewOnScreen(t *testing.T) {
	src := sourceSetup{vcsSetup: vcsSetup{gitRoot: "/repo", workDir: "/repo/sub"}}
	local := src.history(options{}, nil)
	assert.Equal(t, "/repo", local.gitRoot, "the run's own review is filed as its source")
	assert.Equal(t, "/repo/sub", local.workDir)
	assert.Empty(t, local.t3Path)

	remote := src.history(options{}, stubReview())
	assert.Empty(t, remote.gitRoot, "a T3 review has no local repository")
	assert.Equal(t, "ssh://box:/srv/wt", remote.t3Path)
}

func TestSaveHistory_T3ReviewIsFiledByServerAndFolder(t *testing.T) {
	dir := t.TempDir()
	saveHistory(histReq{opts: options{HistoryDir: dir}, annotations: "note\n", t3Path: t3HistoryPath(stubReview())})
	entries, err := os.ReadDir(filepath.Join(dir, "wt"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	data, err := os.ReadFile(filepath.Join(dir, "wt", entries[0].Name())) //nolint:gosec // a file this test wrote
	require.NoError(t, err)
	assert.Contains(t, string(data), "path: ssh://box:/srv/wt\n")
}

func TestSourceSetup_T3Review(t *testing.T) {
	src := sourceSetup{t3: stubReview()}
	assert.Equal(t, "box", src.machineName("here"))
	desc := src.reviewDescription("mine")
	assert.Contains(t, desc, "**T3 Code**: Sabra2 / Fix login")
	assert.Contains(t, desc, "`/srv/wt` on ssh://box")
	assert.Contains(t, desc, "thread `abc`")
	assert.Contains(t, desc, "\n\nmine")
	assert.Equal(t, "mine", sourceSetup{}.reviewDescription("mine"), "a local review keeps the user's text alone")
}
