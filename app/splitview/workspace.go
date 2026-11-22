package splitview

import (
	"maps"
	"reflect"
	"slices"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
)

// A workspace is a wrapped model with its panes, their layout and the focus
// among them. One is on screen; named ones the user switched away from are
// parked, their panes still running, until the user switches back. An
// unnamed workspace (the key "") is not kept: switching away closes its
// panes.

// workspace is a parked workspace.
type workspace struct {
	id        int // its wrapped model, as innerMsg names it
	inner     tea.Model
	terms     []Terminal
	weights   []float64
	termRatio float64
	focus     int // focusDiff or an index into terms
}

// SwitchWorkspaceMsg puts the workspace Key on screen. A workspace not seen
// before starts with Model as its wrapped model and no panes; for one that
// exists, on screen already or not, Model replaces its wrapped model when
// Replace is set (as ReplaceInnerMsg does) and is not used otherwise. Done,
// when set, learns what happened, on the UI goroutine, once the workspace is
// on screen.
type SwitchWorkspaceMsg struct {
	Key     string
	Model   tea.Model
	Replace bool
	Done    func(WorkspaceSwitch)
}

// WorkspaceSwitch is what a SwitchWorkspaceMsg did.
type WorkspaceSwitch struct {
	// Left is the wrapped model of the workspace switched away from; nil
	// when Key was on screen already.
	Left tea.Model
	// LeftKept reports that Left's workspace was parked. When false, it was
	// unnamed and closed with its panes.
	LeftKept bool
	// Created reports that the workspace is new, with Model as its wrapped
	// model.
	Created bool
	// Replaced is the wrapped model Model replaced, nil when none.
	Replaced tea.Model
	// Unused reports that Model was not put on screen.
	Unused bool
}

// Workspace is the key of the workspace on screen.
func (m Model) Workspace() string {
	return m.wsKey
}

// switchWorkspace carries out a SwitchWorkspaceMsg: the workspace it
// leaves on screen counts as used (switcher.go). One that lands while the
// switcher is open, such as a project opening that the user asked for
// before, takes over from it.
func (m Model) switchWorkspace(msg SwitchWorkspaceMsg) (tea.Model, tea.Cmd) {
	m.switcher = switcherState{}
	model, cmd := m.switchTo(msg)
	next := model.(Model)
	next.mru = next.openOrder(used(next.mru, next.wsKey))
	return next, cmd
}

// switchTo is switchWorkspace leaving the order of use alone, as the
// switcher does while the user moves through it.
func (m Model) switchTo(msg SwitchWorkspaceMsg) (tea.Model, tea.Cmd) {
	return m.switchAway(msg, false)
}

// switchAway is switchTo; closeLeft closes the workspace left with its
// panes instead of parking it, as the unnamed one always is: its tab was
// closed (tabs.go). A workspace that is new gets a tab at the end; one not
// opened yet (unopened.go) opens with Model, its tab kept in place.
func (m Model) switchAway(msg SwitchWorkspaceMsg, closeLeft bool) (tea.Model, tea.Cmd) {
	if msg.Key == m.wsKey {
		if msg.Model != nil && isUnopened(m.inner) {
			return m.openInPlace(msg)
		}
		if msg.Model == nil || !msg.Replace {
			// the review keeps its state; Model, never initialized, is
			// left to Done, as for a parked workspace
			if msg.Done != nil {
				msg.Done(WorkspaceSwitch{Unused: true})
			}
			return m, nil
		}
		sw := WorkspaceSwitch{Replaced: m.inner}
		return m.replaceInner(ReplaceInnerMsg{Model: msg.Model, Retire: func(tea.Model) {
			if msg.Done != nil {
				msg.Done(sw)
			}
		}})
	}
	next, exists := m.parked[msg.Key]
	if !exists && msg.Model == nil {
		if msg.Done != nil {
			msg.Done(WorkspaceSwitch{Unused: true})
		}
		return m, nil // nothing to show
	}

	sw := WorkspaceSwitch{Left: m.inner}
	m.parked = maps.Clone(m.parked) // never write into a map older copies share
	if m.parked == nil {
		m.parked = map[string]workspace{}
	}
	if m.wsKey == "" || closeLeft || isGone(m.inner) {
		for _, t := range m.terms {
			m.closing.Go(t.Close)
		}
		if isGone(m.inner) { // it could not be opened: its tab goes with it
			m.tabs, m.mru = without(m.tabs, m.wsKey), without(m.mru, m.wsKey)
		}
	} else {
		m.parked[m.wsKey] = m.currentWorkspace()
		sw.LeftKept = true
	}
	delete(m.parked, msg.Key)

	initNew := true
	switch {
	case !exists:
		next = workspace{id: nextWorkspaceID(), inner: msg.Model, focus: focusDiff, termRatio: float64(m.cfg.WidthPercent) / 100}
		sw.Created = true
		m.tabs = opened(m.tabs, msg.Key)
	case msg.Model != nil && isUnopened(next.inner):
		next.inner, next.id = msg.Model, nextWorkspaceID()
		sw.Created = true // its stand-in held nothing: Replaced stays nil
	case msg.Model != nil && msg.Replace:
		sw.Replaced = next.inner
		next.inner = msg.Model
		next.id = nextWorkspaceID()
	default:
		sw.Unused = msg.Model != nil
		initNew = false
	}
	m = m.restoreWorkspace(next)
	m.wsKey = msg.Key
	if msg.Done != nil {
		msg.Done(sw)
	}
	var initCmd tea.Cmd
	if initNew {
		initCmd = m.tag(m.inner.Init())
	}
	model, sizeCmd := m.relayout()
	return model, tea.Batch(initCmd, sizeCmd)
}

