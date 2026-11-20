package t3palette

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/splitview"
)

// Reconnecting by itself. Every connection the palette makes is watched;
// when the live one ends without the palette closing it (the server
// restarted, the SSH tunnel dropped, the laptop slept), the palette dials
// the same server again, the way the user's connection was made, and keeps
// trying, waiting longer each time, until it is back or the user connects
// elsewhere. Back, it takes the old session's place: a review that came over
// it is rebuilt with its annotations, as the reconnect command used to do.

// reconnectDelays are the waits before each try; the last repeats.
var reconnectDelays = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}

// watchDrop reconnects when s's connection ends while it is the live one.
// A session that cannot tell (no Done channel) is not watched.
func (t *Palette) watchDrop(s Session) {
	done := s.Done()
	if done == nil {
		return
	}
	go func() {
		<-done
		if t.isLive(s) {
			t.autoReconnect(s)
		}
	}()
}

// isLive reports whether s is still the session the commands use, the
// palette open and running.
func (t *Palette) isLive(s Session) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closed && t.program != nil && t.live == s
}

// autoReconnect dials old's server again until it answers, as long as old
// is the live session, then hands the new session to the screen.
func (t *Palette) autoReconnect(old Session) {
	t.mu.Lock()
	p := t.program
	t.mu.Unlock()
	if p == nil {
		return
	}
	label := old.Label()
	p.Send(splitview.FlashMsg{Text: "T3: connection to " + label + " lost; reconnecting…"})
	for i := 0; ; i++ {
		time.Sleep(reconnectDelays[min(i, len(reconnectDelays)-1)])
		if !t.isLive(old) {
			return // closed, or the user connected elsewhere
		}
		ctx, cancel := context.WithTimeout(context.Background(), ConnectTimeout)
		s, sh, err := reach(ctx, old.Redial)
		cancel()
		if err != nil {
			wait := reconnectDelays[min(i+1, len(reconnectDelays)-1)]
			p.Send(splitview.FlashMsg{Text: fmt.Sprintf("T3: %s still unreachable (%v); trying again in %s", label, err, wait)})
			continue
		}
		p.Send(splitview.RunMsg{Run: func(inner tea.Model) tea.Cmd {
			return t.takeOver(old, s, sh, inner)
		}})
		return
	}
}
