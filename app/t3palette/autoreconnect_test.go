package t3palette

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/splitview"
)

func fastReconnects(t *testing.T) {
	t.Helper()
	prev := reconnectDelays
	reconnectDelays = []time.Duration{time.Millisecond, 2 * time.Millisecond}
	t.Cleanup(func() { reconnectDelays = prev })
}

// runMsg waits for the palette to hand the screen a RunMsg.
func runMsg(t *testing.T, h *harness) splitview.RunMsg {
	t.Helper()
	var got splitview.RunMsg
	require.Eventually(t, func() bool {
		for _, m := range h.sent() {
			if r, ok := m.(splitview.RunMsg); ok {
				got = r
				return true
			}
		}
		return false
	}, 2*time.Second, time.Millisecond)
	return got
}

func TestAutoReconnect_RebuildsTheReviewAfterADrop(t *testing.T) {
	fastReconnects(t)
	a := newFakeSession("ssh://a", testShell())
	a2 := newFakeSession("ssh://a", testShell())
	var tries atomic.Int32
	a.redial = func() (Session, error) {
		if tries.Add(1) < 3 {
			return nil, errors.New("reconnect: server unreachable")
		}
		return a2, nil
	}
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	inner := annotatedModel()

	a.drop()
	reconnected := runMsg(t, h)
	texts := flashes(h.sent())
	assert.Contains(t, texts, "T3: connection to ssh://a lost; reconnecting…")
	assert.Contains(t, texts[len(texts)-1], "still unreachable", "a failed try says so and tries again")
	assert.EqualValues(t, 3, tries.Load(), "until the server answers")

	msgs := run(reconnected.Run(inner))
	rebuilt := find[splitview.SwitchWorkspaceMsg](t, msgs)
	model, ok := rebuilt.Model.(fakeModel)
	require.True(t, ok)
	assert.Same(t, inner.store, model.store, "annotations are kept")
	assert.Same(t, a2, model.review.Session)
	h.swap(rebuilt, inner)
	h.settle()
	assert.Same(t, a2, h.p.liveSession())
}

func TestAutoReconnect_OnlyTheLiveSession(t *testing.T) {
	fastReconnects(t)
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	a := newFakeSession("ssh://a", testShell())
	h.connect(t, "ssh://a", a)
	b := newFakeSession("ssh://b", testShell())
	h.connect(t, "ssh://b", b)
	var tries atomic.Int32
	a.redial = func() (Session, error) { tries.Add(1); return nil, errors.New("down") }

	a.drop() // not the live one
	time.Sleep(20 * time.Millisecond)
	assert.Zero(t, tries.Load(), "a session in the background is left alone")

	b.redial = func() (Session, error) { tries.Add(1); return nil, errors.New("down") }
	b.drop()
	require.Eventually(t, func() bool { return tries.Load() > 0 }, time.Second, time.Millisecond)
	run(func() tea.Msg { h.p.useLocal(); return nil }) // the user picks this machine
	n := tries.Load()
	time.Sleep(20 * time.Millisecond)
	assert.LessOrEqual(t, tries.Load(), n+1, "and the tries stop")
}
