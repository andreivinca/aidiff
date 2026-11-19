package chat

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// askingSecret is a conversation whose agent waits for a sudo password.
func askingSecret() Conversation {
	r := SecretRequest{ID: "s1", Label: "sudo password", Reason: "install flatpak with pacman", Status: "pending", Pending: true}
	c := fixture()
	c.State = AgentWaiting
	c.Entries = append(c.Entries, r)
	c.Secret = &r
	return c
}

func TestPane_SecretIsTypedMaskedAndSentApart(t *testing.T) {
	p, f := newTestPane(t, 80, 30, askingSecret())
	out := view(p)
	assert.Contains(t, out, "🔑 the agent asks for the sudo password", "the request shows in the transcript")
	assert.Contains(t, out, "install flatpak with pacman")
	assert.Contains(t, out, "🔑 sudo password: ", "the composer asks for it")
	assert.Contains(t, out, "Waiting for you")

	p.SendText("hunter")
	p.Paste("2\n")
	out = view(p)
	assert.NotContains(t, out, "hunter", "never shown")
	assert.Contains(t, out, "sudo password: •••••••")
	p.SendText("\x7f")
	assert.Contains(t, view(p), "sudo password: ••••••█")

	c := ask(t, p, f, func() { p.SendText("\r") })
	assert.Equal(t, call{kind: "secret", id: "s1", secret: "hunter"}, c, "the secret goes to the backend's secret answer, not as a message")
	locked(p, func() {
		assert.Empty(t, p.secret.runes, "forgotten once sent")
		assert.Empty(t, p.input.text(), "the draft never held it")
	})

	c = ask(t, p, f, func() { p.SendKey(uv.KeyPressEvent{Code: uv.KeyEscape}) })
	assert.Equal(t, call{kind: "decline secret", id: "s1"}, c, "esc declines")

	done := askingSecret()
	done.Secret, done.State = nil, AgentRunning
	done.Entries[len(done.Entries)-1] = SecretRequest{ID: "s1", Label: "sudo password", Status: "cancelled"}
	show(t, p, f, done)
	out = view(p)
	assert.Contains(t, out, "🔑 sudo password: cancelled", "how it ended, in a line")
	assert.NotContains(t, out, "🔑 sudo password: █")
	p.SendText("hi")
	assert.Contains(t, view(p), "hi", "the composer takes messages again")
}

func TestPane_EmptySecretIsNotSent(t *testing.T) {
	p, f := newTestPane(t, 80, 30, askingSecret())
	p.SendText("\r")
	assert.True(t, strings.Contains(view(p), "type the sudo password, then press Enter"))
	select {
	case c := <-f.calls:
		require.Failf(t, "nothing is sent", "got %+v", c)
	default:
	}
}
