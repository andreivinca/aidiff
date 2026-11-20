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

// projectsKey shows and hides the sidebar, the T3 projects or this
// machine's history, whose key help names it.
const projectsKey = "ctrl+b"

// newSplit wraps the review in the split view. It always does: ctrl+p can
// open terminals even when none start open, and the T3 commands live there
// too.
func newSplit(opts options, review tea.Model, t3p *t3palette.Palette, local *localDiff, diffs *localDiffs) splitview.Model {
	terminals := terminalLauncher{command: opts.TerminalCommand, place: t3p.TerminalPlace}
	finder := &paletteFinder{project: reviewProject(t3p, local, diffs)}
	return splitview.New(review, splitview.Config{
		WidthPercent: opts.TerminalWidth,
		OpenOnStart:  startsWithTerminal(opts, t3p.OnScreen() != nil),
		Start:        terminals.start,
		RefreshMsg:   ui.RefreshMsg{},
		Commands:     paletteCommands(t3p, local, diffs),
		Files:        finder.files,
		Carry:        finder.carry,
		Quit:         splitview.QuitText{Title: "Quit aidiff?", Idle: "Leaves the review."},
		Keys:         splitview.Keys{Palette: paletteKey, Sidebar: projectsKey},
		Sidebar:      t3p.Sidebar(),
		OuterSidebar: t3p.Connections(),
		Workspace:    t3p.Workspace(),
	})
}

// startsWithTerminal reports whether the split opens a terminal pane at
// once: with --terminal, and for a local folder's review unless
// --no-terminal, as a T3 thread opens with its chat pane.
func startsWithTerminal(opts options, threadOnScreen bool) bool {
	return opts.Terminal || (!opts.NoTerminal && !threadOnScreen)
}

// paletteCommands are the palette's own entries: Diff View… while a local
// review is on screen (the run's own, or a folder's from the history), Copy
// annotations, then the T3 commands.
func paletteCommands(t3p *t3palette.Palette, local *localDiff, diffs *localDiffs) func() []splitview.Command {
	return func() []splitview.Command {
		cmds := append([]splitview.Command{copyAnnotationsCommand(systemClipboard)}, t3p.Commands()...)
		if t3p.OnScreen() != nil {
			return cmds
		}
		l := local
		if dir := t3p.LocalDir(); dir != "" {
			l = diffs.get(dir)
		}
		if l == nil {
			return cmds
		}
		return append([]splitview.Command{l.command()}, cmds...)
	}
}

// switchView is the toolbar's switch between a review's changes and every
// file of its folder: T3's for a T3 review on screen, else the Diff View…
// of the local folder on screen, as paletteCommands picks it.
func switchView(t3p *t3palette.Palette, local *localDiff, diffs *localDiffs) func(inner tea.Model, files bool) tea.Cmd {
	return func(inner tea.Model, files bool) tea.Cmd {
		if t3p.OnScreen() != nil {
			return t3p.ShowView(inner, files)
		}
		l := local
		if dir := t3p.LocalDir(); dir != "" {
			l = diffs.get(dir)
		}
		if l == nil {
			return func() tea.Msg { return splitview.FlashMsg{Text: "this review has no other view"} }
		}
		return l.showView(inner, files)
	}
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
