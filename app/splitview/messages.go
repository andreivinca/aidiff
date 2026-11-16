package splitview

import tea "github.com/charmbracelet/bubbletea"

// FilesChangedMsg tells the split that files changed on disk. It forwards
// Config.RefreshMsg to the wrapped model, holding it while the model reports
// (through an optional CanRefresh() bool method) that it is busy.
type FilesChangedMsg struct{}

// OpenPaneMsg adds a pane to the right column the way Open Terminal adds a
// shell. Start receives the pane's content size. Until the window size is
// known the request waits for it.
type OpenPaneMsg struct {
	Start StartFunc
}

// Command is a palette entry the embedding program adds through
// Config.Commands.
type Command struct {
	Name   string
	Detail string // dim text on the right, if any
	// Group puts the command in a titled section below the ungrouped ones;
	// commands of a group are listed together, groups in order of first use.
	Group string
	// Run starts the command. It receives the wrapped model, so a command can
	// read the review it acts on, and returns the work to do, if any.
	Run func(inner tea.Model) tea.Cmd
}

// PickerItem is one row of a ShowPickerMsg list.
type PickerItem struct {
	Label  string // what the user filters on and picks
	Detail string // dim text on the right, also matched by the filter
	Value  any    // handed back to OnPick untouched
}

// ShowPickerMsg opens a filterable list in the palette. Enter hands the
// selected row to OnPick. With OnText set, typed text that is not a row's
// label is offered as a final row, labeled by TextLabel (a format with one
// %s; "%s" when empty), and Enter on it hands the text to OnText.
type ShowPickerMsg struct {
	Title     string
	Items     []PickerItem
	OnPick    func(PickerItem) tea.Cmd
	OnText    func(string) tea.Cmd
	TextLabel string
}

// ShowPromptMsg asks for one line of text in the palette. Enter hands the
// trimmed text to OnSubmit; an empty line is ignored; esc cancels.
type ShowPromptMsg struct {
	Title       string
	Placeholder string
	OnSubmit    func(string) tea.Cmd
}

// FlashMsg shows a one-line message on the bottom row until the next key.
type FlashMsg struct {
	Text string
}

// ReplaceInnerMsg swaps the wrapped model for another one, which is
// initialized and sized like the first was. Retire, when set, receives the
// model being replaced first, e.g. to keep its annotations.
type ReplaceInnerMsg struct {
	Model  tea.Model
	Retire func(old tea.Model)
}

// termUpdateMsg reports that a pane's screen changed or its program ended.
type termUpdateMsg struct {
	term Terminal
}

// quitRequestMsg stands in for a tea.QuitMsg the user has not confirmed yet
// (see QuitGuard).
type quitRequestMsg struct{}

// ForceQuitMsg quits without the quit dialog, closing every open pane. It is
// for quits nobody is there to confirm, such as a hangup: the terminal is
// gone, so a dialog would never be answered and the program would run on
// unseen.
type ForceQuitMsg struct{}
