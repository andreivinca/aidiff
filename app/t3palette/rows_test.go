package t3palette

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/splitview"
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

func TestServerItems_SSHHostsGroupedApart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Host boxdev\nHost workpc\n"), 0o600))
	store := t3client.TokenStore{Path: filepath.Join(t.TempDir(), "t3-tokens.json")}
	require.NoError(t, store.Put("ssh://boxdev", "tok"))
	groups := map[string]string{}
	for _, it := range serverItems(store) {
		groups[it.Label] = it.Group
	}
	assert.Empty(t, groups["ssh://boxdev"], "a host with a saved login is a T3 server")
	assert.Equal(t, sshHostsGroup, groups["ssh://workpc"], "a bare ssh host is listed apart")
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

// connectPickerEnv points the connect picker at an empty T3 home, so no
// local server is listed, and at an ssh config naming hosts.
func connectPickerEnv(t *testing.T, hosts ...string) t3client.TokenStore {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("T3CODE_HOME", filepath.Join(home, ".t3"))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	var cfg strings.Builder
	for _, h := range hosts {
		cfg.WriteString("Host " + h + "\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(cfg.String()), 0o600))
	return t3client.TokenStore{Path: filepath.Join(home, "t3-tokens.json")}
}

func TestConnectPicker_MarksServersOnlineOrOffline(t *testing.T) {
	store := connectPickerEnv(t, "workpc")
	require.NoError(t, store.Put("ssh://box", "tok"))
	require.NoError(t, store.Put("ssh://boxdev", "tok"))
	require.NoError(t, store.Put("ssh://live", "tok"))
	h := newHarness(t, Config{Store: store, NoWatch: true, NoChat: true}, nil)
	h.connect(t, "ssh://live", newFakeSession("ssh://live", testShell()))
	var probed []string
	var mu sync.Mutex
	h.p.probe = func(_ context.Context, spec string) error {
		mu.Lock()
		defer mu.Unlock()
		probed = append(probed, spec)
		if spec == "ssh://boxdev" {
			return errors.New("T3 Code is not running on boxdev")
		}
		return nil
	}

	msgs := run(h.p.connectPicker(nil))
	assert.Equal(t, connectPickerTitle, find[splitview.ShowPickerMsg](t, msgs).Title)
	got := map[string]splitview.UpdatePickerItemMsg{}
	for _, m := range msgs {
		if u, ok := m.(splitview.UpdatePickerItemMsg); ok {
			assert.Equal(t, connectPickerTitle, u.Title)
			got[u.Label] = u
		}
	}
	assert.Equal(t, "online", got["ssh://box"].Badge)
	assert.Equal(t, "saved login", got["ssh://box"].Detail)
	assert.Empty(t, got["ssh://boxdev"].Badge)
	assert.Equal(t, "offline · saved login", got["ssh://boxdev"].Detail)
	assert.Equal(t, "online", got["ssh://live"].Badge, "a connected server is online")
	assert.NotContains(t, got, "ssh://workpc", "a bare ssh host is not probed")
	assert.ElementsMatch(t, []string{"ssh://box", "ssh://boxdev"}, probed, "a connected server is not asked")
}

func TestConnectPicker_PastedPairingLinkPairs(t *testing.T) {
	store := connectPickerEnv(t)
	h := newHarness(t, Config{Store: store, NoWatch: true, NoChat: true}, nil)
	const link = "https://Box.ts.net:8443/pair#token=secret"
	var paired string
	h.p.pair = func(_ context.Context, l string) (Session, error) {
		paired = l
		return newFakeSession("https://box.ts.net:8443", testShell()), nil
	}
	picker := find[splitview.ShowPickerMsg](t, run(h.p.connectPicker(nil)))
	msgs := run(picker.OnText(" " + link + " "))
	assert.Equal(t, link, paired, "the whole link is handed to the pairing")
	assert.Equal(t, "https://box.ts.net:8443", h.p.liveSession().Label())
	for _, f := range flashes(msgs) {
		assert.NotContains(t, f, "secret", "the credential is shown nowhere")
	}
	assert.Contains(t, flashes(msgs), "T3: connecting to https://box.ts.net:8443…")

	var dialed string
	h.p.dial = func(_ context.Context, spec string) (Session, error) {
		dialed = spec
		return nil, errors.New("no such server")
	}
	run(picker.OnText("boxdev"))
	assert.Equal(t, "ssh://boxdev", dialed, "a bare name is still an ssh host")
}
