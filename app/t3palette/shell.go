package t3palette

import (
	"context"
	"fmt"
	"time"

	"github.com/umputun/revdiff/app/t3client"
)

// shellMaxAge is how old the cached thread list may be before the palette
// refreshes it in the background.
const shellMaxAge = 5 * time.Second

// shellMaxBackoff is the longest wait between background fetches of the
// thread list while they fail; the wait doubles from shellMaxAge.
const shellMaxBackoff = time.Minute

// shellRefreshedMsg redraws the palette once a background fetch landed; the
// split passes it on and nothing acts on it.
type shellRefreshedMsg struct{}

// fetchShell lists s's projects and threads, and caches them while s is the
// live session: a listing from another server never replaces the live one's.
func (t *Palette) fetchShell(ctx context.Context, s Session) (t3client.Shell, error) {
	sh, err := s.Server().FetchShell(ctx)
	if err != nil {
		return t3client.Shell{}, fmt.Errorf("list threads: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if s == t.live {
		t.setShellLocked(sh)
	}
	return sh, nil
}

// setShellLocked caches sh as the live server's listing, fetched now. mu
// must be held.
func (t *Palette) setShellLocked(sh t3client.Shell) {
	t.shell, t.shellAt = sh, time.Now()
	t.shellErr, t.shellFails, t.shellRetry = nil, 0, time.Time{}
}

// resetShellLocked forgets the cached listing, another server's, and its
// failures, so the next look fetches the live server's. mu must be held.
func (t *Palette) resetShellLocked() {
	t.shell, t.shellAt = t3client.Shell{}, time.Time{}
	t.shellErr, t.shellFails, t.shellRetry = nil, 0, time.Time{}
}

// refreshShell fetches the thread list in the background when the cached
// one is older than shellMaxAge. A failed fetch is kept for the sidebar to
// show and the next one waits, longer after each failure: every redraw asks
// for a refresh. A fetch that lands late is dropped, as it is from any
// session that is no longer live; Close cancels it and waits for it.
func (t *Palette) refreshShell() {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, p := t.live, t.program
	if t.closed || s == nil || t.refreshing == s || time.Since(t.shellAt) <= shellMaxAge || time.Now().Before(t.shellRetry) {
		return
	}
	t.refreshing = s
	t.laterLocked(func() {
		ctx, cancel := context.WithTimeout(t.ctx, callTimeout)
		defer cancel()
		_, err := t.fetchShell(ctx, s)
		t.mu.Lock()
		if t.refreshing == s {
			t.refreshing = nil
		}
		if err != nil && t.live == s {
			t.shellErr = err
			t.shellFails++
			t.shellRetry = time.Now().Add(min(shellMaxAge<<min(t.shellFails, 8), shellMaxBackoff))
		}
		t.mu.Unlock()
		if p != nil {
			p.Send(shellRefreshedMsg{}) // the list, or why there is none
		}
	})
}

// shellWithThread fetches s's listing until it holds threadID, which a
// server may publish a moment after launchThread returns.
func (t *Palette) shellWithThread(ctx context.Context, s Session, threadID string) (t3client.Shell, error) {
	const attempts, pause = 5, 300 * time.Millisecond
	var sh t3client.Shell
	for range attempts {
		var err error
		if sh, err = t.fetchShell(ctx, s); err != nil {
			return sh, err
		}
		for _, th := range sh.Threads {
			if th.ID == threadID {
				return sh, nil
			}
		}
		select {
		case <-ctx.Done():
			return sh, fmt.Errorf("waiting for the thread: %w", ctx.Err())
		case <-time.After(pause):
		}
	}
	return sh, fmt.Errorf("thread %s is not listed yet; open it from the palette once it is", trunc(threadID, 9))
}
