package t3client

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// capture records the request frames a fake server receives.
type capture struct {
	mu   sync.Mutex
	reqs []map[string]any
}

func (c *capture) add(req map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reqs = append(c.reqs, req)
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reqs)
}

func (c *capture) last() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reqs[len(c.reqs)-1]
}

func payloadOf(t *testing.T, req map[string]any) map[string]any {
	t.Helper()
	p, ok := req["payload"].(map[string]any)
	require.True(t, ok, "payload is an object: %v", req["payload"])
	return p
}

func TestLaunchThreadRequest(t *testing.T) {
	var got capture
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		send(t, ws, success(req["id"], map[string]any{"threadId": "th-1", "resumed": false}))
	})
	c := dial(t, url)
	id, err := LaunchThread(context.Background(), c, LaunchRequest{
		ProjectID: "p1",
		Message:   "Fix the login page\n\nIt breaks on empty passwords.",
		Model:     ModelSelection{InstanceID: "claude", Model: "opus"},
	})
	require.NoError(t, err)
	assert.Equal(t, "th-1", id)

	req := got.last()
	assert.Equal(t, "orchestration.launchThread", req["tag"])
	p := payloadOf(t, req)
	assert.Regexp(t, uuidRe, p["commandId"])
	assert.Equal(t, "p1", p["projectId"])
	assert.Equal(t, "Fix the login page", p["title"], "the title is the message's first line")
	assert.Equal(t, map[string]any{"instanceId": "claude", "model": "opus"}, p["modelSelection"])
	assert.Equal(t, "full-access", p["runtimeMode"])
	assert.Equal(t, "default", p["interactionMode"])
	assert.Equal(t, map[string]any{"type": "root"}, p["workspaceStrategy"])
	msg, _ := p["initialMessage"].(map[string]any)
	assert.Equal(t, "Fix the login page\n\nIt breaks on empty passwords.", msg["text"])
	assert.Equal(t, []any{}, msg["attachments"])
	assert.Regexp(t, uuidRe, msg["messageId"])

	id, err = LaunchThread(context.Background(), c, LaunchRequest{ProjectID: "p1", Message: "  \n"})
	require.NoError(t, err, "a thread may start empty")
	assert.Equal(t, "th-1", id)
	p = payloadOf(t, got.last())
	assert.Nil(t, p["initialMessage"], "no message, no run")
	assert.Equal(t, "New thread", p["title"])
	assert.Equal(t, true, p["generateTitle"], "T3 names it after the first message")
}

func TestSendMessageRequest(t *testing.T) {
	var got capture
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		got.add(req)
		send(t, ws, success(req["id"], map[string]any{"sequence": 7}))
	})
	c := dial(t, url)
	require.NoError(t, SendMessage(context.Background(), c, "th-1", "  please add tests  "))
	req := got.last()
	assert.Equal(t, "orchestration.dispatchCommand", req["tag"])
	p := payloadOf(t, req)
	assert.Equal(t, "message.dispatch", p["type"])
	assert.Equal(t, "th-1", p["threadId"])
	assert.Equal(t, "please add tests", p["text"])
	assert.Equal(t, "user", p["createdBy"])
	assert.Equal(t, map[string]any{"type": "queue_after_active"}, p["dispatchMode"], "a busy thread is never interrupted")
	assert.Regexp(t, uuidRe, p["commandId"])
	assert.Regexp(t, uuidRe, p["messageId"])
	assert.NotEqual(t, p["commandId"], p["messageId"])

	require.Error(t, SendMessage(context.Background(), c, "th-1", " "))
}

func TestDefaultModel(t *testing.T) {
	var calls capture
	var mu sync.Mutex
	cfg := map[string]any{"providers": []any{}, "settings": map[string]any{}}
	setCfg := func(v map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		cfg = v
	}
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		calls.add(req)
		mu.Lock()
		v := cfg
		mu.Unlock()
		send(t, ws, success(req["id"], v))
	})
	c := dial(t, url)

	own := Project{ID: "p", DefaultModelSelection: &ModelSelection{InstanceID: "codex", Model: "gpt"}}
	m, err := DefaultModel(context.Background(), c, own)
	require.NoError(t, err)
	assert.Equal(t, ModelSelection{InstanceID: "codex", Model: "gpt"}, m)
	assert.Equal(t, 0, calls.count(), "a project default needs no server call")

	_, err = DefaultModel(context.Background(), c, Project{ID: "p"})
	require.Error(t, err, "no default anywhere")
	assert.Equal(t, "server.getConfig", calls.last()["tag"])

	setCfg(map[string]any{
		"providers": []any{
			map[string]any{"instanceId": "off", "enabled": false, "models": []any{map[string]any{"slug": "x"}}},
			map[string]any{"instanceId": "claude", "enabled": true, "models": []any{
				map[string]any{"slug": "haiku"}, map[string]any{"slug": "opus", "isDefault": true}}},
		},
		"settings": map[string]any{},
	})
	m, err = DefaultModel(context.Background(), c, Project{ID: "p"})
	require.NoError(t, err)
	assert.Equal(t, ModelSelection{InstanceID: "claude", Model: "opus"}, m, "the first enabled provider's default model")

	setCfg(map[string]any{
		"providers": []any{map[string]any{"instanceId": "claude", "enabled": true, "models": []any{map[string]any{"slug": "opus"}}}},
		"settings":  map[string]any{"defaultModelSelection": map[string]any{"instanceId": "s", "model": "server-pick"}},
	})
	m, err = DefaultModel(context.Background(), c, Project{ID: "p"})
	require.NoError(t, err)
	assert.Equal(t, ModelSelection{InstanceID: "s", Model: "server-pick"}, m, "the server setting wins over providers")
}

func TestSSHHostsFrom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte(`# my hosts
Host workpc
  HostName 10.0.0.2
Host box boxdev   # two names
Host *
  ServerAliveInterval 30
Match host boxdev
host workpc
Host !bastion
`), 0o600))
	assert.Equal(t, []string{"workpc", "box", "boxdev"}, sshHostsFrom(path))
	assert.Nil(t, sshHostsFrom(filepath.Join(t.TempDir(), "missing")))
}

func TestTokenStoreServers(t *testing.T) {
	s := TokenStore{Path: filepath.Join(t.TempDir(), "tokens.json")}
	servers, err := s.Servers()
	require.NoError(t, err)
	assert.Empty(t, servers)
	require.NoError(t, s.Put("ssh://old", "a"))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, s.Put("http://192.168.1.5:3000/", "b"))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, s.Put("local", "c"))
	servers, err = s.Servers()
	require.NoError(t, err)
	assert.Equal(t, []string{"local", "http://192.168.1.5:3000", "ssh://old"}, servers, "most recently saved first")
}

func TestTitleFromAndNewID(t *testing.T) {
	assert.Equal(t, "first line", titleFrom("  first line  \nsecond"))
	title := titleFrom(strings.Repeat("word ", 30))
	assert.LessOrEqual(t, len([]rune(title)), maxTitleRunes)
	assert.True(t, strings.HasSuffix(title, "…"))
	first, second := newID(), newID()
	assert.Regexp(t, uuidRe, first)
	assert.NotEqual(t, first, second)
}
