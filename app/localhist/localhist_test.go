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
