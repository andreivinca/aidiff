package splitview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

// statusInner is a wrapped model whose last row is a status line.
type statusInner struct {
	innerModel
	h *int // the height it was given
}

func (s statusInner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		*s.h = size.Height
	}
	model, cmd := s.innerModel.Update(msg)
	s.innerModel = model.(innerModel)
	return s, cmd
}

func (s statusInner) View() string {
	return strings.Repeat("body\n", max(0, *s.h-1)) + "own status"
}

func (statusInner) HasStatusLine() bool { return true }

func (statusInner) StatusLine(width int, hints [][2]string) string {
	right := ""
	for _, h := range hints {
		right += " " + h[0] + ":" + h[1]
	}
	return "STATUS " + strings.Repeat("=", width-7-len(right)) + right
}

// hintTerm is a pane that has its shortcuts shown on the status line.
type hintTerm struct {
	*fakeTerm
}

func (hintTerm) StatusHints() [][2]string {
	return [][2]string{{"enter", "send"}}
}

func TestFooter_StatusLineSpansTheWindow(t *testing.T) {
	got, h := []tea.Msg{}, 0
	hs := &harness{inner: &got}
	cfg := hs.config()
	cfg.OpenOnStart = true
	cfg.Sidebar = &fakeSidebar{available: true}
	cfg.SidebarWidth = 20
	hs.m = New(statusInner{innerModel: innerModel{got: &got}, h: &h}, cfg)
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	hs.send(ShowSidebarMsg{})

	assert.Equal(t, 30, h, "the review keeps the full height and draws its own status row")
	assert.Equal(t, 29, hs.term(0).h, "panes end above the status line")
	lines := strings.Split(hs.m.View(), "\n")
	assert.Len(t, lines, 30)
	assert.Equal(t, "STATUS "+strings.Repeat("=", 93), lines[29], "the status line runs under everything")
	assert.NotContains(t, hs.m.View(), "own status", "the review's own status row is left out")
	assert.True(t, strings.HasPrefix(lines[28], "F"+strings.Repeat("s", 19)+"│body"), "the sidebar ends above it too: %q", lines[28])

	hs.m.terms[0] = hintTerm{fakeTerm: hs.term(0)}
	hs.send(tea.KeyMsg{Type: tea.KeyRight, Alt: true}) // sidebar → review
	hs.send(tea.KeyMsg{Type: tea.KeyRight, Alt: true}) // review → the pane
	lines = strings.Split(hs.m.View(), "\n")
	assert.True(t, strings.HasSuffix(lines[29], " enter:send"), "the focused pane's hints sit on the right: %q", lines[29])
	hs.send(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	lines = strings.Split(hs.m.View(), "\n")
	assert.False(t, strings.HasSuffix(lines[29], " enter:send"), "and leave with its focus")

	hs.send(tea.MouseMsg{X: 50, Y: 29, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	assert.Empty(t, hs.m.terms[0].(hintTerm).mice, "a click on the status line goes nowhere")
}
