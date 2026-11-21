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
// trying, waiting longer each time, until it is back, the user connects
// elsewhere or reconnectFor has passed. One that ends while another is live
// is reconnected once the user selects it again (useLive). Back, it takes
// the old session's place: a review that came over it is rebuilt with its
// annotations, as the reconnect command used to do, and the parked ones as
// they come back on screen.

// reconnectDelays are the waits before each try; the last repeats.
var reconnectDelays = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}

// reconnectFor is how long a reconnect keeps trying before it gives up;
// selecting the connection again starts another.
var reconnectFor = 30 * time.Minute

// loopsWait bounds Close's wait for the goroutines it canceled: a dial
// stuck past its canceled context must not hold up the exit, which has a
// deadline of its own. One that lands later closes what it reached.
var loopsWait = 2 * time.Second

// watchDrop reconnects when s's connection ends while it is the live one.
// A session that cannot tell (no Done channel) is not watched. mu must be
// held and the palette open, or the palette not shared yet.
func (t *Palette) watchDrop(s Session) {
	done := s.Done()
	if done == nil {
		return
	}
	t.loops.Go(func() {
		select {
		case <-done:
		case <-t.ctx.Done():
			return
		}
		if t.claimRetry(s) {
			t.autoReconnect(s)
		}
	})
}

// ended reports whether s's connection is over; a session that cannot tell
// never is.
func ended(s Session) bool {
	done := s.Done()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// claimRetry reports whether a reconnect of s is to start, and if so
// records it as running: s is live, the palette open and running, and no
// reconnect of s runs yet.
func (t *Palette) claimRetry(s Session) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.claimRetryLocked(s)
}

// claimRetryLocked is claimRetry with mu held.
func (t *Palette) claimRetryLocked(s Session) bool {
	if t.closed || t.program == nil || t.live != s || t.retrying[s] {
		return false
	}
	t.retrying[s] = true
	return true
}

// keepRetrying reports whether the reconnect of old goes on: old still
// live, the palette open. When not, the reconnect is recorded as over, in
// the same step, so a selection of old right after starts a new one.
func (t *Palette) keepRetrying(old Session) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed && t.live == old {
		return true
	}
	delete(t.retrying, old)
	return false
}

// stopRetrying records the reconnect of old as over.
func (t *Palette) stopRetrying(old Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.retrying, old)
}

// handOff records s, which the reconnect of old reached, as on its way to
// takeOver, so that Close closes it should the program end before. It
// reports false, and the caller closes s, when old is no longer live or the
// palette closed.
func (t *Palette) handOff(old, s Session) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.retrying, old)
	if t.closed || t.live != old {
		return false
	}
	t.incoming[s] = true
	return true
}

// autoReconnect dials old's server again until it answers, as long as old
// is the live session, then hands the new session to the screen. Close
// cancels it, its waits and its dial included. claimRetry must have
// reported true.
func (t *Palette) autoReconnect(old Session) {
	t.mu.Lock()
	p := t.program
	t.mu.Unlock()
	label := old.Label()
	p.Send(splitview.FlashMsg{Text: "T3: connection to " + label + " lost; reconnecting…"})
	start := time.Now()
	for i := 0; ; i++ {
		select {
		case <-time.After(reconnectDelays[min(i, len(reconnectDelays)-1)]):
		case <-t.ctx.Done():
			return
		}
		if !t.keepRetrying(old) {
			return // closed, or the user connected elsewhere
		}
		if time.Since(start) > reconnectFor {
			t.stopRetrying(old)
			p.Send(splitview.FlashMsg{Text: "T3: gave up reconnecting to " + label + "; select it under Connections to try again"})
			return
		}
		ctx, cancel := context.WithTimeout(t.ctx, ConnectTimeout)
		s, sh, err := reach(ctx, old.Redial)
		cancel()
		if err != nil {
			if t.ctx.Err() != nil {
				return
			}
			wait := reconnectDelays[min(i+1, len(reconnectDelays)-1)]
			p.Send(splitview.FlashMsg{Text: fmt.Sprintf("T3: %s still unreachable (%v); trying again in %s", label, err, wait)})
			continue
		}
		if !t.handOff(old, s) {
			_ = s.Close() // nobody else will: closed meanwhile, or the user connected elsewhere
			return
		}
		p.Send(splitview.RunMsg{Run: func(inner tea.Model) tea.Cmd {
			return t.takeOver(old, s, sh, inner)
		}})
		return
	}
}
