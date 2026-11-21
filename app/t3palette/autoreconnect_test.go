package t3palette

import (
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
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

func TestAutoReconnect_ParkedReviewsAndChatsMoveToTheNewSession(t *testing.T) {
	a := newFakeSession("ssh://a", twoProjectShell())
	a2 := newFakeSession("ssh://a", twoProjectShell())
	a.redial = func() (Session, error) { return a2, nil }
	start := threadReview(t, a, loginID)
	startStore := annotatedModel().store
	h := newHarness(t, Config{NoWatch: true, NoChat: true, StartStore: startStore}, start)
	_, err := h.p.fetchShell(t.Context(), a)
	require.NoError(t, err)
	sb := h.p.Sidebar()
	var inner tea.Model = fakeModel{store: startStore, review: start}
	inner = h.swap(pick(t, sb, 1), inner) // Alpha on screen, Sabra2 parked, both over a
	parkedChat := &fakeChatPane{}
	h.p.mu.Lock()
	h.p.chats = append(h.p.chats, &openChat{pane: parkedChat, ws: projectKey(a, "p1"), session: a,
		th: t3client.Thread{ID: loginID, Title: "Fix login"}})
	h.p.mu.Unlock()

	rebuilt := find[splitview.SwitchWorkspaceMsg](t, h.reconnect(t, a, inner))
	assert.Equal(t, []string{"Fix login"}, parkedChat.follows, "the parked workspace's chat moves to the new connection at once")
	inner = h.swap(rebuilt, inner)
	h.settle()
	assert.Equal(t, 1, a.closes(), "no parked review holds the dropped session open")

	back := pick(t, sb, 2) // Sabra2
	require.NotNil(t, back.Model, "the parked review comes back rebuilt, not over the dropped session")
	assert.True(t, back.Replace)
	assert.Same(t, a2, shown(back).review.Session)
	assert.Equal(t, loginID, shown(back).review.thread().ID)
	assert.Same(t, startStore, shown(back).store, "with its annotations")
	filed := len(h.history())
	h.swap(back, inner)
	h.settle()
	assert.Same(t, a2, h.p.OnScreen().Session)
	assert.Len(t, h.history(), filed, "the annotations moved with it, not to history")
	h.p.mu.Lock()
	assert.Empty(t, h.p.stale, "nothing left to rebuild")
	h.p.mu.Unlock()
}

func TestAutoReconnect_StaleWorkspaceBackWithoutAModelIsRebuilt(t *testing.T) {
	a := newFakeSession("ssh://a", twoProjectShell())
	a2 := newFakeSession("ssh://a", twoProjectShell())
	a.redial = func() (Session, error) { return a2, nil }
	start := threadReview(t, a, loginID)
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, start)
	_, err := h.p.fetchShell(t.Context(), a)
	require.NoError(t, err)
	var inner tea.Model = fakeModel{store: annotation.NewStore(), review: start}
	inner = h.swap(pick(t, h.p.Sidebar(), 1), inner)
	inner = h.swap(find[splitview.SwitchWorkspaceMsg](t, h.reconnect(t, a, inner)), inner)

	key := projectKey(a, "p1") // back some other way than the projects sidebar
	inner = h.swap(splitview.SwitchWorkspaceMsg{Key: key, Done: func(sw splitview.WorkspaceSwitch) {
		h.p.switched(key, nil, nil, sw)
	}}, inner)
	h.settle()
	rebuilt := find[splitview.SwitchWorkspaceMsg](t, run(runMsg(t, h).Run(inner)))
	assert.Same(t, a2, shown(rebuilt).review.Session, "a rebuild follows at once")
	h.swap(rebuilt, inner)
	h.settle()
	assert.Same(t, a2, h.p.OnScreen().Session)
	assert.Equal(t, 1, a.closes())
}

