package main

import (
	"io"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3palette"
	"github.com/umputun/revdiff/app/ui"
)

// paletteKey opens the split's command palette. It is set here, not left to
// splitview's default, because chat panes list it among their shortcuts.
const paletteKey = "ctrl+p"

// newSplit wraps the review in the split view. It always does: ctrl+p can
// open terminals even when none start open, and the T3 commands live there
// too.
func newSplit(opts options, review tea.Model, t3p *t3palette.Palette) splitview.Model {
	terminals := terminalLauncher{command: opts.TerminalCommand, place: t3p.TerminalPlace}
	return splitview.New(review, splitview.Config{
		WidthPercent: opts.TerminalWidth,
		OpenOnStart:  opts.Terminal,
		Start:        terminals.start,
		RefreshMsg:   ui.RefreshMsg{},
		Commands:     t3p.Commands,
		Quit:         splitview.QuitText{Title: "Quit aidiff?", Idle: "Leaves the review."},
		Keys:         splitview.Keys{Palette: paletteKey},
	})
}

// splitProgramOptions are the program options the split view needs.
// 120fps (Bubble Tea's cap) halves the wait between a frame being ready and
// it reaching the screen: up to 8ms instead of 16ms per terminal-pane echo.
// Idle ticks are free (the renderer skips an unchanged frame). The quit
// guard asks before a quit closes open panes.
func splitProgramOptions() []tea.ProgramOption {
	return []tea.ProgramOption{tea.WithFPS(120), splitview.QuitGuard()}
}

// runSplit runs the program on out with modified-key reports on, so a chat
// pane can tell shift+enter from enter, then closes the split's panes and
// returns the review it wrapped, for revdiff's finalization.
func runSplit(p *tea.Program, out io.Writer, deadline *exitDeadline) (tea.Model, error) {
	restoreKeys := splitview.ReportModifiedKeys(out)
	final, err := p.Run()
	deadline.arm(shutdownLimit) // the screen is gone: whatever follows must not hang
	restoreKeys()
	return closeSplit(final), err
}

// closeSplit closes the panes of the split the program ended with and
// returns the review it wrapped, for revdiff's finalization.
func closeSplit(final tea.Model) tea.Model {
	split, ok := final.(splitview.Model)
	if !ok {
		return final
	}
	split.Close()
	return split.Inner()
}
