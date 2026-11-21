package watch

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	waitFor = 2 * time.Second
	quiet   = 300 * time.Millisecond // long enough for a stray event to show up
)

type repo struct {
	root, gitDir string
	w            *Watcher
}

// newRepo builds root/{src, node_modules(ignored), .git/refs/heads} and watches it.
func newRepo(t *testing.T, checkIgnored func(context.Context, []string) []string) *repo {
	t.Helper()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	for _, d := range []string{"src/deep", "node_modules/pkg", ".git/refs/heads"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o750))
	}
	w, err := New(t.Context(), Config{
		Root:         root,
		GitDir:       gitDir,
		Ignored:      []string{filepath.Join(root, "node_modules")},
		CheckIgnored: checkIgnored,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = w.Close()
	})
	return &repo{root: root, gitDir: gitDir, w: w}
}

func (r *repo) write(t *testing.T, rel, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(r.root, rel), []byte(content), 0o600))
}

// next waits for a change notification, then for events to stop for a
// moment: one file write is several kernel events (create, write), each of
// which may make a notification pending.
func (r *repo) next(t *testing.T) {
	t.Helper()
	select {
	case <-r.w.Changes():
	case <-time.After(waitFor):
		t.Fatal("no change notification")
	}
	for {
		select {
		case <-r.w.Changes():
		case <-time.After(quiet):
			return
		}
	}
}

// assertQuiet fails if a change notification shows up.
func (r *repo) assertQuiet(t *testing.T, why string) {
	t.Helper()
	select {
	case <-r.w.Changes():
		t.Fatalf("%s: unexpected change notification", why)
	case <-time.After(quiet):
	}
}

func TestWatcher_ReportsEdits(t *testing.T) {
	r := newRepo(t, nil)
	r.write(t, "src/deep/a.go", "package a")
	r.next(t) // nested directories are watched

	r.write(t, "top.txt", "x")
	r.next(t)

	require.NoError(t, os.Remove(filepath.Join(r.root, "top.txt")))
	r.next(t) // deletes are changes too

	r.write(t, "..env", "x")
	r.next(t) // a root file whose name starts with ".." is inside the root
}

func TestWatcher_BurstIsOneNotification(t *testing.T) {
	r := newRepo(t, nil)
	for i := range 20 {
		r.write(t, "src/f.txt", string(rune('a'+i)))
	}
	r.write(t, "src/g.txt", "g")
	time.Sleep(100 * time.Millisecond) // let the burst land

	// 21 writes (dozens of kernel events) produced one pending notification
	select {
	case <-r.w.Changes():
	case <-time.After(waitFor):
		t.Fatal("no notification")
	}
	select {
	case <-r.w.Changes():
		t.Fatal("a burst must not queue more than one notification")
	default:
	}
}

func TestWatcher_SettleWaitsForTheBurstToEnd(t *testing.T) {
	root := t.TempDir()
	const settle = 500 * time.Millisecond
	w, err := New(t.Context(), Config{Root: root, Settle: settle})
	require.NoError(t, err)
	defer w.Close()

	started := time.Now()
	for i := range 6 {
		require.NoError(t, os.WriteFile(filepath.Join(root, "f.txt"), []byte{byte('a' + i)}, 0o600))
		select {
		case <-w.Changes():
			t.Fatal("reported before the burst ended")
		case <-time.After(50 * time.Millisecond):
		}
	}
	select {
	case <-w.Changes():
		assert.GreaterOrEqual(t, time.Since(started), settle, "the report trails the last change")
	case <-time.After(waitFor):
		t.Fatal("no notification after the burst")
	}
	select {
	case <-w.Changes():
		t.Fatal("a settled burst is one notification")
	case <-time.After(quiet):
	}
}

// ignoredByName is a CheckIgnored that ignores the directories named one of
// names, recording each call's directories, root-relative.
type ignoredByName struct {
	root  string
	names []string
	mu    sync.Mutex
	asked [][]string
}

func (i *ignoredByName) check(_ context.Context, dirs []string) []string { // runs on the watcher's goroutine
	i.mu.Lock()
	defer i.mu.Unlock()
	var ignored, asked []string
	for _, d := range dirs {
		rel, _ := filepath.Rel(i.root, d)
		asked = append(asked, filepath.ToSlash(rel))
		if slices.Contains(i.names, filepath.Base(d)) {
			ignored = append(ignored, d)
		}
	}
	i.asked = append(i.asked, asked)
	return ignored
}

func (i *ignoredByName) calls() [][]string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return slices.Clone(i.asked)
}

func TestWatcher_SkipsIgnoredTrees(t *testing.T) {
	ign := &ignoredByName{names: []string{"dist"}}
	r := newRepo(t, ign.check)
	ign.mu.Lock()
	ign.root = r.root
	ign.mu.Unlock()

	r.write(t, "node_modules/pkg/index.js", "x")
	r.assertQuiet(t, "ignored at startup")

	// a directory created later is checked once with the VCS
	require.NoError(t, os.Mkdir(filepath.Join(r.root, "dist"), 0o750))
	r.assertQuiet(t, "the ignored dir's own creation is not a change")
	r.write(t, "dist/bundle.js", "x")
	r.assertQuiet(t, "files inside a newly created ignored dir")
	assert.Equal(t, [][]string{{"dist"}}, ign.calls())
}

