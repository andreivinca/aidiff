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
	ep := endpoint{base: "http://t3.example:3773", client: client}
	srv := server{kind: serverHTTP, label: ep.base, base: ep.base}
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

// startProxiedFake serves a fake T3 server under /t3, as a reverse proxy
// would, pairing CRED for its token.
func startProxiedFake(t *testing.T) string {
	t.Helper()
	f := &fakeServer{t: t, token: "tok", handler: func(ws *websocket.Conn, req map[string]any) {
		send(t, ws, success(req["id"], nil))
	}}
	mux := http.NewServeMux()
	mux.Handle("/t3/ws", f)
	mux.HandleFunc("/t3/oauth/token", func(w http.ResponseWriter, r *http.Request) {
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

func TestConnect_ReachesAServerUnderAPath(t *testing.T) {
	url := startProxiedFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := TokenStore{Path: filepath.Join(t.TempDir(), "t3-tokens.json")}
	s, err := Connect(ctx, url+"/t3/", ConnectOptions{Store: store, Pair: "CRED"})
	require.NoError(t, err, "pairing and the socket are both below the path")
	defer s.Close()
	assert.Equal(t, url+"/t3", s.Label(), "the path is part of the server's name, to connect to it again")
	require.NoError(t, s.Conn().call(ctx, "x", nil, nil))

	tok, err := store.Get(url + "/t3")
	require.NoError(t, err)
	assert.Equal(t, "tok", tok)
	tok, err = store.Get(url)
	require.NoError(t, err)
	assert.Empty(t, tok, "another server under the same origin has a token of its own")
}

// startRotatingFake is startPairingFake whose server can stop accepting its token.
func startRotatingFake(t *testing.T) (*fakeServer, string) {
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
	return f, srv.URL
}

func TestConnect_PairedSessionRedialsPastAnExpiredToken(t *testing.T) {
	for name, store := range map[string]func(t *testing.T) TokenStore{
		"saved": func(t *testing.T) TokenStore { return TokenStore{Path: filepath.Join(t.TempDir(), "t3-tokens.json")} },
		"kept":  unwritableStore,
	} {
		t.Run(name, func(t *testing.T) {
			f, url := startRotatingFake(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var issued atomic.Int32
			issue := func(context.Context) (string, error) {
				issued.Add(1)
				return "tok2", nil
			}
			// connectHTTP with an issuer stands in for a local or ssh:// server,
			// which Redial would reach with its own
			srv, err := parseServer(url)
			require.NoError(t, err)
			ep := directEndpoint(srv.base)
			s, err := connectHTTP(ctx, srv, ep, ConnectOptions{Store: store(t), Pair: "CRED"}, issue)
			require.NoError(t, err)
			defer s.Close()
			assert.Empty(t, s.opts.Pair, "the credential is spent")
			assert.Empty(t, s.opts.Token, "the paired token is not held as an explicit one")

			f.rotate("tok2") // the paired token expires
			again, err := connectHTTP(ctx, s.server, ep, s.opts, issue)
			require.NoError(t, err, "an expired paired token is replaced, not retried forever")
			defer again.Close()
			assert.EqualValues(t, 1, issued.Load())

			third, err := connectHTTP(ctx, again.server, ep, again.opts, issue)
			require.NoError(t, err)
			defer third.Close()
			assert.EqualValues(t, 1, issued.Load(), "the replacement is reused")
		})
	}
}

func TestServerLabel(t *testing.T) {
	for spec, want := range map[string]string{
		"local":                     "local",
		"":                          "local",
		"ssh://devbox":              "ssh://devbox",
		"ssh://devbox/":             "ssh://devbox",
		"https://t3.example.com/x/": "https://t3.example.com/x",
	} {
		got, err := ServerLabel(spec)
		require.NoError(t, err, spec)
		assert.Equal(t, want, got, "the label a session to %q carries", spec)
	}
	for _, spec := range []string{"ssh://-oProxyCommand=x", "ftp://host", "devbox"} {
		_, err := ServerLabel(spec)
		assert.Error(t, err, "%q names no server", spec)
	}
}
