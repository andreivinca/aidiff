package t3client

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startPairingFake serves a fake T3 /ws and a pairing endpoint that trades
// the credential CRED for the fake's token.
func startPairingFake(t *testing.T) string {
	t.Helper()
	f := &fakeServer{t: t, token: "tok", handler: func(ws *websocket.Conn, req map[string]any) {
		send(t, ws, success(req["id"], nil))
	}}
	mux := http.NewServeMux()
	mux.Handle("/ws", f)
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("subject_token") != "CRED" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// unwritableStore is a token store whose file cannot be created.
func unwritableStore(t *testing.T) TokenStore {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	return TokenStore{Path: filepath.Join(blocker, "t3-tokens.json")}
}

func TestConnect_KeepsAPairedTokenItCannotSave(t *testing.T) {
	url := startPairingFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := Connect(ctx, url, ConnectOptions{Store: unwritableStore(t), Pair: "CRED"})
	require.NoError(t, err, "the credential is spent; the token it bought is used anyway")
	defer s.Close()
	require.Len(t, s.Notices(), 1)
	assert.Contains(t, s.Notices()[0], "was not saved")

	again, err := s.Redial(ctx)
	require.NoError(t, err, "a redial uses the token pairing produced, not the spent credential")
	defer again.Close()
	assert.Equal(t, s.Label(), again.Label())
	require.NoError(t, again.Conn().call(ctx, "x", nil, nil))
}

func TestConnect_PairsAndSavesTheToken(t *testing.T) {
	url := startPairingFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := TokenStore{Path: filepath.Join(t.TempDir(), "t3-tokens.json")}
	s, err := Connect(ctx, url+"/", ConnectOptions{Store: store, Pair: url + "/pair#token=CRED"})
	require.NoError(t, err)
	defer s.Close()
	assert.Empty(t, s.Notices(), "a loopback server needs no warning")
	tok, err := store.Get(url)
	require.NoError(t, err)
	assert.Equal(t, "tok", tok)
}

func TestConnect_ExplicitTokenRedialsWithIt(t *testing.T) {
	url := startPairingFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := Connect(ctx, url, ConnectOptions{Store: unwritableStore(t), Token: "tok"})
	require.NoError(t, err)
	defer s.Close()
	again, err := s.Redial(ctx)
	require.NoError(t, err)
	defer again.Close()

	_, err = Connect(ctx, url, ConnectOptions{Store: unwritableStore(t), Token: "wrong"})
	require.ErrorIs(t, err, errUnauthorized)
}

func TestConnectHTTP_WarnsAboutPlainHTTPToAnotherHost(t *testing.T) {
	url := startPairingFake(t)
	addr := strings.TrimPrefix(url, "http://")
	// the request names a remote host; the client reaches the fake instead
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}}
	ep := endpoint{origin: "http://t3.example:3773", client: client}
	srv := server{kind: serverHTTP, label: ep.origin, origin: ep.origin}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := connectHTTP(ctx, srv, ep, ConnectOptions{Token: "tok"}, nil)
	require.NoError(t, err)
	defer s.Close()
	require.Len(t, s.Notices(), 1)
	assert.Contains(t, s.Notices()[0], "travels unencrypted to http://t3.example:3773")
}

func TestPair_RefusesRedirectsAndTrimsErrors(t *testing.T) {
	var redirected atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/elsewhere", func(http.ResponseWriter, *http.Request) {
		redirected.Store(true)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	_, err := pair(context.Background(), directEndpoint(srv.URL), "CRED")
	require.ErrorContains(t, err, "HTTP 307")
	assert.False(t, redirected.Load(), "the credential never follows a redirect")

	long := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, strings.Repeat("x", 5000), http.StatusBadGateway)
	}))
	defer long.Close()
	_, err = pair(context.Background(), directEndpoint(long.URL), "CRED")
	require.ErrorContains(t, err, "HTTP 502")
	assert.Less(t, len(err.Error()), 400, "the body is quoted only in part")
}