// currentWorkspace is the workspace on screen, to park.
func (m Model) currentWorkspace() workspace {
	focus := m.focus
	if focus == focusSidebar || focus == focusOuter {
		focus = m.sideReturn
	}
	if focus < focusDiff {
		focus = focusDiff
	}
	if focus >= len(m.terms) {
		focus = focusDiff
	}
	return workspace{id: m.wsID, inner: m.inner, terms: m.terms, weights: m.weights, termRatio: m.termRatio, focus: focus}
}

// restoreWorkspace puts w on screen. The sidebar keeps focus while it has
// it; a held refresh belonged to the workspace leaving.
func (m Model) restoreWorkspace(w workspace) Model {
	m.wsID, m.inner, m.terms, m.weights, m.termRatio = w.id, w.inner, w.terms, w.weights, w.termRatio
	if m.focus == focusSidebar || m.focus == focusOuter {
		m.sideReturn = w.focus
	} else {
		m.focus = w.focus
	}
	m.dropLeftCache()
	m.innerSized = false
	m.refreshPending = false
	m.flash = ""
	return m
}

// allTerms are the panes of every workspace, the one on screen first.
func (m Model) allTerms() []Terminal {
	all := slices.Clone(m.terms)
	for _, w := range m.parked {
		all = append(all, w.terms...)
	}
	return all
}

// parkedTermUpdate handles an update of a parked pane: one that exited is
// removed from its workspace and closed; any other is waited on again.
func (m Model) parkedTermUpdate(t Terminal) (tea.Model, tea.Cmd) {
	for key, w := range m.parked {
		i := slices.Index(w.terms, t)
		if i < 0 {
			continue
		}
		if !t.Exited() {
			return m, waitForUpdate(t)
		}
		weights := slices.Delete(slices.Clone(normWeights(w.weights, len(w.terms))), i, i+1)
		w.terms = slices.Delete(slices.Clone(w.terms), i, i+1)
		w.weights = normWeights(weights, len(w.terms))
		switch {
		case w.focus == i:
			w.focus = min(i, len(w.terms)-1) // focusDiff with none left
		case w.focus > i:
			w.focus--
		}
		m.parked = maps.Clone(m.parked)
		m.parked[key] = w
		m.closing.Go(t.Close)
		return m, nil
	}
	return m, nil // closed already
}

// A wrapped model's commands may report long after they started: a file
// list loading over SSH, a debounce tick. By then the model may be parked,
// or replaced, and another one on screen, which must not take its replies.
// So every message the wrapped model's commands produce is tagged with the
// model's id and delivered to that model only, parked or not; a model gone
// for good gets nothing. Bubble Tea's own messages (quit, exec, batches and
// sequences, which are unpacked and tagged command by command) pass as they
// are, since the program acts on them itself.

// innerMsg is a message from a wrapped model's command, for model ws.
type innerMsg struct {
	ws  int
	msg tea.Msg
}

// workspaceIDs numbers the wrapped models; ids are never reused.
var workspaceIDs atomic.Int64

// nextWorkspaceID is a wrapped model id never handed out before.
func nextWorkspaceID() int {
	return int(workspaceIDs.Add(1))
}

// teaPackage is Bubble Tea's import path: its messages are not tagged.
var teaPackage = reflect.TypeFor[tea.QuitMsg]().PkgPath()

// cmdType is the element type of Bubble Tea's batch and sequence messages.
var cmdType = reflect.TypeFor[tea.Cmd]()

// tag makes cmd, a command of the wrapped model on screen, report to it.
func (m Model) tag(cmd tea.Cmd) tea.Cmd {
	return tagCmd(m.wsID, cmd)
}

// tagCmd makes cmd report to wrapped model id.
func tagCmd(id int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		return tagMsg(id, cmd())
	}
}

// tagMsg addresses msg to wrapped model id; a batch or sequence has each of
// its commands tagged instead. The split's own messages go to the split,
// scoped to the model (see scopedMsg).
func tagMsg(id int, msg tea.Msg) tea.Msg {
	if msg == nil {
		return nil
	}
	if out, ok := mapBatch(msg, func(c tea.Cmd) tea.Cmd { return tagCmd(id, c) }); ok {
		return out
	}
	t := reflect.TypeOf(msg)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.PkgPath() == teaPackage {
		return msg
	}
	if c, ok := msg.(ConfirmMsg); ok {
		c.Yes, c.No = tagCmd(id, c.Yes), tagCmd(id, c.No) // the popup is the split's, its answer the model's
		msg = c
	}
	if _, ok := msg.(splitMsg); ok {
		return scopeMsg(id, msg)
	}
	return innerMsg{ws: id, msg: msg}
}

