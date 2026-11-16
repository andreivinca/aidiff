package chat

import (
	"context"
	"fmt"
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
	if !p.input.empty() || len(p.conv.Queued) == 0 || p.unqueuerLocked() == nil {
		return Queued{}, false
	}
	return p.conv.Queued[len(p.conv.Queued)-1], true
}

// unqueueLastLocked takes the newest queued message back into an empty
// input, for the user to edit and send again or to drop by clearing it, and
// reports whether there was one. The backend drops it from the queue first,
// so it is never both queued and in the input; its images stay behind, and
// the note says so. mu must be held.
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
		finish: func(err error) {
			if err != nil {
				return
			}
			if !p.input.empty() {
				p.note = "the queued message was dropped; the input already held a draft: " + firstLine(q.Text)
				return
			}
			p.input.insert(q.Text)
			p.inputChanged()
			p.note = "the queued message is back in the input: enter queues it again, ctrl+u drops it"
			if q.Images > 0 {
				p.note += fmt.Sprintf("; its %d image%s stayed behind", q.Images, plural(q.Images))
			}
		},
	})
	if err != nil {
		p.note = err.Error()
	}
	return true
}
