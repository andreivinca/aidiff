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
	rows        []string  // the frame the drag started on, so streaming output cannot move under the pointer
	meta        []rowMeta // how a copy reads its rows
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
		if m.Button == uv.MouseLeft && p.onHeaderClose(m.X, m.Y) {
			p.closeSelfLocked()
			return
		}
		if m.Button == uv.MouseLeft && p.scroll > 0 && p.frame.valid && m.Y == p.frame.layout.statusY {
			p.scroll, p.selection = 0, textSelection{} // the row says what is below: a click goes there
			p.gen++
			return
		}
		if p.clickStickyLocked(m) {
			return
		}
		p.startSelectionLocked(m)
	case uv.MouseMotionEvent:
		p.dragSelectionLocked(m)
	case uv.MouseReleaseEvent:
		p.dragSelectionLocked(m)
		p.endSelectionLocked()
	}
}

// clickStickyLocked answers a left click on the user's message kept on top
// of the transcript (stickyRows): the transcript scrolls back to it, so it
// shows whole, in its place, with the start of what answers it under it.
// It reports false for a click anywhere else, which may start a drag; mu
// must be held.
func (p *Pane) clickStickyLocked(m uv.Mouse) bool {
	f := p.frame
	pin := f.layout.pin
	if m.Button != uv.MouseLeft || !f.valid || pin.rows == 0 || m.Y < f.layout.headerH || m.Y >= f.layout.headerH+pin.rows {
		return false
	}
	p.selection = textSelection{}
	p.input.deselect()
	p.scroll = pin.scroll
	p.gen++
	return true
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
	p.selection = textSelection{rows: f.rows, meta: f.meta, start: point, end: point, top: f.layout.headerH, bottom: f.layout.textBottom, dragging: true}
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

// text is what a copy of the selection yields: its rows' text without the
// pane's inset, rails and padding, a line wrapped over several rows put
// back into one, as it was before the wrap, and a drawn table as its
// records' cells, its borders left out.
func (s textSelection) text(width int) string {
	var lines []string
	joined := false // the last row taken may be continued
	lastRecord := -1
	for y := s.top; y < s.bottom; y++ {
		left, right := s.bounds(y, width)
		if right <= left {
			joined, lastRecord = false, -1
			continue
		}
		var m rowMeta
		if y < len(s.meta) {
			m = s.meta[y]
		}
		if r := m.table; r != nil {
			if r.cells != "" && r.record != lastRecord {
				lines = append(lines, r.cells)
			}
			joined, lastRecord = false, r.record // a border's is -1
			continue
		}
		lastRecord = -1
		var text string
		if m.end > 0 {
			text = ansi.Strip(ansi.Cut(s.rows[y], max(left, m.start), min(right, m.end)))
		} else {
			text = strings.TrimRight(ansi.Strip(ansi.Cut(s.rows[y], left, right)), " ")
		}
		if m.cont && joined && len(lines) > 0 {
			lines[len(lines)-1] += m.joint + text
		} else {
			lines = append(lines, text)
		}
		joined = m.end > 0
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// follow is a released selection on a new frame, whose text area spans
// rows top to bottom: moved with the rows it selected when they show there
// still, unchanged, the nearest first (a note pushed the transcript up, or
// output scrolled it), so the highlight never hides text the frame it was
// made on did not show. ok is false, and the selection gone, when they do
// not show.
func (s textSelection) follow(rows []string, meta []rowMeta, top, bottom int) (textSelection, bool) {
	lo, hi := min(s.start.y, s.end.y), max(s.start.y, s.end.y)
	for d := 0; d <= len(rows); d++ {
		for _, off := range []int{-d, d} {
			if lo+off < top || hi+off >= bottom || hi >= len(s.rows) {
				continue
			}
			same := true
			for y := lo; y <= hi && same; y++ {
				same = rows[y+off] == s.rows[y]
			}
			if same {
				s.rows, s.meta, s.top, s.bottom = rows, meta, top, bottom
				s.start.y += off
				s.end.y += off
				return s, true
			}
		}
	}
	return textSelection{}, false
}

// draw lays the selection, from the frame it was made on, over a frame's
// rows and joins them into the pane's screen. While the drag goes on, that
// frame stands for the rows it covers, so streamed output cannot move the
// text under the pointer; once released, the selection follows its text on
// each new frame instead (follow).
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
