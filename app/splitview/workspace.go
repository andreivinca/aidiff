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
// exists, Model replaces its wrapped model when Replace is set and is not
// used otherwise. With Key already on screen, Model (when set) replaces the
// wrapped model, as ReplaceInnerMsg does. Done, when set, learns what
// happened, on the UI goroutine, once the workspace is on screen.
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

// switchWorkspace carries out a SwitchWorkspaceMsg.
func (m Model) switchWorkspace(msg SwitchWorkspaceMsg) (tea.Model, tea.Cmd) {
	if msg.Key == m.wsKey {
		sw := WorkspaceSwitch{Unused: msg.Model == nil}
		if msg.Model == nil {
			if msg.Done != nil {
				msg.Done(sw)
			}
			return m, nil
		}
		sw.Replaced = m.inner
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
	if m.wsKey == "" {
		for _, t := range m.terms {
			m.closing.Go(t.Close)
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
// its commands tagged instead.
func tagMsg(id int, msg tea.Msg) tea.Msg {
	if msg == nil {
		return nil
	}
	v := reflect.ValueOf(msg)
	if v.Kind() == reflect.Slice && v.Type().Elem() == cmdType {
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len()) // the same, possibly unexported, type
		for i := range v.Len() {
			c, _ := reflect.TypeAssert[tea.Cmd](v.Index(i))
			out.Index(i).Set(reflect.ValueOf(tagCmd(id, c)))
		}
		return out.Interface()
	}
	t := v.Type()
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.PkgPath() == teaPackage {
		return msg
	}
	if _, split := msg.(RunCommandMsg); split {
		return msg // addressed to the split, not back to the model
	}
	return innerMsg{ws: id, msg: msg}
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
