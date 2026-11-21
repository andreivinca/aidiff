package t3client

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
)

// listing is a metadata-only preview listing f.txt.
func listing(id any) map[string]any {
	return success(id, map[string]any{"sources": []any{
		map[string]any{"kind": "branch-range", "diff": "", "truncated": false, "files": []any{
			map[string]any{"path": "f.txt", "previousPath": nil, "changeType": "change"},
		}},
	}})
}

// patchFor is a one-file preview whose patch adds line.
func patchFor(id any, line string) map[string]any {
	patch := "diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1,2 @@\n a\n+" + line + "\n"
	return success(id, map[string]any{"sources": []any{
		map[string]any{"kind": "branch-range", "diff": patch, "truncated": false},
	}})
}

func isFileRequest(req map[string]any) bool {
	p, _ := req["payload"].(map[string]any)
	_, ok := p["file"]
	return ok
}

func TestRendererDropsAPatchFetchedForAnOlderListing(t *testing.T) {
	var got capture
	var fileRequests atomic.Int32
	release := make(chan struct{})
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		if !isFileRequest(req) {
			send(t, ws, listing(req["id"]))
			return
		}
		if fileRequests.Add(1) > 1 {
			send(t, ws, patchFor(req["id"], "fresh"))
			return
		}
		go func() {
			<-release // answered after the reload
			send(t, ws, patchFor(req["id"], "stale"))
		}()
	})
	r := NewRenderer(dialFake(t, url), "/srv/wt", SourceBranchRange)
	_, err := r.ChangedFiles("", false)
	require.NoError(t, err)

	staleDone := make(chan []diff.DiffLine, 1)
	go func() {
		lines, ferr := r.FileDiff(diff.FileDiffRequest{Path: "f.txt", ContextLines: patchContext})
		assert.NoError(t, ferr)
		staleDone <- lines
	}()
	require.Eventually(t, func() bool {
		return got.count() == 2
	}, 2*time.Second, 10*time.Millisecond)
	_, err = r.ChangedFiles("", false) // R reload while the old patch is in flight
	require.NoError(t, err)
	close(release)
	assert.Equal(t, "stale", (<-staleDone)[1].Content, "the old view gets its own answer")

	lines, err := r.FileDiff(diff.FileDiffRequest{Path: "f.txt", ContextLines: patchContext})
	require.NoError(t, err)
	require.Len(t, lines, 2)
	assert.Equal(t, "fresh", lines[1].Content, "the new listing fetches its own patch")
	assert.Equal(t, 4, got.count())
}

func TestRendererFetchesAPatchOnceForConcurrentViews(t *testing.T) {
	var got capture
	release := make(chan struct{})
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		if !isFileRequest(req) {
			send(t, ws, listing(req["id"]))
			return
		}
		go func() {
			<-release
			send(t, ws, patchFor(req["id"], "b"))
		}()
	})
	r := NewRenderer(dialFake(t, url), "/srv/wt", SourceBranchRange)
	_, err := r.ChangedFiles("", false)
	require.NoError(t, err)

	const views = 4
	var wg sync.WaitGroup
	results := make(chan string, views)
	for range views {
		wg.Go(func() {
			lines, ferr := r.FileDiff(diff.FileDiffRequest{Path: "f.txt", ContextLines: patchContext})
			if assert.NoError(t, ferr) && assert.Len(t, lines, 2) {
				results <- lines[1].Content
			}
		})
	}
	require.Eventually(t, func() bool {
		return got.count() == 2
	}, 2*time.Second, 10*time.Millisecond)
	close(release)
	wg.Wait()
	close(results)
	for content := range results {
		assert.Equal(t, "b", content)
	}
	assert.Equal(t, 2, got.count(), "one listing, one patch")
}

