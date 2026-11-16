package t3palette

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/t3client"
)

func TestServerSpec(t *testing.T) {
	assert.Equal(t, "ssh://boxdev", serverSpec(" boxdev "))
	assert.Equal(t, "local", serverSpec("local"))
	assert.Equal(t, "https://box.ts.net:3000", serverSpec("https://box.ts.net:3000"))
}

func TestServerItems_SavedLoginsListedOnce(t *testing.T) {
	store := t3client.TokenStore{Path: filepath.Join(t.TempDir(), "t3-tokens.json")}
	require.NoError(t, store.Put("ssh://boxdev", "tok"))
	labels := map[string]string{}
	for _, it := range serverItems(store) {
		_, dup := labels[it.Label]
		assert.False(t, dup, "label %q appears twice", it.Label)
		labels[it.Label] = it.Detail
	}
	assert.Equal(t, "saved login", labels["ssh://boxdev"])
}

func TestThreadAndProjectItems(t *testing.T) {
	archived := "2026-01-01T00:00:00.000Z"
	branch, run := "feature/x", "run-1"
	sh := t3client.Shell{
		Projects: []t3client.Project{
			{ID: "p2", Title: "zeta", WorkspaceRoot: "/srv/zeta"},
			{ID: "p1", Title: "Alpha", WorkspaceRoot: "/srv/alpha"},
			{ID: "p3", Title: "old", WorkspaceRoot: "/srv/old", ArchivedAt: &archived},
		},
		Threads: []t3client.Thread{
			{ID: "t-old", ProjectID: "p1", Title: "Older", UpdatedAt: "2026-11-14T10:00:00Z"},
			{ID: "t-new", ProjectID: "p2", Branch: &branch, ActiveRunID: &run, UpdatedAt: "2026-11-15T10:00:00Z"},
		},
	}
	items := threadItems(sh, t3client.SortedThreads(sh), "t-old")
	require.Len(t, items, 2)
	assert.Equal(t, "t-new", items[0].Label, "an untitled thread shows its id")
	assert.Equal(t, "zeta · running · feature/x", items[0].Detail)
	assert.Equal(t, "▸ Older", items[1].Label, "the reviewed thread is marked")
	assert.Equal(t, "Alpha · idle", items[1].Detail)

	projects := projectItems(sh)
	require.Len(t, projects, 2, "archived projects are left out")
	assert.Equal(t, "Alpha", projects[0].Label)
	assert.Equal(t, "/srv/zeta", projects[1].Detail)

	assert.Empty(t, projectThreads(sh, &t3client.Project{ID: "none"}))
	assert.Equal(t, "/srv/", startFolder(t3client.Shell{Projects: []t3client.Project{{WorkspaceRoot: "/srv/alpha"}}}))
	assert.Equal(t, "/", startFolder(t3client.Shell{}))
}

func TestWriteThreads(t *testing.T) {
	var b strings.Builder
	require.NoError(t, WriteThreads(&b, t3client.Shell{}))
	assert.Equal(t, "no threads\n", b.String())

	b.Reset()
	require.NoError(t, WriteThreads(&b, testShell()))
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	require.Len(t, lines, 2)
	assert.True(t, strings.HasPrefix(lines[0], loginID+"  idle"), "newest first: %q", lines[0])
	assert.Contains(t, lines[0], "Sabra2")
	assert.Contains(t, lines[0], "Fix login")
}

func TestTrunc(t *testing.T) {
	assert.Equal(t, "short", trunc("short", 10))
	assert.Equal(t, "abcd…", trunc("abcdefgh", 5))
}
