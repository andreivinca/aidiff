package localhist

import (
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