// mapBatch applies f to every command of msg when msg is a Bubble Tea batch
// or sequence, and reports whether it is one.
func mapBatch(msg tea.Msg, f func(tea.Cmd) tea.Cmd) (tea.Msg, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != cmdType {
		return nil, false
	}
	out := reflect.MakeSlice(v.Type(), v.Len(), v.Len()) // the same, possibly unexported, type
	for i := range v.Len() {
		c, _ := reflect.TypeAssert[tea.Cmd](v.Index(i))
		out.Index(i).Set(reflect.ValueOf(f(c)))
	}
	return out.Interface(), true
}

// The commands the split runs for a wrapped model (a palette command, a
// picker's pick, a sidebar's key, RunMsg's Run) and the wrapped model's own
// commands may report after the user switched workspace, or after the
// model was replaced. The split messages among their replies that act on
// the model, or ask the user about it, are therefore scoped to it: a
// ReplaceInnerMsg replaces that model, on screen or parked, and a RunMsg,
// RunCommandMsg, picker, prompt, confirmation or file list is dropped once
// the model is off screen, as it would act on, or ask about, another one.
// The other split messages (a flash, a workspace switch, a pane to open) are
// for whatever is on screen when they arrive, as are messages from the
// split's commands to the wrapped model: some, such as the machine a new
// connection reached, are meant for the review on screen.

// scopedMsg is a split message for wrapped model ws.
type scopedMsg struct {
	ws  int
	msg tea.Msg
}

// scoped makes the split messages cmd produces that act on the wrapped
// model on screen stay with it.
func (m Model) scoped(cmd tea.Cmd) tea.Cmd {
	return scopeCmd(m.wsID, cmd)
}

// scopeCmd makes the split messages cmd produces that act on wrapped model
// id stay with it.
func scopeCmd(id int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		return scopeMsg(id, cmd())
	}
}

// scopeMsg scopes msg to wrapped model id when it acts on that model; a
// batch or sequence has each of its commands scoped instead.
func scopeMsg(id int, msg tea.Msg) tea.Msg {
	if out, ok := mapBatch(msg, func(c tea.Cmd) tea.Cmd { return scopeCmd(id, c) }); ok {
		return out
	}
	switch msg.(type) {
	case ReplaceInnerMsg, RunMsg, RunCommandMsg, ShowPickerMsg, ShowPromptMsg, ConfirmMsg, FilesMsg:
		return scopedMsg{ws: id, msg: msg}
	}
	return msg
}

// routeScoped carries out a scoped split message (see scopedMsg).
func (m Model) routeScoped(msg scopedMsg) (tea.Model, tea.Cmd) {
	if r, ok := msg.msg.(ReplaceInnerMsg); ok {
		return m.replaceInnerOf(msg.ws, r)
	}
	if msg.ws != m.wsID {
		if c, ok := msg.msg.(ConfirmMsg); ok {
			return m, c.No // asked unseen: the asker learns there is no answer coming
		}
		return m, nil // its model is off screen
	}
	return m.update(msg.msg)
}

// replaceInnerOf carries out a ReplaceInnerMsg for wrapped model ws, on
// screen or parked. A parked newcomer starts loading in the background and
// gets its size once its workspace is back; Config.Carry is not asked, as
// it carries over what the screen showed. When ws is gone for good, nothing
// is replaced and the newcomer is dropped: it was never initialized, so it
// started nothing.
func (m Model) replaceInnerOf(ws int, msg ReplaceInnerMsg) (tea.Model, tea.Cmd) {
	if ws == m.wsID {
		return m.replaceInner(msg)
	}
	for key, w := range m.parked {
		if w.id != ws || msg.Model == nil {
			continue
		}
		if msg.Retire != nil {
			msg.Retire(w.inner)
		}
		w.inner, w.id = msg.Model, nextWorkspaceID() // the old model's replies are dropped
		m.parked = maps.Clone(m.parked)
		m.parked[key] = w
		return m, tagCmd(w.id, w.inner.Init())
	}
	return m, nil
}

// routeInner delivers a wrapped model's message to that model: on screen,
// or parked, in which case it updates in the background.
func (m Model) routeInner(msg innerMsg) (tea.Model, tea.Cmd) {
	if msg.ws == m.wsID {
		return m.updateInner(msg.msg)
	}
	for key, w := range m.parked {
		if w.id != msg.ws {
			continue
		}
		var cmd tea.Cmd
		w.inner, cmd = w.inner.Update(msg.msg)
		m.parked = maps.Clone(m.parked)
		m.parked[key] = w
		return m, tagCmd(w.id, cmd)
	}
	return m, nil // its model is gone
}
