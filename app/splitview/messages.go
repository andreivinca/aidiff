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

// OpenTerminalMsg opens a terminal pane as Open Terminal does, with
// Config.Start, in the workspace on screen when the message arrives. Until
// the window size is known the request waits for it.
type OpenTerminalMsg struct{}

// Command is a palette entry the embedding program adds through
// Config.Commands.
type Command struct {
	Name   string
	Detail string // dim text on the right, if any
	// Badge is a short label drawn in color right after the name, such as
	// "side chat · test"; the filter matches it too
	Badge string
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
	Badge  string // a short label drawn in color after Label, matched by the filter too
	Value  any    // handed back to OnPick untouched
	// Current marks what is in effect now, such as the comparison the
	// review shows; a dropdown built from the items ticks it
	Current bool
	// Group puts the row under a titled rule, after the ungrouped rows; the
	// items of a group are listed together, in order.
	Group string
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

// UpdatePickerItemMsg sets the Badge and Detail of the row labeled Label in
// the picker titled Title, when that picker is still open, for what was
// learned after it opened. Anything else open is left as it is.
type UpdatePickerItemMsg struct {
	Title  string
	Label  string
	Badge  string
	Detail string
}

// ShowPromptMsg asks for one line of text in the palette. Enter hands the
// trimmed text to OnSubmit; an empty line is ignored; esc cancels.
type ShowPromptMsg struct {
	Title       string
	Placeholder string
	OnSubmit    func(string) tea.Cmd
}

// RunCommandMsg runs the palette command whose name ends with Suffix, the
// first one, as picking it in the palette would; Missing is flashed when
// there is none.
type RunCommandMsg struct {
	Suffix  string
	Missing string
}

// RunMsg runs Run with the wrapped model on screen, for a model's command
// that acts on the split's level, such as rebuilding the review in
// another view. Sent by a command of the split or of a wrapped model, it
// runs only while that model is on screen, and its command is the split's,
// scoped to that model (see scopedMsg). Sent from outside the program
// (Program.Send), it runs with whatever model is on screen.
type RunMsg struct {
	Run func(inner tea.Model) tea.Cmd
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

// splitMsg marks the messages addressed to the split itself. A wrapped
// model's command may produce them too (a flash, a picker, a replacement),
// and tagMsg hands every marked message to the split instead of back to the
// model. TestSplitMessagesAreMarked fails for a message type left unmarked.
type splitMsg interface {
	splitMsg()
}

func (FilesChangedMsg) splitMsg()     {}
func (OpenPaneMsg) splitMsg()         {}
func (OpenTerminalMsg) splitMsg()     {}
func (ShowPickerMsg) splitMsg()       {}
func (ShowPromptMsg) splitMsg()       {}
func (UpdatePickerItemMsg) splitMsg() {}
func (RunCommandMsg) splitMsg()       {}
func (RunMsg) splitMsg()              {}
func (FlashMsg) splitMsg()            {}
func (ReplaceInnerMsg) splitMsg()     {}
func (ForceQuitMsg) splitMsg()        {}
func (ConfirmMsg) splitMsg()          {}
func (FilesMsg) splitMsg()            {}
func (ShowSidebarMsg) splitMsg()      {}
func (HideSidebarMsg) splitMsg()      {}
func (SwitchWorkspaceMsg) splitMsg()  {}
