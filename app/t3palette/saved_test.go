package t3palette

import (
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/splitview"
)

// savedFake records what the palette saves.
type savedFake struct {
	mu         sync.Mutex
	remembered []string
	forgotten  []string
}

func (f *savedFake) config(list ...SavedServer) SavedConfig {
	return SavedConfig{
		List: func() []SavedServer { return list },
		Remember: func(spec, _ string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.remembered = append(f.remembered, spec)
		},
		Forget: func(spec string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.forgotten = append(f.forgotten, spec)
			return nil
		},
	}
}

func (f *savedFake) wasRemembered(spec string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.remembered {
		if s == spec {
			return true
		}
	}
	return false
}

func TestSaved_ListedConnectedAndForgotten(t *testing.T) {
	f := &savedFake{}
	h := newHarness(t, Config{NoWatch: true, NoChat: true, LocalName: "andrei-pc",
		Saved: f.config(SavedServer{Spec: "ssh://old", Machine: "oldbox"}, SavedServer{Spec: "local", Machine: "andrei-pc"})}, nil)
	sb := h.p.Connections()
	view := ansi.Strip(sb.View(40, 14, true))
	assert.Contains(t, view, "   ssh://old oldbox", "a server saved before is listed, not connected")
	assert.Contains(t, view, "   local T3 Code andrei-pc")
	assert.Contains(t, view, "x       forget server")

	// local, ssh://old, local T3 Code, connect: Enter on ssh://old connects to it
	old := newFakeSession("ssh://old", testShell())
	h.dials["ssh://old"] = old
	sb.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
	cmd, hide := sb.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, hide)
	find[splitview.ShowSidebarMsg](t, run(cmd))
	assert.Same(t, old, h.p.liveSession(), "connected")
	n := 0
	for _, r := range h.p.connRows() {
		if r.label == "ssh://old" {
			n++
			assert.NotNil(t, r.session, "as connected")
		}
	}
	assert.Equal(t, 1, n, "listed once")

	b := newFakeSession("ssh://b", testShell())
	h.connect(t, "ssh://b", b)
	assert.Eventually(t, func() bool { return f.wasRemembered("ssh://b") }, time.Second, time.Millisecond, "a new server is saved")

	// x on the saved local T3 Code asks first
	rows := h.p.connRows()
	i := len(rows) - 2
	require.Equal(t, localT3Row, rows[i].label)
	sb.(*connectionsSidebar).sel = i
	cmd, hide = sb.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	assert.False(t, hide)
	ask := find[splitview.ConfirmMsg](t, run(cmd))
	assert.Equal(t, "Forget local T3 Code?", ask.Title)
	assert.Equal(t, "Forget", ask.Confirm)
	assert.Contains(t, ansi.Strip(sb.View(40, 14, true)), "local T3 Code", "nothing forgotten before the answer")

	assert.Equal(t, []string{"forgot local T3 Code"}, flashes(run(ask.Yes)))
	assert.Equal(t, []string{"local"}, f.forgotten)
	assert.NotContains(t, ansi.Strip(sb.View(40, 14, true)), "local T3 Code")

	sb.HandleKey(tea.KeyMsg{Type: tea.KeyHome})
	cmd, _ = sb.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	assert.Nil(t, cmd, "this machine cannot be forgotten")
	h.settle()
}
