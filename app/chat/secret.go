package chat

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// SecretRequest is the agent asking the user for a secret, such as a sudo
// password or a token. While one is pending the composer takes the secret
// instead of a message: what is typed shows as dots, enter hands it to the
// backend, which stores it out of the conversation, and esc declines.
type SecretRequest struct {
	ID          string // the request's id, as SecretAnswerer takes it
	Label       string // what is asked for, e.g. "sudo password"
	Reason      string // why the agent needs it
	URL         string // where the secret can be found or made, if the agent named one
	Placeholder string
	Status      string // how it ended: saved, declined or cancelled; pending while open
	Pending     bool   // the agent still waits for it
}

// SecretAnswerer is a backend that answers the agent's secret requests.
type SecretAnswerer interface {
	// ProvideSecret hands secret over for the request requestID.
	ProvideSecret(ctx context.Context, requestID, secret string) error
	// DeclineSecret turns the request requestID down.
	DeclineSecret(ctx context.Context, requestID string) error
}

// secretInput is what has been typed of a secret, for the request it
// answers. It is never shown, kept in the input history or sent as a message.
type secretInput struct {
	id    string
	runes []rune
}

// clear forgets what was typed.
func (s *secretInput) clear() {
	for i := range s.runes {
		s.runes[i] = 0
	}
	s.runes = s.runes[:0]
}

// secretLocked is the secret request the composer takes input for, nil when
// there is none: one pending, a backend that answers it, and no dialog open.
// What was typed for an earlier request is dropped. mu must be held.
func (p *Pane) secretLocked() *SecretRequest {
	r := p.conv.Secret
	if r == nil || !r.Pending || p.stop.turn != "" || p.activity.open {
		return nil
	}
	if _, ok := p.backend.(SecretAnswerer); !ok {
		return nil
	}
	if p.secret.id != r.ID {
		p.secret.clear()
		p.secret.id = r.ID
	}
	return r
}

// secretTextLocked takes typed bytes for a pending secret: characters are
// added, backspace deletes the last one, ctrl+u all of them, enter sends
// the secret, esc declines the request; other keys do nothing. It reports
// whether a secret was pending. mu must be held.
func (p *Pane) secretTextLocked(s string) bool {
	r := p.secretLocked()
	if r == nil {
		return false
	}
	if s == "\x1b" {
		p.declineSecretLocked(r)
		return true
	}
	if strings.HasPrefix(s, "\x1b") {
		return true // an escape sequence: a key the secret has no use for
	}
	for _, ch := range s {
		switch {
		case ch == '\r' || ch == '\n':
			p.sendSecretLocked(r)
			return true
		case ch == '\x7f' || ch == '\b':
			if n := len(p.secret.runes); n > 0 {
				p.secret.runes[n-1] = 0
				p.secret.runes = p.secret.runes[:n-1]
			}
		case ch == '\x15': // ctrl+u
			p.secret.clear()
		case unicode.IsControl(ch):
		default:
			p.secret.runes = append(p.secret.runes, ch)
		}
	}
	return true
}

// secretPasteLocked adds pasted text to a pending secret, without its line
// breaks; it reports whether a secret was pending. mu must be held.
func (p *Pane) secretPasteLocked(s string) bool {
	if p.secretLocked() == nil {
		return false
	}
	for _, ch := range strings.TrimRight(s, "\r\n") {
		if !unicode.IsControl(ch) {
			p.secret.runes = append(p.secret.runes, ch)
		}
	}
	return true
}

// secretKeyLocked takes a key for a pending secret: page up and down still
// scroll the transcript, every other key is dropped. It reports whether a
// secret was pending. mu must be held.
func (p *Pane) secretKeyLocked(k uv.KeyPressEvent) bool {
	if p.secretLocked() == nil {
		return false
	}
	switch k.Code {
	case uv.KeyPgUp:
		p.scrollLocked(p.pageLocked())
	case uv.KeyPgDown:
		p.scrollLocked(-p.pageLocked())
	case uv.KeyBackspace:
		p.secretTextLocked("\x7f")
	case uv.KeyEnter:
		p.secretTextLocked("\r")
	case uv.KeyEscape:
		p.secretTextLocked("\x1b")
	}
	return true
}