func TestRendererRefusesAnotherSourceKind(t *testing.T) {
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		send(t, ws, success(req["id"], map[string]any{"sources": []any{
			map[string]any{"kind": "branch-range", "diff": sampleDiff, "truncated": false},
		}}))
	})
	r := NewRenderer(dialFake(t, url), "/srv/wt", SourceWorkingTree)
	_, err := r.ChangedFiles("", false)
	require.ErrorContains(t, err, "no working-tree diff (only branch-range)")

	_, err = diffPreview{}.source(SourceWorkingTree)
	require.ErrorContains(t, err, "no working-tree diff")
	assert.NotContains(t, err.Error(), "only")
}

func TestParsePatch(t *testing.T) {
	p, err := parsePatch(" \n")
	require.NoError(t, err)
	assert.True(t, p.empty(), "a blank patch changes nothing")
	files, err := p.changedFiles()
	require.NoError(t, err)
	assert.Empty(t, files)
	lines, err := p.fileDiff(diff.FileDiffRequest{Path: "f.txt"})
	require.NoError(t, err)
	assert.Empty(t, lines)

	p, err = parsePatch(sampleDiff)
	require.NoError(t, err)
	files, err = p.changedFiles()
	require.NoError(t, err)
	assert.Equal(t, []string{"f.txt", "g.txt"}, diff.FileEntryPaths(files))
	lines, err = p.fileDiff(diff.FileDiffRequest{Path: "g.txt"})
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Equal(t, "new", lines[0].Content, "the final newline is not read as a line")
}

// assertCutShort checks that lines end with the notice that the server cut
// the patch short, a row nothing can take for a line of the file.
func assertCutShort(t *testing.T, lines []diff.DiffLine) {
	t.Helper()
	require.NotEmpty(t, lines)
	last := lines[len(lines)-1]
	assert.Equal(t, diff.ChangeDivider, last.ChangeType, "the cursor, annotations and reverts all pass a divider by")
	assert.True(t, last.IsPlaceholder)
	assert.Contains(t, last.Content, "cut this diff short")
	assert.Zero(t, last.NewNum)
	assert.Zero(t, last.OldNum)
}

func TestRendererSaysAPatchWasCutShort(t *testing.T) {
	var got capture
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		if !isFileRequest(req) {
			send(t, ws, listing(req["id"]))
			return
		}
		patch := "diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1,2 @@\n a\n+b\n"
		send(t, ws, success(req["id"], map[string]any{"sources": []any{
			map[string]any{"kind": "branch-range", "diff": patch, "truncated": true},
		}}))
	})
	r := NewRenderer(dialFake(t, url), "/srv/wt", SourceBranchRange)
	_, err := r.ChangedFiles("", false)
	require.NoError(t, err)

	for _, contextLines := range []int{patchContext, 0, 10} {
		lines, err := r.FileDiff(diff.FileDiffRequest{Path: "f.txt", ContextLines: contextLines})
		require.NoError(t, err)
		require.Len(t, lines, 3, "context %d: the patch's lines and the notice, nothing laid over the file", contextLines)
		assert.Equal(t, "b", lines[1].Content)
		assertCutShort(t, lines)
	}
	assert.Equal(t, 2, got.count(), "a cut patch is not expanded with the file's contents")
}

func TestRendererSaysAnOlderServersPatchWasCutShort(t *testing.T) {
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		send(t, ws, success(req["id"], map[string]any{"sources": []any{
			map[string]any{"kind": "branch-range", "diff": sampleDiff, "truncated": true},
		}}))
	})
	r := NewRenderer(dialFake(t, url), "/srv/wt", SourceBranchRange)
	files, err := r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"f.txt", "g.txt"}, diff.FileEntryPaths(files))

	lines, err := r.FileDiff(diff.FileDiffRequest{Path: "f.txt", ContextLines: patchContext})
	require.NoError(t, err)
	assert.Len(t, lines, 2, "a file before the cut is whole")
	for range 2 { // the notice is added to each view, never to the patch itself
		lines, err = r.FileDiff(diff.FileDiffRequest{Path: "g.txt", ContextLines: patchContext})
		require.NoError(t, err)
		require.Len(t, lines, 2, "the last file, where the patch was cut")
		assertCutShort(t, lines)
	}
}
