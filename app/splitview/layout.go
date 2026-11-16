package splitview

import tea "github.com/charmbracelet/bubbletea"

// minPaneWidth is the narrowest the wrapped model or the terminal area gets.
const minPaneWidth = 20

// rect is a terminal's place in the terminal area; x is relative to leftW.
type rect struct {
	x, w, h int
}

// leftWidth is the wrapped model's width for the current window and terminals.
func (m Model) leftWidth() int {
	return m.leftWidthFor(len(m.terms))
}

// leftWidthFor is the wrapped model's width with n terminals open.
func (m Model) leftWidthFor(n int) int {
	if n == 0 {
		return m.width
	}
	if m.width < 2*minPaneWidth {
		return max(1, m.width/2)
	}
	termW := int(float64(m.width)*m.termRatio + 0.5)
	return min(max(m.width-termW, minPaneWidth), m.width-minPaneWidth)
}

// termRects lays out the open terminals.
func (m Model) termRects() []rect {
	return m.paneRects(m.leftW, m.normWeights())
}

// paneRects lays len(weights) terminals out side by side right of column
// leftW, full height, with a one-column separator between neighbors. The
// weights are their shares of the area and sum to 1.
func (m Model) paneRects(leftW int, weights []float64) []rect {
	n := len(weights)
	if n == 0 {
		return nil
	}
	total := max(1, m.width-leftW)
	avail := max(n, total-(n-1))
	h := max(1, m.height)

	widths := make([]int, n)
	used := 0
	for i := range n - 1 {
		widths[i] = max(1, int(float64(avail)*weights[i]+0.5))
		used += widths[i]
	}
	widths[n-1] = avail - used
	for widths[n-1] < 1 { // rounding overshot: take from the widest
		widest := 0
		for i := range n - 1 {
			if widths[i] > widths[widest] {
				widest = i
			}
		}
		widths[widest]--
		widths[n-1]++
	}

	rects := make([]rect, n)
	x := 0
	for i, w := range widths {
		rects[i] = rect{x: x, w: w, h: h}
		x += w + 1
	}
	return rects
}

// normWeights returns the terminal shares summing to 1, falling back to equal
// shares when they are missing or out of sync with the terminal list.
func (m Model) normWeights() []float64 {
	n := len(m.terms)
	out := make([]float64, n)
	sum := 0.0
	if len(m.weights) == n {
		for _, w := range m.weights {
			sum += w
		}
	}
	for i := range out {
		if sum > 0 {
			out[i] = m.weights[i] / sum
		} else {
			out[i] = 1 / float64(n)
		}
	}
	return out
}

// grownWeights are the shares with one more terminal at the end: it gets an
// equal share and the others shrink proportionally.
func (m Model) grownWeights() []float64 {
	n := len(m.terms) + 1
	weights := make([]float64, 0, n)
	for _, w := range m.normWeights() {
		weights = append(weights, w*float64(n-1)/float64(n))
	}
	return append(weights, 1/float64(n))
}

// relayout recomputes the split and pushes sizes to the wrapped model and the
// terminals.
func (m Model) relayout() (tea.Model, tea.Cmd) {
	m.leftW = m.leftWidth()
	for i, r := range m.termRects() {
		m.terms[i].Resize(r.w, r.h)
	}
	if m.innerSized && m.leftW == m.innerW && m.height == m.innerH {
		return m, nil // e.g. dragging between terminals: spare the wrapped model a re-render
	}
	m.innerW, m.innerH, m.innerSized = m.leftW, m.height, true
	return m.updateInner(tea.WindowSizeMsg{Width: m.leftW, Height: m.height})
}

// dragTo moves the main divider to column x, the wrapped model's last one.
func (m Model) dragTo(x int) (tea.Model, tea.Cmd) {
	if m.width < 2*minPaneWidth {
		return m, nil
	}
	leftW := min(max(x+1, minPaneWidth), m.width-minPaneWidth)
	if leftW == m.leftW {
		return m, nil
	}
	m.termRatio = float64(m.width-leftW) / float64(m.width)
	return m.relayout()
}

// dragSepTo moves the separator right of terminal k to column x, resizing
// only terminals k and k+1.
func (m Model) dragSepTo(k, x int) (tea.Model, tea.Cmd) {
	rects := m.termRects()
	if k+1 >= len(rects) {
		return m, nil
	}
	a, b := rects[k], rects[k+1]
	combined := a.w + b.w
	if combined < 2*minTermWidth {
		return m, nil
	}
	newA := min(max(x-m.leftW-a.x, minTermWidth), combined-minTermWidth)
	if newA == a.w {
		return m, nil
	}
	// store the new widths as exact shares so the layout reproduces them
	avail := 0
	for _, r := range rects {
		avail += r.w
	}
	m.weights = make([]float64, len(rects))
	for i, r := range rects {
		m.weights[i] = float64(r.w) / float64(avail)
	}
	m.weights[k] = float64(newA) / float64(avail)
	m.weights[k+1] = float64(combined-newA) / float64(avail)
	return m.relayout()
}