func TestAutoReconnect_SelectingADroppedConnectionReconnects(t *testing.T) {
	prev := reconnectDelays
	reconnectDelays = []time.Duration{20 * time.Millisecond}
	t.Cleanup(func() { reconnectDelays = prev })
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	a := newFakeSession("ssh://a", testShell())
	h.connect(t, "ssh://a", a)
	h.connect(t, "ssh://b", newFakeSession("ssh://b", testShell()))
	a2 := newFakeSession("ssh://a", testShell())
	var tries atomic.Int32
	a.redial = func() (Session, error) { tries.Add(1); return a2, nil }

	a.drop() // while b is the live one: left alone
	time.Sleep(40 * time.Millisecond)
	assert.Zero(t, tries.Load())

	require.True(t, h.p.useLive(a))
	require.True(t, h.p.useLive(a), "selected twice")
	reconnected := runMsg(t, h)
	assert.EqualValues(t, 1, tries.Load(), "one reconnect")
	run(reconnected.Run(nil))
	h.settle()
	assert.Same(t, a2, h.p.liveSession(), "the dropped connection is back, not live and dead")
	assert.Equal(t, 1, a.closes())
}

func TestAutoReconnect_GivesUpAndRestartsOnSelection(t *testing.T) {
	fastReconnects(t)
	prev := reconnectFor
	reconnectFor = 0
	t.Cleanup(func() { reconnectFor = prev })
	a := newFakeSession("ssh://a", testShell())
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	a.drop()
	require.Eventually(t, func() bool {
		return slices.Contains(flashes(h.sent()), "T3: gave up reconnecting to ssh://a; select it under Connections to try again")
	}, 2*time.Second, time.Millisecond)

	reconnectFor = time.Hour
	a2 := newFakeSession("ssh://a", testShell())
	a.redial = func() (Session, error) { return a2, nil }
	require.True(t, h.p.useLive(a), "selecting it again")
	run(runMsg(t, h).Run(annotatedModel()))
	assert.Same(t, a2, h.p.liveSession())
}

func TestAutoReconnect_CloseEndsTheWait(t *testing.T) {
	prev := reconnectDelays
	reconnectDelays = []time.Duration{time.Hour}
	t.Cleanup(func() { reconnectDelays = prev })
	a := newFakeSession("ssh://a", testShell())
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	a.drop()
	require.Eventually(t, func() bool { return len(flashes(h.sent())) > 0 }, 2*time.Second, time.Millisecond)
	h.p.Close()
	done := make(chan struct{})
	go func() {
		h.p.loops.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the reconnect still waits after Close")
	}
}

func TestAutoReconnect_ASessionReachedAfterCloseIsClosed(t *testing.T) {
	fastReconnects(t)
	prev := loopsWait
	loopsWait = 50 * time.Millisecond
	t.Cleanup(func() { loopsWait = prev })
	a := newFakeSession("ssh://a", testShell())
	a2 := newFakeSession("ssh://a", testShell())
	dialing, release := make(chan struct{}), make(chan struct{})
	a.redial = func() (Session, error) { // a dial that does not heed the cancel
		close(dialing)
		<-release
		return a2, nil
	}
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	a.drop()
	select {
	case <-dialing:
	case <-time.After(2 * time.Second):
		t.Fatal("no reconnect")
	}
	began := time.Now()
	h.p.Close()
	assert.Less(t, time.Since(began), time.Second, "a hung dial does not hold up Close")
	close(release)
	assert.Eventually(t, func() bool { return a2.closes() == 1 }, 2*time.Second, time.Millisecond,
		"the session it reached is closed, though nobody takes it over")
	for _, m := range h.sent() {
		_, isRun := m.(splitview.RunMsg)
		assert.False(t, isRun, "and not handed to a program that is gone")
	}
}

func TestAutoReconnect_HandedOffButNotTakenOverIsClosedByClose(t *testing.T) {
	fastReconnects(t)
	a := newFakeSession("ssh://a", testShell())
	a2 := newFakeSession("ssh://a", testShell())
	a.redial = func() (Session, error) { return a2, nil }
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	a.drop()
	reconnected := runMsg(t, h) // the program ends before running it
	h.p.Close()
	assert.Equal(t, 1, a2.closes())
	run(reconnected.Run(annotatedModel()))
	assert.Equal(t, 1, a2.closes(), "not closed twice when it lands late")
}
