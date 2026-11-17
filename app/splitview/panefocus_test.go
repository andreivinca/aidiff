package splitview

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

// twoPaneInner is a wrapped model with a left and a right pane of its own.
type twoPaneInner struct {
	innerModel
	right bool // the right pane has focus
	busy  bool // typing: its panes keep focus where it is
}

func (p twoPaneInner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := p.innerModel.Update(msg)
	p.innerModel = model.(innerModel)
	return p, cmd
}

func (p twoPaneInner) MovePaneFocus(right bool) (tea.Model, bool) {
	if p.busy || p.right == right {
		return p, false
	}
	p.right = right
	return p, true
}

func (p twoPaneInner) FocusPaneEdge(right bool) tea.Model {
	p.right = right
	return p
}

func TestPaneFocus_WalksTheReviewsOwnPanes(t *testing.T) {
	got := []tea.Msg{}
	hs := &harness{inner: &got}
	cfg := hs.config()
	cfg.OpenOnStart = true
	cfg.Sidebar = &fakeSidebar{available: true}
	hs.m = New(twoPaneInner{innerModel: innerModel{got: &got}}, cfg)
	hs.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	right := func() bool { return hs.m.inner.(twoPaneInner).right }
	left, rightKey := tea.KeyMsg{Type: tea.KeyLeft, Alt: true}, tea.KeyMsg{Type: tea.KeyRight, Alt: true}

	assert.Equal(t, 0, hs.m.focus, "the terminal starts focused")
	hs.send(left)
	assert.Equal(t, focusDiff, hs.m.focus)
	assert.True(t, right(), "coming from the right lands on the review's right pane")
	hs.send(left)
	assert.Equal(t, focusDiff, hs.m.focus, "the review keeps focus")
	assert.False(t, right(), "and moves it to its left pane")
	hs.send(left)
	assert.Equal(t, focusSidebar, hs.m.focus, "past its left pane: the sidebar")

	hs.send(rightKey)
	assert.Equal(t, focusDiff, hs.m.focus)
	assert.False(t, right(), "coming from the left lands on its left pane")
	hs.send(rightKey)
	assert.True(t, right())
	assert.Equal(t, focusDiff, hs.m.focus)
	hs.send(rightKey)
	assert.Equal(t, 0, hs.m.focus, "past its right pane: the terminal")

	hs.send(left)
	hs.send(left)
	assert.False(t, right())
	inner := hs.m.inner.(twoPaneInner)
	inner.busy = true
	hs.m.inner = inner
	hs.send(rightKey)
	assert.Equal(t, 0, hs.m.focus, "a busy review keeps its pane, and focus moves on past it")
}
