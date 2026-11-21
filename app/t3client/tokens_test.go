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

func TestTokenStore_KeysSSHLoginsByUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t3-tokens.json")
	// a file saved before logins were told apart: a userless ssh:// key
	require.NoError(t, os.WriteFile(path, []byte(`{"servers":{"ssh://box":{"token":"old"}}}`), 0o600))
	s := TokenStore{Path: path}

	require.NoError(t, s.Put("ssh://alice@box", "alice-tok"))
	require.NoError(t, s.Put("ssh://bob@box", "bob-tok"))
	for label, want := range map[string]string{
		"ssh://alice@box": "alice-tok",
		"ssh://bob@box":   "bob-tok",
		"ssh://box":       "old", // a userless label keeps the token it was saved with
		"ssh://Box/":      "old",
		"ssh://carol@box": "", // and never answers for a login with a user
	} {
		tok, err := s.Get(label)
		require.NoError(t, err)
		assert.Equal(t, want, tok, label)
	}
	servers, err := s.Servers()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"ssh://alice@box", "ssh://bob@box", "ssh://box"}, servers, "each a spec to connect to")
}

func TestTokenKey(t *testing.T) {
	for label, want := range map[string]string{
		"local":                         "local",
		"ssh://box":                     "ssh://box",
		"ssh://Alice@Box.LAN:2222/":     "ssh://Alice@box.lan:2222",
		"HTTP://H:1/":                   "http://h:1",
		"https://Example.com/T3/":       "https://example.com/T3",
		"https://example.com/t3?x=1#y":  "https://example.com/t3",
		"https://u:p@example.com:8443/": "https://example.com:8443",
	} {
		assert.Equal(t, want, tokenKey(label), label)
	}
}
