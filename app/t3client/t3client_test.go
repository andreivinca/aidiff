package t3client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
)

// fakeServer is a minimal T3 /ws endpoint: handler answers each request frame.
type fakeServer struct {
	t       *testing.T
	token   string
	handler func(ws *websocket.Conn, req map[string]any)

	mu    sync.Mutex
	acks  int
	query string
	ws    *websocket.Conn // the latest connection
}

// socket is the latest client connection, for frames a test sends itself.
func (f *fakeServer) socket() *websocket.Conn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ws
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.URL.Query().Get("orchestrationProtocol") != protocolVersion {
		http.Error(w, "protocol", http.StatusUpgradeRequired)
		return
	}
	f.mu.Lock()
	f.query = r.URL.RawQuery
	f.mu.Unlock()
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(maxFrameBytes)
	f.mu.Lock()
	f.ws = ws
	f.mu.Unlock()
	ctx := r.Context()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var req map[string]any
		if json.Unmarshal(data, &req) != nil {
			f.t.Errorf("client sent invalid JSON: %q", data)
			return
		}
		switch req["_tag"] {
		case "Ack":
			f.mu.Lock()
			f.acks++
			f.mu.Unlock()
		case "Request":
			f.handler(ws, req)
		}
	}
}

func (f *fakeServer) ackCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acks
}

func send(t *testing.T, ws *websocket.Conn, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		t.Errorf("marshal: %v", err)
		return
	}
	_ = ws.Write(context.Background(), websocket.MessageText, b)
}

func success(id, value any) map[string]any {
	return map[string]any{"_tag": "Exit", "requestId": id, "exit": map[string]any{"_tag": "Success", "value": value}}
}

func startFake(t *testing.T, handler func(ws *websocket.Conn, req map[string]any)) (*fakeServer, string) {
	t.Helper()
	f := &fakeServer{t: t, token: "tok", handler: handler}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func dialFake(t *testing.T, url string) *Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, directEndpoint(url), "tok")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = c.close()
	})
	return c
}

const sampleDiff = "diff --git a/f.txt b/f.txt\nindex 7898192..422c2b7 100644\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1,2 @@\n a\n+b\n" +
	"diff --git a/g.txt b/g.txt\nnew file mode 100644\nindex 0000000..3e75765\n--- /dev/null\n+++ b/g.txt\n@@ -0,0 +1 @@\n+new\n"

func TestConn_CallSuccessAndRequestShape(t *testing.T) {
	var mu sync.Mutex
	var got map[string]any
	f, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		mu.Lock()
		got = req
		mu.Unlock()
		send(t, ws, success(req["id"], map[string]any{"answer": 42}))
	})
	c := dialFake(t, url)

	var out struct{ Answer int }
	require.NoError(t, c.call(context.Background(), "some.method", map[string]string{"k": "v"}, &out))
	assert.Equal(t, 42, out.Answer)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "some.method", got["tag"])
	assert.Equal(t, map[string]any{"k": "v"}, got["payload"])
	assert.Equal(t, []any{}, got["headers"])
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, "orchestrationProtocol=2", f.query)
}

func TestConn_CallFailureAndDefect(t *testing.T) {
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		if req["tag"] == "fails" {
			send(t, ws, map[string]any{"_tag": "Exit", "requestId": req["id"], "exit": map[string]any{
				"_tag": "Failure", "cause": []any{map[string]any{"_tag": "Fail", "error": map[string]any{
					"_tag": "VcsRepositoryDetectionError", "detail": "cwd outside workspace"}}}}})
			return
		}
		send(t, ws, map[string]any{"_tag": "Exit", "requestId": req["id"], "exit": map[string]any{
			"_tag": "Failure", "cause": []any{map[string]any{"_tag": "Die", "defect": "Unknown request tag: dies"}}}})
	})
	c := dialFake(t, url)

	err := c.call(context.Background(), "fails", nil, nil)
	var rpcErr *rpcError
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, "VcsRepositoryDetectionError", rpcErr.Tag)
	assert.Equal(t, "cwd outside workspace", rpcErr.Detail)

	err = c.call(context.Background(), "dies", nil, nil)
	require.ErrorAs(t, err, &rpcErr)
	assert.Empty(t, rpcErr.Tag)
	assert.Contains(t, err.Error(), "Unknown request tag: dies")
}

