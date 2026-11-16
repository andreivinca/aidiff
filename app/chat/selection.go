package chat

import (
	"context"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

type textPoint struct {
	x, y int
}

// textSelection is a drag over the transcript and the queued messages.
type textSelection struct {
	rows        []string // the frame the drag started on, so streaming output cannot move under the pointer
	start, end  textPoint
	top, bottom int // the rows it may cover: from top up to, not including, bottom
	dragging    bool
}

// selectionMouse starts, extends or ends a drag; a finished one is copied.
func (p *Pane) selectionMouse(event vt.Mouse) {
	p.lock()
	defer p.mu.Unlock()
	if p.stop.turn != "" || p.activity.open {
		return
	}
	m := event.Mouse()
	switch event.(type) {
	case uv.MouseClickEvent:
		p.startSelectionLocked(m)
	case uv.MouseMotionEvent:
		p.dragSelectionLocked(m)
	case uv.MouseReleaseEvent:
		p.dragSelectionLocked(m)
		p.endSelectionLocked()
	}
}

// startSelectionLocked begins a drag on the text of the frame on screen; mu
// must be held.
func (p *Pane) startSelectionLocked(m uv.Mouse) {
	if m.Button != uv.MouseLeft {
		return
	}
	p.selection = textSelection{}
	p.input.deselect() // one selection at a time, so a copy takes the one on screen
	p.gen++
	f := p.frame
	if !f.valid || m.Y < f.layout.headerH || m.Y >= f.layout.textBottom {
		return
	}
	point := textPoint{p.selectionX(m.X), m.Y}
	p.selection = textSelection{rows: f.rows, start: point, end: point, top: f.layout.headerH, bottom: f.layout.textBottom, dragging: true}
}

// dragSelectionLocked moves the end of the drag under the pointer, within
// the text; mu must be held.
func (p *Pane) dragSelectionLocked(m uv.Mouse) {
	s := &p.selection
	if !s.dragging {
		return
	}
	s.end = textPoint{p.selectionX(m.X), max(s.top, min(m.Y, s.bottom-1))}
	p.gen++
}

// endSelectionLocked finishes a drag and copies what it covers; mu must be
// held.
func (p *Pane) endSelectionLocked() {
	s := &p.selection
	if !s.dragging {
		return
	}
	s.dragging = false
	text := s.text(p.width)
	if text == "" {
		p.selection = textSelection{}
		return
	}
	p.copySelectionLocked(text)
}

// selectionX keeps a column inside the text, which starts past the inset.
func (p *Pane) selectionX(x int) int {
	return max(len(inset), min(x, p.width))
}

func (s textSelection) bounds(y, width int) (int, int) {
	a, b := s.start, s.end
	if a.y > b.y || (a.y == b.y && a.x > b.x) {
		a, b = b, a
	}
	if y < a.y || y > b.y {
		return 0, 0
	}
	left, right := len(inset), width
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

// draw lays the selection, from the frame it was made on, over a frame's
// rows and joins them into the pane's screen.
func (s textSelection) draw(rows []string, width int) string {
	if s.rows == nil {
		return strings.Join(rows, "\n")
	}
	out := slices.Clone(rows)
	for y := s.top; y < min(s.bottom, len(out)); y++ {
		row := s.rows[y]
		left, right := s.bounds(y, width)
		if right > left {
			selected := ansi.Strip(ansi.Cut(row, left, right))
			row = ansi.Cut(row, 0, left) + lipgloss.NewStyle().Reverse(true).Render(selected) + ansi.Cut(row, right, width)
		}
		out[y] = row
	}
	return strings.Join(out, "\n")
}

// copySelectionLocked puts text on the clipboard in the background; mu must
// be held.
func (p *Pane) copySelectionLocked(text string) {
	p.goLocked(func() func() {
		ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
		defer cancel()
		err := p.clip.Write(ctx, text)
		return func() {
			p.note = "selection copied"
			if err != nil {
				p.note = "copy: " + err.Error()
			}
		}
	})
}

// MouseCaptured keeps an active drag routed here outside the pane bounds.
func (p *Pane) MouseCaptured() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.selection.dragging
}
