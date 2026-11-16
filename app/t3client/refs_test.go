package t3client

import (
	"context"
	"sync"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
)

func TestListRefs(t *testing.T) {
	var mu sync.Mutex
	var payload any
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		if req["tag"] != "vcs.listRefs" {
			return
		}
		mu.Lock()
		payload = req["payload"]
		mu.Unlock()
		send(t, ws, success(req["id"], map[string]any{"isRepo": true, "totalCount": 2, "nextCursor": nil, "refs": []any{
			map[string]any{"name": "feature", "isRemote": false, "upstreamRef": "refs/remotes/origin/feature", "current": true, "isDefault": false},
			map[string]any{"name": "origin/main", "isRemote": true, "remoteName": "origin", "current": false, "isDefault": false},
		}}))
	})
	refs, err := dialFake(t, url).ListRefs(context.Background(), "/srv/wt")
	require.NoError(t, err)
	assert.Equal(t, []Ref{
		{Name: "feature", UpstreamRef: "refs/remotes/origin/feature", Current: true},
		{Name: "origin/main", IsRemote: true},
	}, refs)
	assert.Equal(t, "origin/feature", refs[0].Upstream(), "the upstream as a diff base names it")
	assert.Empty(t, refs[1].Upstream())
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, map[string]any{"cwd": "/srv/wt", "refKind": "all", "limit": float64(refsLimit)}, payload)
}

func TestListRefsOutsideARepository(t *testing.T) {
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		if req["tag"] == "vcs.listRefs" {
			send(t, ws, success(req["id"], map[string]any{"isRepo": false, "refs": []any{}, "totalCount": 0, "nextCursor": nil}))
		}
	})
	_, err := dialFake(t, url).ListRefs(context.Background(), "/srv/notes")
	require.ErrorIs(t, err, ErrNotRepository)
}

func TestRendererComparesWithItsBase(t *testing.T) {
	var mu sync.Mutex
	var payloads []map[string]any
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		if req["tag"] != "review.getDiffPreview" {
			return
		}
		mu.Lock()
		payloads = append(payloads, req["payload"].(map[string]any))
		mu.Unlock()
		send(t, ws, success(req["id"], map[string]any{"cwd": "/srv/wt", "sources": []any{
			map[string]any{"kind": "branch-range", "diff": sampleDiff, "truncated": false,
				"files": []any{map[string]any{"path": "f.txt", "changeType": "change"}}},
		}}))
	})
	r := NewSourceRenderer(dialFake(t, url), "/srv/wt", DiffSource{Kind: SourceBranchRange, BaseRef: "v1.0"})
	_, err := r.ChangedFiles("", false)
	require.NoError(t, err)
	_, err = r.FileDiff(diff.FileDiffRequest{Path: "f.txt"})
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, payloads, 2)
	for _, p := range payloads {
		assert.Equal(t, "v1.0", p["baseRef"], "the listing and every file's patch compare with the base")
	}
}