func TestDial_RejectsBadTokenWithErrUnauthorized(t *testing.T) {
	_, url := startFake(t, nil)
	_, err := dial(context.Background(), directEndpoint(url), "wrong")
	require.ErrorIs(t, err, errUnauthorized)
}

func TestConn_SubscribeAcksEveryChunk(t *testing.T) {
	f, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		send(t, ws, map[string]any{"_tag": "Chunk", "requestId": req["id"], "values": []any{1, 2}})
		send(t, ws, map[string]any{"_tag": "Chunk", "requestId": req["id"], "values": []any{3}})
		send(t, ws, success(req["id"], nil))
	})
	c := dialFake(t, url)

	var vals []string
	require.NoError(t, c.subscribe(context.Background(), "stream", nil, func(v json.RawMessage) {
		vals = append(vals, string(v))
	}))
	assert.Equal(t, []string{"1", "2", "3"}, vals)
	require.Eventually(t, func() bool {
		return f.ackCount() == 2
	}, 2*time.Second, 10*time.Millisecond)
}

func TestConn_CallFailsWhenConnectionDrops(t *testing.T) {
	_, url := startFake(t, func(ws *websocket.Conn, _ map[string]any) {
		_ = ws.Close(websocket.StatusGoingAway, "restart")
	})
	c := dialFake(t, url)
	require.Error(t, c.call(context.Background(), "x", nil, nil))
	<-c.Done()
	require.Error(t, c.Err())
}

func shellFixture() Shell {
	wt := "/srv/wt/feature"
	return Shell{
		Projects: []Project{{ID: "p1", Title: "app", WorkspaceRoot: "/srv/app"}},
		Threads: []Thread{
			{ID: "aaaa-1", ProjectID: "p1", Title: "old", UpdatedAt: "2026-11-14T10:00:00.000Z"},
			{ID: "bbbb-2", ProjectID: "p1", Title: "new", WorktreePath: &wt, UpdatedAt: "2026-11-15T10:00:00.000Z"},
		},
	}
}

func TestResolve(t *testing.T) {
	sh := shellFixture()

	tg, err := Resolve(sh, "")
	require.NoError(t, err)
	assert.Equal(t, "bbbb-2", tg.Thread.ID, "most recently updated thread")
	assert.Equal(t, "/srv/wt/feature", tg.Cwd, "worktree wins over project root")

	tg, err = Resolve(sh, "aaaa")
	require.NoError(t, err)
	assert.Equal(t, "/srv/app", tg.Cwd, "thread without worktree reviews project root")
	assert.Equal(t, "app / old", tg.Label())

	_, err = Resolve(sh, "zzz")
	require.Error(t, err)

	sh.Threads = append(sh.Threads, Thread{ID: "aaaa-3", ProjectID: "p1"})
	_, err = Resolve(sh, "aaaa")
	require.ErrorContains(t, err, "ambiguous")

	tg, err = Resolve(Shell{Projects: sh.Projects}, "")
	require.NoError(t, err)
	assert.Nil(t, tg.Thread)
	assert.Equal(t, "/srv/app", tg.Cwd)
}

