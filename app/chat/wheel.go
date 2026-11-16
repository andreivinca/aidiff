package chat

import "time"

// Wheel scrolling of the transcript: a notch moves wheelStep lines, and a
// fast run of notches one way speeds up, a step more every wheelRamp
// notches up to wheelMaxStep lines a notch, so a trackpad flick or a quick
// spin covers a long conversation the way a terminal's own scrolling does.
// A pause of wheelBurst or a turn back starts over.
const (
	wheelStep    = 3
	wheelMaxStep = 12
	wheelRamp    = 3
	wheelBurst   = 50 * time.Millisecond
)

// wheelState follows a run of wheel notches.
type wheelState struct {
	last   time.Time // the previous notch
	dir    int       // its direction: -1 up, 1 down
	streak int       // notches in the run so far
}

// step is how many lines the notch at now in direction dir scrolls.
func (w *wheelState) step(dir int, now time.Time) int {
	if dir == w.dir && !w.last.IsZero() && now.Sub(w.last) <= wheelBurst {
		w.streak++
	} else {
		w.streak = 0
	}
	w.last, w.dir = now, dir
	return min(wheelMaxStep, wheelStep*(1+w.streak/wheelRamp))
}
