package chat

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// uploadRate is the slowest link a message's files are given time for: a
// send carrying files may take requestTimeout and then as long as its
// files take at this rate. The backend ends a transfer that stops moving
// long before that.
const uploadRate = 64 << 10 // bytes per second

// progressInterval is how often upload progress redraws the pane at most.
const progressInterval = 100 * time.Millisecond

var (
	// errSendCancelled ends an upload the user stopped with esc.
	errSendCancelled = errors.New("the upload was cancelled; the message is back in the input")
	// errSendLeft ends an upload the pane left: it follows another thread,
	// or closed.
	errSendLeft = errors.New("the upload stopped as the chat left its thread; the message is back in the input")
)

// sendTimeout bounds sending a message whose files hold size bytes.
func sendTimeout(size int64) time.Duration {
	return requestTimeout + time.Duration(size/uploadRate)*time.Second
}

// stopSendLocked stops the request in flight, when it is uploading files,
// for cause; its message goes back into the input. mu must be held.
func (p *Pane) stopSendLocked(cause error) bool {
	if p.cancelSend == nil {
		return false
	}
	p.cancelSend(cause)
	p.cancelSend = nil
	return true
}

// uploadProgress is how far the files of the message being sent went. The
// backend reports it from a goroutine of its own, maybe while the pane's
// lock is held by whoever waits on it, so it takes a lock of its own and
// only wakes the split.
type uploadProgress struct {
	mu      sync.Mutex
	id      string // the message being sent; "" when none is
	name    string // the file going up
	sent    int64
	size    int64
	changed bool      // moved since the pane last drew it
	woke    time.Time // the last redraw it asked for
}

// start begins reporting for the message id.
func (u *uploadProgress) start(id string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.id, u.name, u.sent, u.size, u.changed = id, "", 0, 0, true
}

// stop ends reporting for the message id.
func (u *uploadProgress) stop(id string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.id == id {
		u.id, u.changed = "", true
	}
}

// report records how far the file name of message id went, and whether the
// pane should be redrawn for it: not more often than progressInterval,
// except as a file starts or ends.
func (u *uploadProgress) report(id, name string, sent, size int64, now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.id != id {
		return false
	}
	edge := name != u.name || sent == size
	u.name, u.sent, u.size, u.changed = name, sent, size, true
	if !edge && now.Sub(u.woke) < progressInterval {
		return false
	}
	u.woke = now
	return true
}

// moved reports, once, that the progress changed since the last call.
func (u *uploadProgress) moved() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	changed := u.changed
	u.changed = false
	return changed
}

// label is the state of the card of message id while its files go up, ""
// when they do not.
func (u *uploadProgress) label(id string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.id != id || u.name == "" {
		return ""
	}
	return fmt.Sprintf("Uploading %s: %s of %s · esc cancels", tokenName(u.name), sizeText(u.sent), sizeText(u.size))
}

// progressFunc is the Outgoing.Progress of the message id: it records how
// far its files went and wakes the split, never waiting on the pane.
func (p *Pane) progressFunc(id string) func(name string, sent, size int64) {
	return func(name string, sent, size int64) {
		if p.upload.report(id, name, sent, size, time.Now()) {
			p.box.wake()
		}
	}
}
