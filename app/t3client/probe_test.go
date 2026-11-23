package t3client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_HTTP(t *testing.T) {
	unauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/auth/session", r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"), "a probe sends no token")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer unauth.Close()
	require.NoError(t, Probe(context.Background(), unauth.URL), "a server refusing a stranger still answers")

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()
	require.Error(t, Probe(context.Background(), broken.URL), "a proxy with nothing behind it is not a server")

	gone := httptest.NewServer(http.NotFoundHandler())
	url := gone.URL
	gone.Close()
	require.Error(t, Probe(context.Background(), url))

	require.Error(t, Probe(context.Background(), "ftp://box"))
}

func TestPairingLink(t *testing.T) {
	spec, ok := PairingLink(" https://Box.tail187a74.ts.net:8443/pair#token=abc ")
	require.True(t, ok)
	assert.Equal(t, "https://box.tail187a74.ts.net:8443", spec)

	spec, ok = PairingLink("http://192.168.1.5:3773/pair?token=abc")
	require.True(t, ok)
	assert.Equal(t, "http://192.168.1.5:3773", spec)

	for _, s := range []string{"abc", "https://box:8443", "https://box:8443/pair#token=", "ssh://box#token=abc", "boxdev"} {
		_, ok := PairingLink(s)
		assert.False(t, ok, s)
	}
}
