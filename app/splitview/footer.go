package splitview

import "slices"

// A wrapped model whose last row is a status line (statusLiner) has it drawn
// across the whole window, under the sidebars and the panes, instead of in
// its own column. The model still gets the full height and draws the row
// itself; the split leaves that row out of the column and draws the
// model's line at the window's width in its place. Everything else ends one
// row higher.

// statusLiner is a wrapped model with a status line as its last row.
type statusLiner interface {
	// HasStatusLine reports whether the model draws a status line at all.
	HasStatusLine() bool
	// StatusLine is the status line rendered width columns wide, with
	// hints, the focused pane's shortcuts as key and label pairs, on the
	// right.
	StatusLine(width int, hints [][2]string) string
}

// statusHinter is a pane that has its shortcuts shown on the status line
// while it has focus, instead of drawing them itself.
type statusHinter interface {
	StatusHints() [][2]string
}

// footerRows is 1 while the wrapped model has a status line the split draws
// across the window, else 0.
func (m Model) footerRows() int {
	if s, ok := m.inner.(statusLiner); ok && s.HasStatusLine() && m.height > 1 {
		return 1
	}
	return 0
}

// bodyH is the height of the columns: the window's, less the status line.
func (m Model) bodyH() int {
	return m.height - m.footerRows()
}

// footerHints are the focused pane's shortcuts for the status line, none
// while the review, a sidebar or a popup has focus.
func (m Model) footerHints() [][2]string {
	if m.focus < 0 || m.focus >= len(m.terms) || m.palette.open || m.quitDialog {
		return nil
	}
	if h, ok := m.terms[m.focus].(statusHinter); ok {
		return h.StatusHints()
	}
	return nil
}

// footerLine is the wrapped model's status line at the window's width, with
// the focused pane's hints, cached with its view while the hints stay the
// same.
func (m Model) footerLine() string {
	c := m.leftCache
	hints := m.footerHints()
	if !c.footerValid || c.footerW != m.winW || !slices.Equal(c.footerHints, hints) {
		s, _ := m.inner.(statusLiner)
		c.footer = fitLine(s.StatusLine(m.winW, hints), m.winW)
		c.footerValid, c.footerW, c.footerHints = true, m.winW, hints
	}
	return c.footer
}
