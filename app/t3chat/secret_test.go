package t3chat

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// sudoRequest is the secret_request item of an agent asking for a sudo password.
func sudoRequest(status, secretStatus string) t3client.TurnItem {
	title := "sudo password"
	return t3client.TurnItem{ID: "turn-item:secret-request:1", Type: "secret_request", Status: status, Title: &title,
		Label: "sudo password", Reason: "install flatpak with pacman", Delivery: "file", SecretStatus: secretStatus}
}

func TestConversation_SecretRequest(t *testing.T) {
	f := newFakeServer(fixture())
	s := subscribe(t, f)
	f.live().TurnItem(sudoRequest("waiting", "pending"))
	c := s.v.next(t)
	require.NotNil(t, c.Secret, "the composer takes the secret")
	assert.Equal(t, chat.SecretRequest{ID: "turn-item:secret-request:1", Label: "sudo password", Reason: "install flatpak with pacman",
		Status: "pending", Pending: true}, *c.Secret)
	assert.Equal(t, chat.AgentWaiting, c.State, "waiting for the user, not working")
	assert.Contains(t, c.Entries, chat.Entry(*c.Secret), "and the request shows in the transcript")

	f.live().TurnItem(sudoRequest("cancelled", "cancelled"))
	c = s.v.next(t)
	assert.Nil(t, c.Secret, "a request that timed out takes no secret")
	assert.NotEqual(t, chat.AgentWaiting, c.State)
	assert.Contains(t, c.Entries, chat.Entry(chat.SecretRequest{ID: "turn-item:secret-request:1", Label: "sudo password",
		Reason: "install flatpak with pacman", Status: "cancelled"}))
}

func TestPane_AnswersASecretRequest(t *testing.T) {
	f := newFakeServer(fixture())
	p := paneOn(t, f)
	f.live().TurnItem(sudoRequest("waiting", "pending"))
	drain(p)
	assert.Contains(t, view(p), "🔑 the agent asks for the sudo password")
	assert.Equal(t, t3client.Command{"type": "secrets.answerRequest", "threadId": "th-1", "turnItemId": "turn-item:secret-request:1",
		"secret": "hunter2", "decline": false}, send(t, p, f, "hunter2"), "the secret goes to T3's secret store, not as a message")
	p.SendText("\x1b")
	select {
	case cmd := <-f.cmds:
		assert.Equal(t, true, cmd["decline"], "esc declines")
	case <-time.After(waitLimit):
		t.Fatal("nothing declined")
	}
}
