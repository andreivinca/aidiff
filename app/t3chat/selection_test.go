package t3chat

import (
	"context"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaneDragCopiesText(t *testing.T) {
	p, _ := newTestPane(t, 80, 24, fixture())
	copied := make(chan string, 1)
	p.mu.Lock()
	p.cfg.CopyText = func(_ context.Context, s string) error { copied <- s; return nil }
	p.mu.Unlock()
	rows := strings.Split(view(p), "\n")
	y := -1
	for i, row := range rows {
		if strings.Contains(row, "secret thoughts") {
			continue
		}
		if i > 0 && i < 1+p.bodyH && strings.TrimSpace(ansi.Strip(ansi.Cut(row, 3, 80))) != "" {
			y = i
			break
		}
	}
	require.Positive(t, y)
	want := strings.TrimRight(ansi.Strip(ansi.Cut(rows[y], 3, 70)), " ")
	p.SendMouse(uv.MouseClickEvent{X: 3, Y: y, Button: uv.MouseLeft})
	p.SendMouse(uv.MouseMotionEvent{X: 70, Y: y, Button: uv.MouseLeft})
	assert.True(t, p.MouseCaptured())
	p.SendMouse(uv.MouseReleaseEvent{X: 70, Y: y, Button: uv.MouseLeft})
	assert.False(t, p.MouseCaptured())
	select {
	case got := <-copied:
		assert.Equal(t, want, got)
	case <-time.After(time.Second):
		t.Fatal("selection was not copied")
	}
	p.SendText("\x1b")
	p.mu.Lock()
	assert.Nil(t, p.selection.rows)
	assert.Empty(t, p.stop.runID, "escape clears selection without stopping the agent")
	p.mu.Unlock()
}

func TestSelectionReverseAndUnicode(t *testing.T) {
	s := textSelection{rows: []string{"   café 界!", "   next line"}, top: 0, bottom: 2,
		start: textPoint{7, 1}, end: textPoint{3, 0}}
	assert.Equal(t, "café 界!\nnext", s.text(20))
	s.start, s.end = s.end, s.start
	assert.Equal(t, "café 界!\nnext", s.text(20))
}
