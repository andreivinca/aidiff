package splitview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

// staticTerm is a pane whose screen does not change between frames: Render
// returns the same string each time, as termpane's frame cache does.
type staticTerm struct {
	*fakeTerm
	frame string
}

func (s *staticTerm) Resize(w, h int) {
	s.fakeTerm.Resize(w, h)
	s.frame = styledScreen(w, h)
}

func (s *staticTerm) Render(bool) string {
	return s.frame
}

// styledScreen is a w by h screen of colored text, as a shell draws one.
func styledScreen(w, h int) string {
	line := "\x1b[32m$\x1b[0m \x1b[1;34m~/src\x1b[0m " + strings.Repeat("x", max(0, w-8))
	return strings.TrimSuffix(strings.Repeat(line+"\n", h), "\n")
}

// styledSidebar draws colored, numbered rows, as the projects panel does.
type styledSidebar struct {
	fakeSidebar
}

func (s *styledSidebar) View(width, height int, _ bool) string {
	rows := make([]string, height)
	for i := range rows {
		rows[i] = "\x1b[38;5;6m " + string(rune('1'+i%9)) + "\x1b[0m project " + strings.Repeat("p", max(0, width-12))
	}
	return strings.Join(rows, "\n")
}

// benchInner is a review with a colored status line under the window.
type benchInner struct {
	innerModel
}

func (benchInner) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return benchInner{}, nil
}

func (benchInner) View() string {
	return strings.Repeat("\x1b[33mdiff\x1b[0m line\n", 60)
}

func (benchInner) HasStatusLine() bool { return true }

func (benchInner) StatusLine(width int, _ [][2]string) string {
	return "\x1b[48;5;208m\x1b[38;5;0m main.go \x1b[1m+3 -1\x1b[22m " + strings.Repeat(" ", max(0, width-20)) + "\x1b[0m"
}

// BenchmarkSplit_ViewUnchanged draws a frame in which nothing changed but
// one pane asked for a redraw: the sidebar, two terminals and the status
// line all render the same strings as the frame before.
func BenchmarkSplit_ViewUnchanged(b *testing.B) {
	sb := &styledSidebar{fakeSidebar{available: true}}
	m := New(benchInner{}, Config{
		Sidebar:      sb,
		SidebarWidth: 30,
		Start: func(w, h int) (Terminal, error) {
			return &staticTerm{fakeTerm: newFakeTerm(w, h), frame: styledScreen(w, h)}, nil
		},
	})
	update := func(msg tea.Msg) {
		model, _ := m.Update(msg)
		m = model.(Model)
	}
	update(tea.WindowSizeMsg{Width: 220, Height: 60})
	update(OpenTerminalMsg{})
	update(OpenTerminalMsg{})
	update(ShowSidebarMsg{})
	if len(m.terms) != 2 || !m.sideOpen {
		b.Fatalf("setup: %d terminals, sidebar open %v", len(m.terms), m.sideOpen)
	}
	_ = m.View()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.View()
	}
}

func TestView_CachedPanesFollowChanges(t *testing.T) {
	sb := &styledSidebar{fakeSidebar{available: true}}
	var term *staticTerm
	m := New(benchInner{}, Config{
		Sidebar: sb,
		Start: func(w, h int) (Terminal, error) {
			term = &staticTerm{fakeTerm: newFakeTerm(w, h), frame: styledScreen(w, h)}
			return term, nil
		},
	})
	update := func(msg tea.Msg) {
		model, _ := m.Update(msg)
		m = model.(Model)
	}
	update(tea.WindowSizeMsg{Width: 120, Height: 20})
	update(OpenTerminalMsg{})
	update(ShowSidebarMsg{})
	_ = m.View()

	term.frame = strings.Replace(term.frame, "~/src", "~/new", 1)
	assert.Contains(t, m.View(), "~/new", "a pane's new frame shows")
	update(tea.WindowSizeMsg{Width: 140, Height: 20})
	for i, l := range strings.Split(m.View(), "\n") {
		assert.Equal(t, 140, ansi.StringWidth(l), "row %d is refitted to the new width", i)
	}
	update(tea.KeyMsg{Type: tea.KeyRight, Alt: true}) // focus leaves the sidebar, which closes
	assert.NotContains(t, m.View(), "project", "a hidden sidebar is not drawn from the cache")
}
