package splitview

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
)

// minTermWidth is the narrowest a pane may get, when opening another one or
// dragging a separator.
const minTermWidth = 10

// Close stops every pane of every workspace, in parallel, and waits until all
// of them, including those closed earlier in the background, have finished.
// Safe to call more than once.
func (m Model) Close() {
	for _, t := range m.allTerms() {
		m.closing.Go(t.Close)
	}
	m.closing.Wait()
}

// requestPane opens a pane now, or once the window has a size.
func (m Model) requestPane(start StartFunc) (tea.Model, tea.Cmd) {
	if start == nil {
		return m, nil
	}
	if m.winW == 0 {
		m.pending = append(m.pending, start)
		return m, nil
	}
	return m.openPane(start)
}

// openTerminal adds a shell column at the far right and focuses it.
func (m Model) openTerminal() (tea.Model, tea.Cmd) {
	return m.openPane(m.cfg.Start)
}

// openPending opens the panes requested before the window had a size.
func (m Model) openPending() (tea.Model, tea.Cmd) {
	pending := m.pending
	m.pending = nil
	cmds := make([]tea.Cmd, 0, len(pending))
	for _, start := range pending {
		model, cmd := m.openPane(start)
		m = model.(Model)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// openPane adds a pane column at the far right and focuses it. The new pane
// gets an equal share; the others shrink proportionally, keeping the sizes
// the user dragged them to. Nothing changes when there is no room or the pane
// fails to start.
func (m Model) openPane(start StartFunc) (tea.Model, tea.Cmd) {
	if m.winW == 0 || start == nil {
		return m, nil
	}
	weights := m.grownWeights()
	rects := m.paneRects(m.leftWidthFor(len(weights)), weights)
	for _, r := range rects {
		if r.w < minTermWidth {
			m.flash = "No room for another terminal (drag the divider left to make space)"
			return m, nil
		}
	}
	r := rects[len(rects)-1]
	t, err := start(r.w, r.h)
	if err != nil {
		m.flash = "Terminal failed to start: " + err.Error()
		return m, nil
	}
	m.terms = append(slices.Clip(m.terms), t) // never write into a backing array older copies share
	m.weights = weights
	m.focus = len(m.terms) - 1
	model, cmd := m.relayout()
	return model, tea.Batch(cmd, waitForUpdate(t))
}

// closeTerminal stops and removes terminal i; with none left the wrapped
// model gets the full width again.
func (m Model) closeTerminal(i int) (tea.Model, tea.Cmd) {
	t := m.terms[i]
	weights := m.normWeights() // before removing, while it matches m.terms
	m.terms = append(m.terms[:i:i], m.terms[i+1:]...)
	m.weights = slices.Delete(slices.Clone(weights), i, i+1) // the others share its space proportionally
	m.weights = m.normWeights()
	m.closing.Go(t.Close) // may wait up to a second for stubborn processes
	// the focus to give back when a sidebar hides names a pane too
	m.focus, m.sideReturn, m.outerReturn = m.afterClose(m.focus, i), m.afterClose(m.sideReturn, i), m.afterClose(m.outerReturn, i)
	return m.relayout()
}

// afterClose is focus value f once pane i is gone, m.terms already without
// it: a later pane moves down one, the closed one hands over to its right
// neighbor, or the left one when it was last (focusDiff with none left).
// The review and the sidebars, all negative, keep theirs.
func (m Model) afterClose(f, i int) int {
	switch {
	case f == i:
		return min(i, len(m.terms)-1)
	case f > i:
		return f - 1
	}
	return f
}

func (m Model) handleTermUpdate(msg termUpdateMsg) (tea.Model, tea.Cmd) {
	i := slices.Index(m.terms, msg.term)
	if i < 0 {
		return m.parkedTermUpdate(msg.term) // a parked workspace's, or closed already
	}
	if msg.term.Exited() {
		return m.closeTerminal(i)
	}
	return m, waitForUpdate(msg.term)
}

// waitForUpdate waits for t's next update. A closed Updates channel means
// the pane is gone: the wait ends without a message.
func waitForUpdate(t Terminal) tea.Cmd {
	return func() tea.Msg {
		if _, ok := <-t.Updates(); !ok {
			return nil
		}
		return termUpdateMsg{term: t}
	}
}
