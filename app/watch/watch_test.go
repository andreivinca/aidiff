package watch

import (
	"os"
	"path/filepath"
	"slices"
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
func newRepo(t *testing.T, isIgnored func(string) bool) *repo {
	t.Helper()
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	for _, d := range []string{"src/deep", "node_modules/pkg", ".git/refs/heads"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o750))
	}
	w, err := New(Config{
		Root:      root,
		GitDir:    gitDir,
		Ignored:   []string{filepath.Join(root, "node_modules")},
		IsIgnored: isIgnored,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	return &repo{root: root, gitDir: gitDir, w: w}
}

func (r *repo) write(t *testing.T, rel, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(r.root, rel), []byte(content), 0o600))
}

// next waits for changes and collects everything until events stop for a
// moment: one file write is several kernel events (create, write), and a
// notification may arrive after its paths were already drained (an empty batch).
func (r *repo) next(t *testing.T) ([]string, bool) {
	t.Helper()
	set := map[string]struct{}{}
	meta := false
	deadline := time.After(waitFor)
	for {
		select {
		case <-r.w.Changes():
			paths, m := r.w.Drain()
			for _, p := range paths {
				set[p] = struct{}{}
			}
			meta = meta || m
		case <-time.After(quiet):
			if len(set) > 0 || meta {
				out := make([]string, 0, len(set))
				for p := range set {
					out = append(out, p)
				}
				slices.Sort(out)
				return out, meta
			}
		case <-deadline:
			t.Fatal("no change notification")
		}
	}
}

// assertQuiet fails if any real change (not an empty batch) shows up.
func (r *repo) assertQuiet(t *testing.T, why string) {
	t.Helper()
	timeout := time.After(quiet)
	for {
		select {
		case <-r.w.Changes():
			if paths, meta := r.w.Drain(); len(paths) > 0 || meta {
				t.Fatalf("%s: unexpected change %v meta=%v", why, paths, meta)
			}
		case <-timeout:
			return
		}
	}
}

func TestWatcher_ReportsEditsWithPaths(t *testing.T) {
	r := newRepo(t, nil)
	r.write(t, "src/deep/a.go", "package a")
	paths, meta := r.next(t)
	assert.Contains(t, paths, "src/deep/a.go", "nested directories are watched")
	assert.False(t, meta)

	r.write(t, "top.txt", "x")
	paths, _ = r.next(t)
	assert.Contains(t, paths, "top.txt")

	require.NoError(t, os.Remove(filepath.Join(r.root, "top.txt")))
	paths, _ = r.next(t)
	assert.Contains(t, paths, "top.txt", "deletes are changes too")
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
	paths, _ := r.w.Drain()
	slices.Sort(paths)
	assert.Equal(t, []string{"src/f.txt", "src/g.txt"}, paths, "each path once")
}

func TestWatcher_SkipsIgnoredTrees(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	r := newRepo(t, func(dir string) bool { // runs on the watcher goroutine
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, filepath.Base(dir))
		return filepath.Base(dir) == "dist"
	})

	r.write(t, "node_modules/pkg/index.js", "x")
	r.assertQuiet(t, "ignored at startup")

	// a directory created later is checked once with the VCS
	require.NoError(t, os.Mkdir(filepath.Join(r.root, "dist"), 0o750))
	r.assertQuiet(t, "the ignored dir's own creation is not a change")
	r.write(t, "dist/bundle.js", "x")
	r.assertQuiet(t, "files inside a newly created ignored dir")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"dist"}, asked)
}

func TestWatcher_WatchesNewDirectories(t *testing.T) {
	r := newRepo(t, func(string) bool { return false })
	require.NoError(t, os.Mkdir(filepath.Join(r.root, "newpkg"), 0o750))
	paths, _ := r.next(t)
	assert.Contains(t, paths, "newpkg")

	r.write(t, "newpkg/b.go", "package b")
	paths, _ = r.next(t)
	assert.Contains(t, paths, "newpkg/b.go", "the new directory is watched")
}

func TestWatcher_GitStateIsMeta(t *testing.T) {
	r := newRepo(t, nil)

	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "index.lock"), []byte("x"), 0o600))
	r.assertQuiet(t, "lock files are ignored")

	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "index"), []byte("x"), 0o600))
	paths, meta := r.next(t)
	assert.True(t, meta, "staging (index) is a git state change")
	assert.Empty(t, paths, ".git paths are not reported as worktree files")

	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "refs", "heads", "main"), []byte("x"), 0o600))
	_, meta = r.next(t)
	assert.True(t, meta, "a commit moves a branch ref")

	require.NoError(t, os.Mkdir(filepath.Join(r.gitDir, "refs", "heads", "feature"), 0o750))
	_, _ = r.next(t)
	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "refs", "heads", "feature", "x"), []byte("x"), 0o600))
	_, meta = r.next(t)
	assert.True(t, meta, "refs in new branch namespaces are watched too")

	require.NoError(t, os.MkdirAll(filepath.Join(r.gitDir, "objects", "ab"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "objects", "ab", "cd"), []byte("x"), 0o600))
	_, _ = r.next(t) // creating objects/ at the top of .git is itself an event
	require.NoError(t, os.WriteFile(filepath.Join(r.gitDir, "objects", "ab", "ef"), []byte("x"), 0o600))
	r.assertQuiet(t, "object writes are not watched")
}

func TestWatcher_IgnoresChmod(t *testing.T) {
	r := newRepo(t, nil)
	r.write(t, "src/a.txt", "x")
	_, _ = r.next(t)
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
