package main

import (
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/ui"
)

// viewSwitch takes the toolbar's View menu from a review's changes to every
// file of its folder and back. The review is built before the Diff View…
// commands that rebuild it, so it holds this switch, and the switch learns
// what to run once they exist (set); it asks at the time of the pick, so it
// acts on whichever review is on screen then.
type viewSwitch struct {
	mu      sync.Mutex
	to      func(inner tea.Model, files bool) tea.Cmd
	choices func(inner tea.Model) tea.Cmd // the Diff View… choices for inner: a command ending in its picker
	send    func(inner tea.Model) tea.Cmd // sends inner's annotations to the agent of the T3 thread on screen
}

// setSend is what the ⋯ menu's Send annotations to agent runs: the T3
// palette's, which knows the thread on screen.
func (v *viewSwitch) setSend(send func(inner tea.Model) tea.Cmd) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.send = send
}

// sendFor is the ⋯ menu's Send annotations to agent for a review of src:
// only a T3 thread's has an agent to send them to. It runs through the
// split, on the review on screen.
func (v *viewSwitch) sendFor(src sourceSetup) func() tea.Cmd {
	if v == nil || src.t3 == nil || src.t3.Target.Thread == nil {
		return nil
	}
	return func() tea.Cmd {
		return func() tea.Msg {
			return splitview.RunMsg{Run: func(inner tea.Model) tea.Cmd {
				v.mu.Lock()
				send := v.send
				v.mu.Unlock()
				if send == nil {
					return nil
				}
				return send(inner)
			}}
		}
	}
}

// setChoices is what lists the Compare dropdown's choices: the Diff View…
// picker of the review on screen.
func (v *viewSwitch) setChoices(choices func(inner tea.Model) tea.Cmd) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.choices = choices
}

// compareFor is the toolbar's Compare dropdown for a review of src run with
// opts, nil where forSource has no switch: there is nothing else to compare.
// The choices are Diff View…'s, as its picker lists them, asked for through
// the split so they are the review on screen's.
func (v *viewSwitch) compareFor(opts options, src sourceSetup) func() tea.Cmd {
	if v.forSource(opts, src) == nil {
		return nil
	}
	return func() tea.Cmd {
		return func() tea.Msg {
			return splitview.RunMsg{Run: func(inner tea.Model) tea.Cmd {
				v.mu.Lock()
				choices := v.choices
				v.mu.Unlock()
				if choices == nil {
					return func() tea.Msg { return ui.CompareMenuMsg{Err: "nothing to compare"} }
				}
				cmd := choices(inner)
				return func() tea.Msg { return pickerMenu(cmd) }
			}}
		}
	}
}

// pickerMenu runs cmd, a Diff View… command, and turns the picker it ends
// in into the Compare dropdown's choices; a picking row does what it does
// in the picker. A command that ends in a message instead, such as "not a
// git repository", gives that as the reason there are no choices.
func pickerMenu(cmd tea.Cmd) ui.CompareMenuMsg {
	var said string
	for _, msg := range runAll(cmd) {
		switch msg := msg.(type) {
		case splitview.ShowPickerMsg:
			items := make([]ui.CompareItem, 0, len(msg.Items))
			for _, it := range msg.Items {
				onPick := msg.OnPick
				items = append(items, ui.CompareItem{Label: it.Label, Detail: it.Detail, Current: it.Current,
					Run: func() tea.Cmd { return onPick(it) }})
			}
			return ui.CompareMenuMsg{Items: items}
		case splitview.FlashMsg:
			if !strings.HasSuffix(msg.Text, "…") { // "listing branches…" says it works, not why it failed
				said = msg.Text
			}
		}
	}
	if said == "" {
		said = "nothing to compare"
	}
	return ui.CompareMenuMsg{Err: said}
}

// runAll runs cmd and the commands of the batches it returns, and gives
// every message they end in.
func runAll(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, runAll(c)...)
	}
	return out
}

// set is what the switch runs: to rebuilds inner as its folder's files, or
// as its changes.
func (v *viewSwitch) set(to func(inner tea.Model, files bool) tea.Cmd) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.to = to
}

// forSource is the toolbar's switch for a review of src run with opts, nil
// for a source that has no changes to go back to or no folder to browse:
// stdin, a compare pair, a plain folder outside git, every file of a
// repository (--all-files). A local review switches through its Diff View…,
// so it has the switch exactly when it has one (hasLocalDiff); the file tree
// Diff View… builds is one too, with a folder to go back from.
func (v *viewSwitch) forSource(opts options, src sourceSetup) func(files bool) tea.Cmd {
	if v == nil || (src.t3 == nil && !hasLocalDiff(opts, src)) {
		return nil
	}
	return func(files bool) tea.Cmd {
		return func() tea.Msg {
			return splitview.RunMsg{Run: func(inner tea.Model) tea.Cmd {
				v.mu.Lock()
				to := v.to
				v.mu.Unlock()
				if to == nil {
					return nil
				}
				return to(inner, files)
			}}
		}
	}
}
