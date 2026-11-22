package localhist

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTabs_RoundTrip(t *testing.T) {
	tabs := Tabs{Path: filepath.Join(t.TempDir(), "tabs.json")}
	list, cur, err := tabs.Load()
	require.NoError(t, err)
	assert.Empty(t, list, "no file: no tabs")
	assert.Equal(t, -1, cur)

	want := []Tab{
		{Kind: TabLocal, Dir: "/home/u/aidiff"},
		{Kind: TabT3, Server: "ssh://devbox", Machine: "devbox", Project: "p1", Title: "Sabra", Thread: "th-1"},
		{Kind: TabT3, Server: "local", Machine: "andrei-pc", Project: "p2", Title: "Root only"},
	}
	require.NoError(t, tabs.Save(want, 1))
	list, cur, err = tabs.Load()
	require.NoError(t, err)
	assert.Equal(t, want, list, "in order, as saved")
	assert.Equal(t, 1, cur, "the tab on screen")

	b, err := os.ReadFile(tabs.Path)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"version": 1`)
	assert.Contains(t, string(b), `"kind": "t3"`)
	assert.Contains(t, string(b), `"server": "ssh://devbox"`)
	assert.NotContains(t, string(b), `"thread": ""`, "empty fields are left out")

	require.NoError(t, tabs.Save(nil, 0))
	b, err = os.ReadFile(tabs.Path)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"tabs": []`, "no tabs is an empty list")
	assert.Contains(t, string(b), `"current": -1`, "and none on screen")
}

func TestTabs_FileAndDirectoryArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "revdiff")
	tabs := Tabs{Path: filepath.Join(dir, "tabs.json")}
	require.NoError(t, tabs.Save([]Tab{{Kind: TabLocal, Dir: "/a"}}, 0))
	fi, err := os.Stat(tabs.Path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the file is the user's alone")
	di, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), di.Mode().Perm(), "the directory it creates too")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp-", "the atomic write leaves no temp file behind")
	}
}

func TestTabs_CorruptOrOtherVersionIsIgnoredAndReplaced(t *testing.T) {
	for name, content := range map[string]string{
		"corrupt":   "{not json",
		"version 0": `{"tabs": [{"kind": "local", "dir": "/a"}]}`,
		"version 2": `{"version": 2, "current": 0, "tabs": [{"kind": "local", "dir": "/a"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			tabs := Tabs{Path: filepath.Join(t.TempDir(), "tabs.json")}
			require.NoError(t, os.WriteFile(tabs.Path, []byte(content), 0o600))
			list, cur, err := tabs.Load()
			require.Error(t, err, "said, for the caller to log")
			assert.Empty(t, list, "no tabs from it")
			assert.Equal(t, -1, cur)
			b, rerr := os.ReadFile(tabs.Path)
			require.NoError(t, rerr)
			assert.Equal(t, content, string(b), "reading changes nothing")

			require.NoError(t, tabs.Save([]Tab{{Kind: TabLocal, Dir: "/b"}}, 0), "the next save replaces it")
			list, cur, err = tabs.Load()
			require.NoError(t, err)
			assert.Equal(t, []Tab{{Kind: TabLocal, Dir: "/b"}}, list)
			assert.Equal(t, 0, cur)
		})
	}
}

func TestTabs_InvalidEntriesAreLeftOut(t *testing.T) {
	tabs := Tabs{Path: filepath.Join(t.TempDir(), "tabs.json")}
	content := `{"version": 1, "current": 4, "tabs": [
		{"kind": "local", "dir": "/a"},
		{"kind": "local", "dir": "relative"},
		{"kind": "t3", "server": "ssh://x"},
		{"kind": "future", "dir": "/b"},
		{"kind": "t3", "server": "ssh://x", "project": "p"}
	]}`
	require.NoError(t, os.WriteFile(tabs.Path, []byte(content), 0o600))
	list, cur, err := tabs.Load()
	require.NoError(t, err)
	assert.Equal(t, []Tab{{Kind: TabLocal, Dir: "/a"}, {Kind: TabT3, Server: "ssh://x", Project: "p"}}, list,
		"a relative folder, a project with no id and an unknown kind name nothing to reopen")
	assert.Equal(t, 1, cur, "the tab on screen follows the ones left out")
}

func TestTabs_Capped(t *testing.T) {
	tabs := Tabs{Path: filepath.Join(t.TempDir(), "tabs.json")}
	many := make([]Tab, MaxTabs+10)
	for i := range many {
		many[i] = Tab{Kind: TabLocal, Dir: "/d/" + strconv.Itoa(i)}
	}
	require.NoError(t, tabs.Save(many, MaxTabs+5))
	list, cur, err := tabs.Load()
	require.NoError(t, err)
	assert.Equal(t, many[:MaxTabs], list, "the first MaxTabs are saved")
	assert.Equal(t, -1, cur, "the tab on screen was past them")

	// a file written by hand past the cap is read up to it
	require.NoError(t, os.WriteFile(tabs.Path, []byte(`{"version": 1, "current": 2, "tabs": [`+
		repeatTab(MaxTabs+3)+`]}`), 0o600))
	list, cur, err = tabs.Load()
	require.NoError(t, err)
	assert.Len(t, list, MaxTabs)
	assert.Equal(t, 2, cur)
}

// repeatTab is n local tabs, as JSON list items.
func repeatTab(n int) string {
	s := ""
	for i := range n {
		if i > 0 {
			s += ","
		}
		s += `{"kind": "local", "dir": "/r/` + strconv.Itoa(i) + `"}`
	}
	return s
}

func TestTabs_SaveGivesUpOnAHeldLock(t *testing.T) {
	old := tabsLockWait
	tabsLockWait = 50 * time.Millisecond
	t.Cleanup(func() { tabsLockWait = old })

	tabs := Tabs{Path: filepath.Join(t.TempDir(), "tabs.json")}
	lf, err := os.OpenFile(tabs.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	require.NoError(t, syscall.Flock(int(lf.Fd()), syscall.LOCK_EX)) // another aidiff, hung
	start := time.Now()
	err = tabs.Save([]Tab{{Kind: TabLocal, Dir: "/a"}}, 0)
	require.Error(t, err, "the exit does not wait on it")
	assert.Less(t, time.Since(start), 2*time.Second)
	_, statErr := os.Stat(tabs.Path)
	assert.True(t, os.IsNotExist(statErr), "and writes nothing")

	require.NoError(t, lf.Close()) // released
	require.NoError(t, tabs.Save([]Tab{{Kind: TabLocal, Dir: "/a"}}, 0))
	assert.FileExists(t, tabs.Path)
}

func TestTabs_LastWriterWins(t *testing.T) {
	tabs := Tabs{Path: filepath.Join(t.TempDir(), "tabs.json")}
	require.NoError(t, tabs.Save([]Tab{{Kind: TabLocal, Dir: "/one"}, {Kind: TabLocal, Dir: "/two"}}, 1))
	require.NoError(t, tabs.Save([]Tab{{Kind: TabLocal, Dir: "/three"}}, 0))
	list, cur, err := tabs.Load()
	require.NoError(t, err)
	assert.Equal(t, []Tab{{Kind: TabLocal, Dir: "/three"}}, list, "the file is replaced whole, never merged")
	assert.Equal(t, 0, cur)
}
