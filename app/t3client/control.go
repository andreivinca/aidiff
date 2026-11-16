package t3client

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// pingInterval keeps idle connections alive through proxies and tunnels.
const pingInterval = 25 * time.Second

// silenceLimit is how long the server may stay silent after a ping before
// the connection counts as dead: a peer gone with a sleeping laptop or a
// dropped VPN never closes the socket, so silence is the only sign of it.
const silenceLimit = 2 * pingInterval

// controlWriteTimeout bounds writing one control frame once its turn comes.
const controlWriteTimeout = 10 * time.Second

// controlFrame is a frame the client sends on its own: an Ack for a stream
// chunk, an Interrupt for an abandoned request, or a Ping.
type controlFrame struct {
	Tag       string `json:"_tag"`
	RequestID string `json:"requestId,omitempty"`
}

// controlQueue holds control frames for the control writer, so the goroutine
// producing one, often the read loop, never waits on the socket.
type controlQueue struct {
	mu     sync.Mutex
	frames []controlFrame
	ready  chan struct{} // signaled when frames is non-empty
}

func newControlQueue() controlQueue {
	return controlQueue{ready: make(chan struct{}, 1)}
}

// push queues f without blocking.
func (q *controlQueue) push(f controlFrame) {
	q.mu.Lock()
	q.frames = append(q.frames, f)
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

// drain takes every queued frame.
func (q *controlQueue) drain() []controlFrame {
	q.mu.Lock()
	defer q.mu.Unlock()
	frames := q.frames
	q.frames = nil
	return frames
}

// controlLoop writes queued control frames until the connection ends. A
// frame waits for its turn behind any request being written, however large,
// and is then written within controlWriteTimeout; a write that times out
// closes the connection, which is the right end for a wedged peer.
func (c *Conn) controlLoop() {
	for {
		select {
		case <-c.done:
			return
		case <-c.control.ready:
			for _, f := range c.control.drain() {
				// a failed write means the connection is closing, and the
				// read loop reports why; the loop ends with it
				_ = c.writeControl(f)
			}
		}
	}
}

func (c *Conn) writeControl(f controlFrame) error {
	frame, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode %s frame: %w", f.Tag, err)
	}
	ctx, cancel := context.WithTimeout(c.ctx, controlWriteTimeout)
	defer cancel()
	if err := c.write(c.ctx, ctx, frame); err != nil {
		return err
	}
	if f.Tag == "Ping" {
		c.alive.pinged(time.Now())
	}
	return nil
}

// pingLoop pings the server every pingInterval until the connection ends.
func (c *Conn) pingLoop() {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case now := <-t.C:
			c.ping(now)
		}
	}
}

// ping queues a ping, or, when one has gone unanswered for silenceLimit by
// now, ends the connection instead.
func (c *Conn) ping(now time.Time) {
	if silent := c.alive.silence(now); silent >= silenceLimit {
		c.abort(fmt.Errorf("the T3 server stopped answering (silent for %s)", silent.Round(time.Second)))
		return
	}
	c.control.push(controlFrame{Tag: "Ping"})
}

// liveness tracks whether the server answers. Any inbound frame counts as an
// answer, so a busy stream needs no pongs. Silence is measured from the
// oldest ping actually written since the last frame came in, so time spent
// queued behind a long upload is not mistaken for a dead peer.
type liveness struct {
	unanswered atomic.Int64 // unix nanoseconds of that ping; 0 when none is outstanding
}

// heard records an inbound frame.
func (l *liveness) heard() {
	l.unanswered.Store(0)
}

// pinged records a ping written at.
func (l *liveness) pinged(at time.Time) {
	l.unanswered.CompareAndSwap(0, at.UnixNano())
}

// silence is how long the oldest outstanding ping has waited, at now.
func (l *liveness) silence(now time.Time) time.Duration {
	since := l.unanswered.Load()
	if since == 0 {
		return 0
	}
	return now.Sub(time.Unix(0, since))
}
