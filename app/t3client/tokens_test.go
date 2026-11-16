package t3client

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenStore_ConcurrentSavesAllSurvive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t3-tokens.json")
	const writers = 16
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			// a store of its own, as another aidiff instance would have
			s := TokenStore{Path: path}
			assert.NoError(t, s.Put(fmt.Sprintf("ssh://host%d", i), "tok"))
		})
	}
	wg.Wait()
	servers, err := TokenStore{Path: path}.Servers()
	require.NoError(t, err)
	assert.Len(t, servers, writers, "no save is lost to another")
}

func TestTokenStore_KeepsTokensPrivateInASharedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.Mkdir(dir, 0o755)) //nolint:gosec // a shared settings directory
	s := TokenStore{Path: filepath.Join(dir, "t3-tokens.json")}
	require.NoError(t, s.Put("local", "tok"))
	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), dirInfo.Mode().Perm(), "the shared directory keeps its mode")
	fileInfo, err := os.Stat(s.Path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm(), "the token file is private")
}
