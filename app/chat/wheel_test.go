package chat

import (
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
)

func TestWheelState_Accelerates(t *testing.T) {
	var w wheelState
	at := time.Unix(1000, 0)
	var steps []int
	for range 12 {
		steps = append(steps, w.step(-1, at))
		at = at.Add(10 * time.Millisecond)
	}
	assert.Equal(t, []int{3, 3, 3, 6, 6, 6, 9, 9, 9, 12, 12, 12}, steps, "a fast run speeds up, up to 12 lines a notch")
	assert.Equal(t, 3, w.step(1, at), "turning back starts over")
	assert.Equal(t, 3, w.step(1, at.Add(time.Second)), "so does a pause")
}

func TestPane_WheelSpeedsUpOnAFastRun(t *testing.T) {
	p, _ := newTestPane(t, 40, 10, longConversation())
	view(p)
	at := time.Unix(1000, 0)
	locked(p, func() {
		p.now = func() time.Time { return at }
	})
	up := uv.MouseWheelEvent{Button: uv.MouseWheelUp}
	p.SendMouse(up)
	locked(p, func() {
		assert.Equal(t, 3, p.scroll, "one notch: three lines")
	})
	for range 5 {
		at = at.Add(10 * time.Millisecond)
		p.SendMouse(up)
		view(p)
	}
	locked(p, func() {
		assert.Equal(t, 3+3+3+6+6+6, p.scroll, "a fast run covers more")
	})
}
