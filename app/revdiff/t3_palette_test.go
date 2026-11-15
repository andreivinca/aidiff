package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

func commandNames(cmds []splitview.Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.Name
	}
	return out
}

func TestT3Palette_CommandsFollowState(t *testing.T) {
	p := newT3Palette(options{}, reviewDeps{}, "", sourceSetup{})
	assert.Equal(t, []string{"T3: Connect to server…"}, commandNames(p.commands()))
	assert.False(t, p.history().t3)

	session := &t3client.Session{Label: "ssh://boxdev"}
	p.adoptSession(session)
	p.program = nil
	p.shellAt = time.Now() // fresh enough: no background fetch against the fake session
	assert.Equal(t, []string{"T3: New thread…", "T3: Projects…", "T3: Connect to another server…"},
		commandNames(p.commands()), "connected, no threads known yet")
	assert.Equal(t, "ssh://boxdev", p.commands()[0].Detail)
	assert.False(t, p.history().t3, "the review on screen is still the local one")

	branch := "main"
	th := t3client.Thread{ID: "abcdef123456", ProjectID: "p1", Title: "Fix login", Branch: &branch, UpdatedAt: "2026-11-15T12:00:00Z"}
	older := t3client.Thread{ID: "older", ProjectID: "p1", Title: "Older work", UpdatedAt: "2026-11-14T12:00:00Z"}
	p.mu.Lock()
	p.setShellLocked(t3client.Shell{Projects: []t3client.Project{{ID: "p1", Title: "Sabra2"}}, Threads: []t3client.Thread{older, th}})
	p.mu.Unlock()
	p.cur = &t3Review{session: session, target: t3client.Target{Thread: &th}}
	assert.Equal(t, []string{"T3: New thread…", "T3: Projects…", "T3: ▸ Fix login", "T3: Older work", "T3: Open chat",
		"T3: Send message to agent…", "T3: Send annotations to agent", "T3: Connect to another server…"},
		commandNames(p.commands()), "the sessions follow the new-thread entry, newest first, the open one marked")
	assert.Equal(t, "Sabra2 · idle · main", p.commands()[2].Detail)
	assert.Equal(t, "Fix login", p.commands()[4].Detail)

	picker, ok := p.threadList(session, p.shell, nil).(splitview.ShowPickerMsg)
	require.True(t, ok)
	assert.Equal(t, "New thread…", picker.Items[0].Label, "the picker leads with a new thread too")
	assert.Equal(t, "▸ Fix login", picker.Items[1].Label)
	assert.Equal(t, "Older work", picker.Items[2].Label)

	projects, ok := p.projectList(session, p.shell).(splitview.ShowPickerMsg)
	require.True(t, ok)
	assert.Equal(t, "New project…", projects.Items[0].Label, "connecting leads with a new project")
	assert.Equal(t, "Sabra2", projects.Items[1].Label)
	assert.Contains(t, projects.Items[1].Detail, "2 threads")

	other := t3client.Project{ID: "p2", Title: "Other"}
	p.selectProject(other)
	assert.Equal(t, "Other", p.commands()[0].Detail, "new threads start in the chosen project")
	assert.NotContains(t, commandNames(p.commands()), "T3: Older work", "and only its sessions are listed")
	assert.Empty(t, projectThreads(p.shell, &other))
	assert.Equal(t, "/srv/", startFolder(t3client.Shell{Projects: []t3client.Project{{WorkspaceRoot: "/srv/alpha"}}}))
	assert.Equal(t, "/", startFolder(t3client.Shell{}))
}

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
}
