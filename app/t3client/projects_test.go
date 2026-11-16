package t3client

import (
	"context"
	"testing"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowseAndCreateProject(t *testing.T) {
	var got capture
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		switch req["tag"] {
		case "filesystem.browse":
			send(t, ws, success(req["id"], map[string]any{"parentPath": "/home/me/Projects", "entries": []any{
				map[string]any{"name": "aidiff", "fullPath": "/home/me/Projects/aidiff"},
				map[string]any{"name": "sabra2", "fullPath": "/home/me/Projects/sabra2"},
			}}))
		case "projects.mutate":
			p := payloadOf(t, req)
			send(t, ws, success(req["id"], map[string]any{"id": p["projectId"], "title": p["title"],
				"workspaceRoot": p["workspaceRoot"], "defaultModelSelection": nil, "scripts": []any{}}))
		}
	})
	c := dialFake(t, url)
	ctx := context.Background()

	parent, entries, err := c.Browse(ctx, "/home/me/Projects/")
	require.NoError(t, err)
	assert.Equal(t, "/home/me/Projects/", payloadOf(t, got.last())["partialPath"])
	assert.Equal(t, "/home/me/Projects", parent)
	assert.Equal(t, []DirEntry{{Name: "aidiff", Path: "/home/me/Projects/aidiff"}, {Name: "sabra2", Path: "/home/me/Projects/sabra2"}}, entries)

	p, err := c.CreateProject(ctx, "sabra2", "/home/me/Projects/sabra2")
	require.NoError(t, err)
	payload := payloadOf(t, got.last())
	assert.Equal(t, "projects.mutate", got.last()["tag"])
	assert.Equal(t, "project.create", payload["type"])
	assert.Regexp(t, uuidRe, payload["projectId"], "the client mints the project id")
	assert.Regexp(t, uuidRe, payload["commandId"])
	assert.Equal(t, "/home/me/Projects/sabra2", payload["workspaceRoot"])
	assert.Equal(t, payload["projectId"], p.ID)
	assert.Equal(t, "sabra2", p.Title)
	assert.False(t, p.Archived())
}