func TestWatcher_SkipsIgnoredFoldersInsideANewTree(t *testing.T) {
	ign := &ignoredByName{names: []string{"node_modules"}}
	r := newRepo(t, ign.check)
	ign.mu.Lock()
	ign.root = r.root
	ign.mu.Unlock()

	// a project moved in whole: its node_modules was never seen at startup
	outside := filepath.Join(t.TempDir(), "app")
	for _, d := range []string{"src/lib", "node_modules/a/b", "node_modules/c"} {
		require.NoError(t, os.MkdirAll(filepath.Join(outside, d), 0o750))
	}
	require.NoError(t, os.Rename(outside, filepath.Join(r.root, "app")))
	r.next(t) // the new tree is a change once watched

	var watched []string
	for _, d := range r.w.fsw.WatchList() {
		if rel, _ := filepath.Rel(r.root, d); strings.HasPrefix(filepath.ToSlash(rel), "app") {
			watched = append(watched, filepath.ToSlash(rel))
		}
	}
	assert.ElementsMatch(t, []string{"app", "app/src", "app/src/lib"}, watched, "an ignored folder inside a new tree is not watched")
	assert.Equal(t, [][]string{{"app"}, {"app/node_modules", "app/src"}, {"app/src/lib"}}, ign.calls(),
		"one question per level of the tree, and nothing below an ignored folder is asked about")

	r.write(t, "app/node_modules/c/x.js", "x")
	r.assertQuiet(t, "files in an ignored folder of a new tree")
	r.write(t, "app/src/lib/x.go", "x")
	r.next(t)
}

func TestWatcher_SlowIgnoreCheckDoesNotHoldBackChanges(t *testing.T) {
	asked, release := make(chan struct{}, 1), make(chan struct{})
	r := newRepo(t, func(context.Context, []string) []string {
		select {
		case asked <- struct{}{}:
		default:
		}
		<-release // git stuck, as in a checkout creating folders by the hundred
		return nil
	})
	defer close(release)
	require.NoError(t, os.Mkdir(filepath.Join(r.root, "newpkg"), 0o750))
	select {
	case <-asked:
	case <-time.After(waitFor):
		t.Fatal("the new folder was not checked")
	}
	r.write(t, "src/a.go", "package a")
	r.next(t) // an edit is reported while the check is still running
}

func TestNew_SetupEndsWithItsContext(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b"), 0o750))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w, err := New(ctx, Config{Root: root})
	require.ErrorIs(t, err, context.Canceled, "a setup no longer wanted stops walking")
	assert.Nil(t, w)
}

func TestWatcher_WatchesNewDirectories(t *testing.T) {
	r := newRepo(t, func(context.Context, []string) []string {
		return nil
	})
	require.NoError(t, os.Mkdir(filepath.Join(r.root, "newpkg"), 0o750))
	r.next(t)

	r.write(t, "newpkg/b.go", "package b")
	r.next(t) // the new directory is watched
}

func TestWatcher_GitStateIsAChange(t *testing.T) {
	r := newRepo(t, nil)

	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "index.lock"), []byte("x"), 0o600))
	r.assertQuiet(t, "lock files are ignored")

	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "index"), []byte("x"), 0o600))
	r.next(t) // staging (index) is a git state change

	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "refs", "heads", "main"), []byte("x"), 0o600))
	r.next(t) // a commit moves a branch ref

	require.NoError(t, os.Mkdir(filepath.Join(r.gitDir, "refs", "heads", "feature"), 0o750))
	r.next(t)
	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "refs", "heads", "feature", "x"), []byte("x"), 0o600))
	r.next(t) // refs in new branch namespaces are watched too

	require.NoError(t, os.MkdirAll(filepath.Join(r.gitDir, "objects", "ab"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "objects", "ab", "cd"), []byte("x"), 0o600))
	r.next(t) // creating objects/ at the top of .git is itself an event
	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "objects", "ab", "ef"), []byte("x"), 0o600))
	r.assertQuiet(t, "object writes are not watched")
}

func TestWatcher_IgnoresChmod(t *testing.T) {
	r := newRepo(t, nil)
	r.write(t, "src/a.txt", "x")
	r.next(t)
	require.NoError(t, os.Chmod(filepath.Join(r.root, "src", "a.txt"), 0o600))
	r.assertQuiet(t, "permission-only change")
}

func TestWatcher_CloseStops(t *testing.T) {
	r := newRepo(t, nil)
	require.NoError(t, r.w.Close())
	require.NoError(t, r.w.Close(), "close twice is fine")
	r.write(t, "src/a.txt", "x")
	r.assertQuiet(t, "after close")
}

func TestShallowWatchFollowsOpenedDirectories(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "deep"), 0o700))
	w, err := New(t.Context(), Config{Root: root, Shallow: true})
	require.NoError(t, err)
	defer w.Close()
	assert.ElementsMatch(t, []string{root}, w.fsw.WatchList())
	w.SetDirectories([]string{root, src})
	assert.ElementsMatch(t, []string{root, src}, w.fsw.WatchList())
	r := &repo{root: root, w: w}
	r.write(t, "src/file", "hello")
	r.next(t)
	w.SetDirectories([]string{root})
	assert.ElementsMatch(t, []string{root}, w.fsw.WatchList())
	r.write(t, "src/file", "changed")
	r.assertQuiet(t, "collapsed folder must no longer be watched")
}

func TestWatcher_CloseConcurrently(t *testing.T) {
	r := newRepo(t, nil)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			assert.NoError(t, r.w.Close())
		})
	}
	wg.Wait()
}

func TestWatcher_WarnsOnceAboutUnwatchedDirectories(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	root := t.TempDir()
	w, err := New(t.Context(), Config{Root: root, Shallow: true})
	require.NoError(t, err)
	defer w.Close()

	missing := filepath.Join(root, "missing")
	w.SetDirectories([]string{root, missing, filepath.Join(root, "also-missing")})
	assert.Equal(t, 1, strings.Count(buf.String(), "[WARN]"), buf.String())
	assert.Contains(t, buf.String(), missing)
}
