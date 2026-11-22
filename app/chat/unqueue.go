package chat

import (
	"context"
	"fmt"
	"strings"
)

// unqueuerLocked is the backend as one that takes queued messages back, nil
// when it cannot; mu must be held.
func (p *Pane) unqueuerLocked() Unqueuer {
	u, _ := p.backend.(Unqueuer)
	return u
}

// lastQueuedLocked is the newest message the backend holds queued, when the
// input is empty and the backend can take it back; mu must be held.
func (p *Pane) lastQueuedLocked() (Queued, bool) {
	if !p.input.empty() || p.unqueuerLocked() == nil {
		return Queued{}, false
	}
	for i := len(p.conv.Queued) - 1; i >= 0; i-- {
		if q := p.conv.Queued[i]; !q.Steer { // a steer is already the agent's
			return q, true
		}
	}
	return Queued{}, false
}

// unqueueLastLocked takes the newest queued message back into an empty
// input, for the user to edit and send again or to drop by clearing it, and
// reports whether there was one. The backend drops it from the queue first,
// so it is never both queued and in the input; a draft typed meanwhile
// stays, under it. Its images and files stay behind, and the note says so.
// mu must be held.
func (p *Pane) unqueueLastLocked() bool {
	q, ok := p.lastQueuedLocked()
	if !ok {
		return false
	}
	u := p.unqueuerLocked()
	err := p.requestLocked(request{
		run: func(ctx context.Context) error {
			if err := u.Unqueue(ctx, q.ID); err != nil {
				return fmt.Errorf("taking the queued message back failed: %w", err)
			}
			return nil
		},
		keep: func(err error) {
			if err != nil {
				return
			}
			p.restoreLocked(q.Text, nil, nil) // never dropped: ahead of a draft typed meanwhile
			p.note = "the queued message is back in the input: enter queues it again, ctrl+u drops it"
			var left []string
			for _, k := range []struct {
				n    int
				noun string
			}{{q.Images, "image"}, {q.Files, "file"}} {
				if k.n > 0 {
					left = append(left, fmt.Sprintf("%d %s%s", k.n, k.noun, plural(k.n)))
				}
			}
			if len(left) > 0 {
				p.note += "; its " + strings.Join(left, " and ") + " stayed behind"
			}
		},
	})
	if err != nil {
		p.note = err.Error()
	}
	return true
}