func TestFetchShellAndRenderer(t *testing.T) {
	var mu sync.Mutex
	var previewPayload any
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		switch req["tag"] {
		case "orchestration.subscribeShell":
			send(t, ws, map[string]any{"_tag": "Chunk", "requestId": req["id"], "values": []any{
				map[string]any{"kind": "snapshot", "snapshot": shellFixture()}}})
		case "review.getDiffPreview":
			mu.Lock()
			previewPayload = req["payload"]
			mu.Unlock()
			send(t, ws, success(req["id"], map[string]any{"cwd": "/srv/wt/feature", "sources": []any{
				map[string]any{"kind": "working-tree", "diff": "", "truncated": false},
				map[string]any{"kind": "branch-range", "diff": sampleDiff, "truncated": false},
			}}))
		}
	})
	c := dialFake(t, url)

	sh, err := c.FetchShell(context.Background())
	require.NoError(t, err)
	require.Len(t, sh.Threads, 2)

	r := NewRenderer(c, "/srv/wt/feature", SourceBranchRange)
	files, err := r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"f.txt", "g.txt"}, diff.FileEntryPaths(files))
	mu.Lock()
	assert.Equal(t, map[string]any{"cwd": "/srv/wt/feature", "sourceKind": "branch-range", "metadataOnly": true}, previewPayload)
	mu.Unlock()

	lines, err := r.FileDiff(diff.FileDiffRequest{Path: "f.txt"})
	require.NoError(t, err)
	require.Len(t, lines, 2)
	assert.Equal(t, diff.ChangeAdd, lines[1].ChangeType)
	assert.Equal(t, "b", lines[1].Content)

	// a clean working tree is an empty review, not an error
	r = NewRenderer(c, "/srv/wt/feature", SourceWorkingTree)
	files, err = r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestWatchThreadFiresOnRunTransitions(t *testing.T) {
	running := "run-1"
	done := "2026-11-15T11:00:00.000Z"
	idle := Thread{ID: "t1", ProjectID: "p1"}
	busy := idle
	busy.ActiveRunID = &running
	finished := idle
	finished.LatestRunCompletedAt = &done
	other := Thread{ID: "t2", ProjectID: "p1", ActiveRunID: &running}

	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		id := req["id"]
		chunk := func(v any) {
			send(t, ws, map[string]any{"_tag": "Chunk", "requestId": id, "values": []any{v}})
		}
		chunk(map[string]any{"kind": "snapshot", "snapshot": Shell{Threads: []Thread{idle}}})
		chunk(map[string]any{"kind": "thread.updated", "thread": other}) // another thread: ignored
		chunk(map[string]any{"kind": "thread.updated", "thread": idle})  // no change: ignored
		chunk(map[string]any{"kind": "thread.updated", "thread": busy})  // run started
		chunk(map[string]any{"kind": "thread.updated", "thread": finished})
		send(t, ws, success(id, nil))
	})
	c := dialFake(t, url)

	var seen []bool
	require.NoError(t, c.WatchThread(context.Background(), "t1", func(th Thread) {
		seen = append(seen, th.Running())
	}))
	assert.Equal(t, []bool{true, false}, seen)
}

func TestPairExchangesCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/oauth/token", r.URL.Path)
		assert.NoError(t, r.ParseForm())
		assert.Equal(t, "urn:ietf:params:oauth:grant-type:token-exchange", r.PostForm.Get("grant_type"))
		assert.Equal(t, "urn:t3:params:oauth:token-type:environment-bootstrap", r.PostForm.Get("subject_token_type"))
		if r.PostForm.Get("subject_token") != "CRED" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"bearer-1","token_type":"Bearer","expires_in":60,"scope":"x"}`))
	}))
	defer srv.Close()

	tok, err := pair(context.Background(), directEndpoint(srv.URL), "CRED")
	require.NoError(t, err)
	assert.Equal(t, "bearer-1", tok)

	_, err = pair(context.Background(), directEndpoint(srv.URL), "nope")
	require.ErrorContains(t, err, "HTTP 400")
}

func TestPairingCredentialParsesLinks(t *testing.T) {
	cred, origin := pairingCredential("https://box.tail.ts.net:8443/pair#token=ABC123")
	assert.Equal(t, "ABC123", cred)
	assert.Equal(t, "https://box.tail.ts.net:8443", origin)

	cred, origin = pairingCredential("  ABC123 \n")
	assert.Equal(t, "ABC123", cred)
	assert.Empty(t, origin)
}

func TestFirstTokenSkipsLauncherNoise(t *testing.T) {
	out := "\n  eyJ2Ijox.sig_-x\n[1115/115502.414082:ERROR:electron/shell/common/node_util.cc:78] CompileAndCall failed\n"
	tok, err := firstToken([]byte(out))
	require.NoError(t, err)
	assert.Equal(t, "eyJ2Ijox.sig_-x", tok, "the first non-empty line, the noise after it ignored")

	_, err = firstToken([]byte("[1115:ERROR] boom\n"))
	require.ErrorContains(t, err, "where a token belongs")
	_, err = firstToken([]byte("Usage: t3 auth session issue\neyJ2Ijox.sig\n"))
	require.Error(t, err, "a token is never searched for past a line that is not one")
	_, err = firstToken([]byte("\n\n"))
	require.ErrorContains(t, err, "printed no token")
}

func TestTokenStoreRoundTrip(t *testing.T) {
	s := TokenStore{Path: filepath.Join(t.TempDir(), "sub", "t3-tokens.json")}
	tok, err := s.Get("http://h:1")
	require.NoError(t, err)
	assert.Empty(t, tok)

	require.NoError(t, s.Put("HTTP://H:1/some/path", "a"))
	require.NoError(t, s.Put("ssh://boxdev", "b"))
	tok, err = s.Get("http://h:1")
	require.NoError(t, err)
	assert.Equal(t, "a", tok, "keys are normalized to the origin")

	info, err := os.Stat(s.Path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	require.NoError(t, s.remove("http://h:1"))
	tok, err = s.Get("http://h:1")
	require.NoError(t, err)
	assert.Empty(t, tok)
	tok, err = s.Get("ssh://boxdev")
	require.NoError(t, err)
	assert.Equal(t, "b", tok)
}

func TestSocketURL(t *testing.T) {
	u, err := socketURL("https://box.example:8443/")
	require.NoError(t, err)
	assert.Equal(t, "wss://box.example:8443/ws?orchestrationProtocol=2", u)

	u, err = socketURL("http://127.0.0.1:3773")
	require.NoError(t, err)
	assert.Equal(t, "ws://127.0.0.1:3773/ws?orchestrationProtocol=2", u)

	_, err = socketURL("ftp://x")
	require.Error(t, err)
}

func TestConnect_RejectsUnsafeSSHHost(t *testing.T) {
	for _, h := range []string{"ssh://-oProxyCommand=x", "ssh://a;b", "ssh://"} {
		_, err := Connect(context.Background(), h, ConnectOptions{})
		require.ErrorContains(t, err, "invalid ssh host", h)
	}
}

// TestRendererListsEveryFileAndFetchesPatchesOnDemand covers a server that
// lists the changed files: the tree holds all of them, and each patch is
// fetched once, when the file is opened.
func TestRendererListsEveryFileAndFetchesPatchesOnDemand(t *testing.T) {
	var got capture
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		p, _ := req["payload"].(map[string]any)
		if file, ok := p["file"].(map[string]any); ok {
			diffText := ""
			if file["path"] == "f.txt" {
				cut := strings.Index(sampleDiff, "diff --git a/g.txt")
				require.NotEqual(t, -1, cut)
				diffText = sampleDiff[:cut]
			}
			send(t, ws, success(req["id"], map[string]any{"sources": []any{
				map[string]any{"kind": "branch-range", "diff": diffText, "truncated": false},
			}}))
			return
		}
		assert.Equal(t, true, p["metadataOnly"])
		send(t, ws, success(req["id"], map[string]any{"sources": []any{
			map[string]any{"kind": "branch-range", "diff": "", "truncated": true, "files": []any{
				map[string]any{"path": "f.txt", "previousPath": nil, "changeType": "change", "additions": 1, "deletions": 0},
				map[string]any{"path": "g.txt", "previousPath": nil, "changeType": "new", "additions": 1, "deletions": 0},
				map[string]any{"path": "frontend/src/app/page.tsx", "previousPath": "frontend/src/old.tsx", "changeType": "rename-changed", "additions": 9, "deletions": 2},
			}},
		}}))
	})
	c := dialFake(t, url)
	r := NewRenderer(c, "/srv/wt/feature", SourceBranchRange)

	files, err := r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"f.txt", "g.txt", "frontend/src/app/page.tsx"}, diff.FileEntryPaths(files), "every listed file, not just the capped patch's")
	assert.Equal(t, diff.FileAdded, files[1].Status)
	assert.Equal(t, diff.FileRenamed, files[2].Status)
	assert.Equal(t, "frontend/src/old.tsx", files[2].OldPath)
	assert.False(t, r.cutShort(), "a listing is never cut short")
	assert.Equal(t, 1, got.count())

	lines, err := r.FileDiff(diff.FileDiffRequest{Path: "f.txt"})
	require.NoError(t, err)
	require.Len(t, lines, 2)
	assert.Equal(t, "b", lines[1].Content)
	p := payloadOf(t, got.last())
	file, _ := p["file"].(map[string]any)
	assert.Equal(t, "f.txt", file["path"])
	assert.Equal(t, "branch-range", file["sourceKind"])
	assert.Nil(t, p["metadataOnly"])

	_, err = r.FileDiff(diff.FileDiffRequest{Path: "f.txt"})
	require.NoError(t, err)
	assert.Equal(t, 2, got.count(), "a patch is fetched once")
	lines, err = r.FileDiff(diff.FileDiffRequest{Path: "g.txt"})
	require.NoError(t, err)
	assert.Nil(t, lines, "a file whose patch comes back empty shows nothing")
	lines, err = r.FileDiff(diff.FileDiffRequest{Path: "unknown.txt"})
	require.NoError(t, err)
	assert.Nil(t, lines)
	assert.Equal(t, 3, got.count(), "nothing is asked for a file that is not listed")
}
