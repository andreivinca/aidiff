package localhist

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_AddMovesToTopAndCaps(t *testing.T) {
	clock := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	s := Store{Path: filepath.Join(t.TempDir(), "history.json"), now: func() time.Time {
		clock = clock.Add(time.Minute)
		return clock
	}}

	list, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, list, "a missing file is an empty history")

	require.NoError(t, s.Add("/a"))
	require.NoError(t, s.Add("/b/"))
	require.NoError(t, s.Add("/a"))
	list, err = s.List()
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "/a", list[0].Dir, "opened again: on top")
	assert.Equal(t, "/b", list[1].Dir, "cleaned")
	assert.True(t, list[0].Opened.After(list[1].Opened))

	for i := range maxEntries + 5 {
		require.NoError(t, s.Add(filepath.Join("/d", string(rune('a'+i%26)), time.Duration(i).String())))
	}
	list, err = s.List()
	require.NoError(t, err)
	assert.Len(t, list, maxEntries)
}

func TestConnections_AddListRemove(t *testing.T) {
	c := Connections{Path: filepath.Join(t.TempDir(), "connections.json")}
	list, err := c.List()
	require.NoError(t, err)
	assert.Empty(t, list, "no file: no servers")

	require.NoError(t, c.Add("ssh://a", "boxa"))
	require.NoError(t, c.Add("local", "pc"))
	require.NoError(t, c.Add("ssh://a", "boxa2"))
	list, err = c.List()
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "ssh://a", list[0].Spec, "the newest connection on top, once")
	assert.Equal(t, "boxa2", list[0].Machine)
	assert.Equal(t, "local", list[1].Spec)

	require.NoError(t, c.Remove("ssh://a"))
	list, err = c.List()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "local", list[0].Spec)
}

func TestFiles_OnDiskFormatIsKept(t *testing.T) {
	dir := t.TempDir()
	at := func() time.Time { return time.Date(2026, 11, 18, 10, 0, 0, 0, time.FixedZone("x", 3600)) }
	s := Store{Path: filepath.Join(dir, "history.json"), now: at}
	require.NoError(t, s.Add("/a"))
	b, err := os.ReadFile(s.Path)
	require.NoError(t, err)
	assert.Equal(t, "{\n  \"folders\": [\n    {\n      \"dir\": \"/a\",\n      \"opened\": \"2026-11-18T09:00:00Z\"\n    }\n  ]\n}\n", string(b))

	c := Connections{Path: filepath.Join(dir, "connections.json"), now: at}
	require.NoError(t, c.Add("ssh://a", "boxa"))
	require.NoError(t, c.Add("local", ""))
	b, err = os.ReadFile(c.Path)
	require.NoError(t, err)
	assert.Equal(t, "{\n  \"servers\": [\n    {\n      \"spec\": \"local\",\n      \"connected\": \"2026-11-18T09:00:00Z\"\n    },\n"+
		"    {\n      \"spec\": \"ssh://a\",\n      \"machine\": \"boxa\",\n      \"connected\": \"2026-11-18T09:00:00Z\"\n    }\n  ]\n}\n", string(b))
}

func TestFiles_EmptyPathOrKeyIsRefused(t *testing.T) {
	assert.Error(t, Store{}.Add("/a"), "no file to write")
	assert.Error(t, Connections{}.Add("local", ""))
	assert.Error(t, Store{Path: filepath.Join(t.TempDir(), "h.json")}.Add(""), "no folder: not the current one")
	assert.Error(t, Connections{Path: filepath.Join(t.TempDir(), "c.json")}.Add("", "box"))
	assert.Equal(t, "ssh://a", func() string {
		c := Connections{Path: filepath.Join(t.TempDir(), "c.json")}
		require.NoError(t, c.Add("ssh://a", ""))
		list, err := c.List()
		require.NoError(t, err)
		return list[0].Spec
	}(), "a spec is not cleaned as a path")
}

func TestFiles_CorruptFileIsMovedAsideOnWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		add  func(path string) error
		list func(path string) (int, error)
	}{
		{"history", func(p string) error { return Store{Path: p}.Add("/a") },
			func(p string) (int, error) { l, err := Store{Path: p}.List(); return len(l), err }},
		{"connections", func(p string) error { return Connections{Path: p}.Add("ssh://a", "") },
			func(p string) (int, error) { l, err := Connections{Path: p}.List(); return len(l), err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name+".json")
			require.NoError(t, os.WriteFile(path, []byte(`{"folders": [ {"dir": `), 0o600))

			_, err := tc.list(path)
			require.Error(t, err, "reading reports the damage")
			_, statErr := os.Stat(path + ".corrupt")
			assert.ErrorIs(t, statErr, fs.ErrNotExist, "and changes nothing")

			require.NoError(t, tc.add(path), "a change starts afresh")
			n, err := tc.list(path)
			require.NoError(t, err)
			assert.Equal(t, 1, n)
			bad, err := os.ReadFile(path + ".corrupt")
			require.NoError(t, err)
			assert.Equal(t, `{"folders": [ {"dir": `, string(bad), "the damaged file is kept aside")

			require.NoError(t, os.WriteFile(path, []byte(`[1, 2]`), 0o600))
			require.NoError(t, tc.add(path), "a file of the wrong shape too")
		})
	}
}
