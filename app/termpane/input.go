package termpane

import "sync"

// inputQueue holds bytes bound for the child. Pushing never blocks, however
// slowly the child reads; the writer goroutine takes everything queued so far
// in one go.
type inputQueue struct {
	mu      sync.Mutex
	buf     []byte
	dropped bool          // the pty refused input: nothing is kept any more
	ready   chan struct{} // holds a value while buf may be non-empty
}

func newInputQueue() inputQueue {
	return inputQueue{ready: make(chan struct{}, 1)}
}

// push appends b and wakes the writer.
func (q *inputQueue) push(b []byte) {
	q.mu.Lock()
	if !q.dropped {
		q.buf = append(q.buf, b...)
	}
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default: // the writer is already due to look
	}
}

// take removes and returns everything queued.
func (q *inputQueue) take() []byte {
	q.mu.Lock()
	defer q.mu.Unlock()
	b := q.buf
	q.buf = nil
	return b
}

// discard drops what is queued and everything pushed later.
func (q *inputQueue) discard() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.dropped = true
	q.buf = nil
}
