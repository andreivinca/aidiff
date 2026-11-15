package t3chat

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/umputun/revdiff/app/clipboard"
)

type textPoint struct{ x, y int }
type textSelection struct {
	rows        []string // snapshot keeps streaming output from moving under the pointer
	start, end  textPoint
	top, bottom int
	dragging    bool
}

func (p *Pane) selectionMouse(event vt.Mouse) bool {
	if _, wheel := event.(uv.MouseWheelEvent); wheel {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stop.runID != "" || p.activity.open {
		return true
	}
	m := event.Mouse()
	switch event.(type) {
	case uv.MouseClickEvent:
		if m.Button != uv.MouseLeft {
			return true
		}
		p.selection = textSelection{}
		p.gen++
		rows := strings.Split(p.renderLocked(false), "\n")
		if m.Y < 1 || m.Y >= p.textBottom {
			return true
		}
		point := textPoint{max(3, min(m.X, p.width)), m.Y}
		p.selection = textSelection{rows: rows, start: point, end: point, top: 1, bottom: p.textBottom, dragging: true}
	case uv.MouseMotionEvent, uv.MouseReleaseEvent:
		if !p.selection.dragging {
			return true
		}
		s := &p.selection
		s.end = textPoint{max(3, min(m.X, p.width)), max(s.top, min(m.Y, s.bottom-1))}
		p.gen++
		if _, released := event.(uv.MouseReleaseEvent); released {
			s.dragging = false
			text := s.text(p.width)
			if text != "" {
				p.copySelectionLocked(text)
			} else {
				p.selection = textSelection{}
			}
		}
	}
	return true
}

func (s textSelection) bounds(y, width int) (int, int) {
	a, b := s.start, s.end
	if a.y > b.y || (a.y == b.y && a.x > b.x) {
		a, b = b, a
	}
	if y < a.y || y > b.y {
		return 0, 0
	}
	left, right := 3, width
	if y == a.y {
		left = a.x
	}
	if y == b.y {
		right = b.x
	}
	return left, right
}

func (s textSelection) text(width int) string {
	var lines []string
	for y := s.top; y < s.bottom; y++ {
		left, right := s.bounds(y, width)
		if right > left {
			lines = append(lines, strings.TrimRight(ansi.Strip(ansi.Cut(s.rows[y], left, right)), " "))
		}
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

func (p *Pane) selectionRenderLocked(out string) string {
	s := p.selection
	if s.rows == nil {
		return out
	}
	rows := strings.Split(out, "\n")
	for y := s.top; y < min(s.bottom, len(rows)); y++ {
		row := s.rows[y]
		left, right := s.bounds(y, p.width)
		if right > left {
			selected := ansi.Strip(ansi.Cut(row, left, right))
			row = ansi.Cut(row, 0, left) + lipgloss.NewStyle().Reverse(true).Render(selected) + ansi.Cut(row, right, p.width)
		}
		rows[y] = row
	}
	return strings.Join(rows, "\n")
}

func (p *Pane) copySelectionLocked(text string) {
	copyText := p.cfg.CopyText
	if copyText == nil {
		copyText = clipboard.Write
	}
	subID := p.subID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := copyText(ctx, text)
		p.mu.Lock()
		if p.subID == subID {
			p.note = "selection copied"
			if err != nil {
				p.note = "copy: " + err.Error()
			}
			p.gen++
		}
		p.mu.Unlock()
		p.signal()
	}()
}

// MouseCaptured keeps an active drag routed here outside the pane bounds.
func (p *Pane) MouseCaptured() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.selection.dragging
}