// sendSecretLocked hands the typed secret to the backend and forgets it
// here, whatever the backend answers. mu must be held.
func (p *Pane) sendSecretLocked(r *SecretRequest) {
	if len(p.secret.runes) == 0 {
		p.note = "type the " + secretName(r) + ", then press Enter; Esc declines"
		return
	}
	secret := string(p.secret.runes)
	b, _ := p.backend.(SecretAnswerer)
	id := r.ID
	err := p.requestLocked(request{run: func(ctx context.Context) error {
		if err := b.ProvideSecret(ctx, id, secret); err != nil {
			return fmt.Errorf("the %s was not saved: %w", secretName(r), err)
		}
		return nil
	}})
	if err != nil {
		p.note = err.Error() // kept to send again once the pane can
		return
	}
	p.secret.clear()
}

// declineSecretLocked turns the request down. mu must be held.
func (p *Pane) declineSecretLocked(r *SecretRequest) {
	b, _ := p.backend.(SecretAnswerer)
	id := r.ID
	err := p.requestLocked(request{run: func(ctx context.Context) error {
		if err := b.DeclineSecret(ctx, id); err != nil {
			return fmt.Errorf("decline failed: %w", err)
		}
		return nil
	}})
	if err != nil {
		p.note = err.Error()
		return
	}
	p.secret.clear()
}

// secretRowLocked is the input row while a secret is pending: what is asked
// for, then a dot per typed character. ok is false when none is pending.
// mu must be held.
func (p *Pane) secretRowLocked(w int, showCursor bool) (row string, ok bool) {
	r := p.secretLocked()
	if r == nil {
		return "", false
	}
	c := p.composer
	head := "🔑 " + secretName(r) + ": "
	cursor := ""
	if showCursor {
		cursor = c.text.Render("█")
	}
	if len(p.secret.runes) == 0 {
		hint := "type it, Enter sends it to T3 Code (not to the chat) · Esc declines"
		if r.Placeholder != "" {
			hint = r.Placeholder + " · " + hint
		}
		room := max(0, w-ansi.StringWidth(head)-ansi.StringWidth(cursor))
		return c.text.Render(head) + cursor + c.muted.Render(ansi.Truncate(hint, room, "…")), true
	}
	dots := strings.Repeat("•", len(p.secret.runes))
	room := max(1, w-ansi.StringWidth(head)-1)
	if len(p.secret.runes) > room {
		dots = "…" + strings.Repeat("•", room-1)
	}
	return c.text.Render(head+dots) + cursor, true
}

// secretName is what the request asks for, "secret" when it says nothing.
func secretName(r *SecretRequest) string {
	if r.Label != "" {
		return r.Label
	}
	return "secret"
}

// secretRows shows a secret request in the transcript: while it is pending,
// what is asked for and why, with how to answer; then how it ended, in a line.
func secretRows(s SecretRequest, w int, st styles) []string {
	if !s.Pending {
		ended := s.Status
		if ended == "" || ended == "pending" {
			ended = "no longer asked"
		}
		return []string{st.muted.Render(ansi.Truncate(inset+"🔑 "+secretName(&s)+": "+ended, w, "…"))}
	}
	out := []string{st.warn.Render(ansi.Truncate(inset+"🔑 the agent asks for the "+secretName(&s), w, "…"))}
	if s.Reason != "" {
		out = append(out, wrapLines(s.Reason, w)...)
	}
	if s.URL != "" {
		out = append(out, wrapLines("  "+s.URL, w)...)
	}
	return append(out, st.muted.Render(ansi.Truncate(inset+"  type it below, Enter sends it to T3 Code · Esc declines", w, "…")))
}
