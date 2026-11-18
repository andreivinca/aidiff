package t3client

import (
	"fmt"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
)

// twentyLines is a 20-line file with line 10 changed from "old" to "new".
var twentyLines = func() string {
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		if i == 10 {
			b.WriteString("new\n")
			continue
		}
		fmt.Fprintf(&b, "l%d\n", i)
	}
	return b.String()
}()

const twentyLinesPatch = "diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -7,7 +7,7 @@\n l7\n l8\n l9\n-old\n+new\n l11\n l12\n l13\n"

// startContentsFake serves f.txt's listing, patch and contents, and counts
// the contents requests.
func startContentsFake(t *testing.T, contents string, contentsCalls *int) *Renderer {
	t.Helper()
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		switch {
		case req["tag"] == "review.getDiffFileContents":
			*contentsCalls++
			p, _ := req["payload"].(map[string]any)
			assert.Equal(t, "f.txt", p["newPath"])
			assert.Equal(t, "change", p["changeType"])
			send(t, ws, success(req["id"], map[string]any{"oldContents": "", "newContents": contents}))
		case isFileRequest(req):
			send(t, ws, success(req["id"], map[string]any{"sources": []any{
				map[string]any{"kind": "branch-range", "diff": twentyLinesPatch, "truncated": false},
			}}))
		default:
			send(t, ws, listing(req["id"]))
		}
	})
	r := NewRenderer(dialFake(t, url), "/srv/wt", SourceBranchRange)
	_, err := r.ChangedFiles("", false)
	require.NoError(t, err)
	return r
}

func contents(lines []diff.DiffLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l.ChangeType) + l.Content
	}
	return out
}

func TestRendererGivesTheContextAskedFor(t *testing.T) {
	var calls int
	r := startContentsFake(t, twentyLines, &calls)

	lines, err := r.FileDiff(diff.FileDiffRequest{Path: "f.txt", ContextLines: patchContext})
	require.NoError(t, err)
	assert.Equal(t, 0, calls, "the patch's own context needs no contents")
	assert.Equal(t, []string{"~⋯ 6 lines ⋯", " l7", " l8", " l9", "-old", "+new", " l11", " l12", " l13"}, contents(lines))

	lines, err = r.FileDiff(diff.FileDiffRequest{Path: "f.txt", ContextLines: 5})
	require.NoError(t, err)
	assert.Equal(t, []string{"~⋯ 4 lines ⋯", " l5", " l6", " l7", " l8", " l9", "-old", "+new",
		" l11", " l12", " l13", " l14", " l15", "~⋯ 5 lines ⋯"}, contents(lines))
	assert.Equal(t, 15, lines[len(lines)-2].NewNum)
	assert.Equal(t, 15, lines[len(lines)-2].OldNum)

	lines, err = r.FileDiff(diff.FileDiffRequest{Path: "f.txt", ContextLines: 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"~⋯ 8 lines ⋯", " l9", "-old", "+new", " l11", "~⋯ 9 lines ⋯"}, contents(lines))

	lines, err = r.FileDiff(diff.FileDiffRequest{Path: "f.txt"})
	require.NoError(t, err)
	require.Len(t, lines, 21, "the whole file: 19 unchanged lines, one removed, one added")
	assert.Equal(t, " l1", contents(lines)[0])
	assert.Equal(t, " l20", contents(lines)[20])
	assert.Equal(t, diff.DiffLine{OldNum: 20, NewNum: 20, Content: "l20", ChangeType: diff.ChangeContext}, lines[20])
	assert.Equal(t, 1, calls, "the contents are fetched once per listing")
}

func TestRendererKeepsThePatchWhenContentsDoNotMatch(t *testing.T) {
	var calls int
	r := startContentsFake(t, "something else\n", &calls)
	lines, err := r.FileDiff(diff.FileDiffRequest{Path: "f.txt", ContextLines: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, []string{"~⋯ 6 lines ⋯", " l7", " l8", " l9", "-old", "+new", " l11", " l12", " l13"}, contents(lines),
		"a file changed since its patch keeps the patch")
}

func TestExpandFull_AddsAndRemovesAtTheEdges(t *testing.T) {
	patch := []diff.DiffLine{
		{OldNum: 1, ChangeType: diff.ChangeRemove, Content: "gone"},
		{NewNum: 1, ChangeType: diff.ChangeAdd, Content: "first"},
		{OldNum: 2, NewNum: 2, ChangeType: diff.ChangeContext, Content: "b"},
		{ChangeType: diff.ChangeDivider, Content: "⋯ 2 lines ⋯"},
		{OldNum: 5, ChangeType: diff.ChangeRemove, Content: "e"},
		{NewNum: 6, ChangeType: diff.ChangeAdd, Content: "tail"},
	}
	full, ok := expandFull(patch, []string{"first", "b", "c", "d", "f", "tail"})
	require.True(t, ok)
	assert.Equal(t, []string{"-gone", "+first", " b", " c", " d", "-e", " f", "+tail"}, contents(full))
	assert.Equal(t, diff.DiffLine{OldNum: 6, NewNum: 5, Content: "f", ChangeType: diff.ChangeContext}, full[6])
}
