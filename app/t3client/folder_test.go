package t3client

import (
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
)

// startFolderFake serves a folder with src/ and README.md, src/ holding
// main.go and lib/; reading a.bin fails as binary, big.txt comes cut short.
func startFolderFake(t *testing.T, got *capture) *FolderReader {
	t.Helper()
	listings := map[string][]any{
		"": {map[string]any{"path": "src", "kind": "directory"}, map[string]any{"path": "README.md", "kind": "file"}},
		"src": {map[string]any{"path": "src/main.go", "kind": "file"}, map[string]any{"path": "src/lib", "kind": "directory"},
			map[string]any{"path": "src/broken", "kind": "directory"}}, // listed, but listing it fails
		"src/lib": nil,
	}
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		p, _ := req["payload"].(map[string]any)
		assert.Equal(t, "/srv/wt", p["cwd"])
		switch req["tag"] {
		case "projects.listEntries":
			dir, _ := p["directoryPath"].(string)
			entries, ok := listings[dir]
			if !ok {
				send(t, ws, map[string]any{"_tag": "Exit", "requestId": req["id"], "exit": map[string]any{
					"_tag": "Failure", "cause": []any{map[string]any{"_tag": "Fail", "error": map[string]any{
						"_tag": "ProjectListEntriesError", "detail": "gone"}}}}})
				return
			}
			send(t, ws, success(req["id"], map[string]any{"entries": entries, "truncated": false}))
		case "projects.readFile":
			switch p["relativePath"] {
			case "a.bin":
				send(t, ws, map[string]any{"_tag": "Exit", "requestId": req["id"], "exit": map[string]any{
					"_tag": "Failure", "cause": []any{map[string]any{"_tag": "Fail", "error": map[string]any{
						"_tag": "ProjectReadFileError", "detail": "a.bin is a binary file"}}}}})
			case "big.txt":
				send(t, ws, success(req["id"], map[string]any{"relativePath": "big.txt", "contents": "one\n", "byteLength": 9e6, "truncated": true}))
			default:
				send(t, ws, success(req["id"], map[string]any{"relativePath": p["relativePath"], "contents": "package main\n\nfunc main() {}\n",
					"byteLength": 30, "truncated": false}))
			}
		}
	})
	return NewFolderReader(dialFake(t, url), "/srv/wt")
}

func TestFolderReader_ListsOpenFolders(t *testing.T) {
	var got capture
	r := startFolderFake(t, &got)

	entries, err := r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []diff.FileEntry{{Path: "README.md"}, {Path: "src", IsDir: true}}, entries)
	assert.Equal(t, 1, got.count(), "a closed folder is not listed")

	r.SetDirectory("src", true)
	r.SetDirectory("src/lib", true)
	entries, err = r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []diff.FileEntry{
		{Path: "README.md"}, {Path: "src", IsDir: true, Expanded: true}, {Path: "src/broken", IsDir: true},
		{Path: "src/lib", IsDir: true, Expanded: true}, {Path: "src/main.go"},
	}, entries)

	r.SetDirectory("src", false)
	entries, err = r.ChangedFiles("", false)
	require.NoError(t, err)
	assert.Equal(t, []diff.FileEntry{{Path: "README.md"}, {Path: "src", IsDir: true}}, entries, "closing a folder closes what is under it")
	r.mu.Lock()
	assert.Empty(t, r.expanded)
	r.mu.Unlock()
}

func TestFolderReader_ForgetsAFolderItCannotList(t *testing.T) {
	var got capture
	r := startFolderFake(t, &got)
	r.SetDirectory("src", true)
	r.SetDirectory("src/broken", true)
	entries, err := r.ChangedFiles("", false)
	require.NoError(t, err, "only the root's failure is an error")
	assert.Contains(t, entries, diff.FileEntry{Path: "src/broken", IsDir: true}, "shown closed")
	r.mu.Lock()
	assert.True(t, r.expanded["src"])
	assert.False(t, r.expanded["src/broken"], "and forgotten")
	r.mu.Unlock()
}

func TestFolderReader_ShowsAFileWhole(t *testing.T) {
	var got capture
	r := startFolderFake(t, &got)

	lines, err := r.FileDiff(diff.FileDiffRequest{Path: "src/main.go"})
	require.NoError(t, err)
	assert.Equal(t, []diff.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "package main", ChangeType: diff.ChangeContext},
		{OldNum: 2, NewNum: 2, Content: "", ChangeType: diff.ChangeContext},
		{OldNum: 3, NewNum: 3, Content: "func main() {}", ChangeType: diff.ChangeContext},
	}, lines)
	assert.Equal(t, "src/main.go", payloadOf(t, got.last())["relativePath"])

	lines, err = r.FileDiff(diff.FileDiffRequest{Path: "a.bin"})
	require.NoError(t, err, "a file the server will not show is a line saying why, not a failed load")
	require.Len(t, lines, 1)
	assert.True(t, lines[0].IsPlaceholder)
	assert.Equal(t, "a.bin is a binary file", lines[0].Content)

	lines, err = r.FileDiff(diff.FileDiffRequest{Path: "big.txt"})
	require.NoError(t, err)
	require.Len(t, lines, 2)
	assert.Equal(t, "one", lines[0].Content)
	assert.True(t, lines[1].IsPlaceholder, "a file cut short says so")
}
