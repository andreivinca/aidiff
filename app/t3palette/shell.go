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
		t.shell, t.shellAt = sh, time.Now()
	}
	return sh, nil
}

// refreshShell fetches the thread list in the background when the cached
// one is older than shellMaxAge. Close does not wait for it: a fetch that
// lands late is dropped, as it is from any session that is no longer live.
func (t *Palette) refreshShell() {
	t.mu.Lock()
	s, p := t.live, t.program
	stale := s != nil && t.refreshing != s && time.Since(t.shellAt) > shellMaxAge
	if stale {
		t.refreshing = s
	}
	t.mu.Unlock()
	if !stale {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		_, err := t.fetchShell(ctx, s)
		t.mu.Lock()
		if t.refreshing == s {
			t.refreshing = nil
		}
		t.mu.Unlock()
		if err == nil && p != nil {
			p.Send(shellRefreshedMsg{})
		}
	}()
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
