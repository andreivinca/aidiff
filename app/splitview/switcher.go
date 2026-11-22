package splitview

import (
	"slices"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
)

// The session switcher (Keys.Switch, ctrl+tab) lists the open workspaces,
// most recently used first, as an OS window switcher lists windows: the one
// on screen first and the one used before it selected, so one press goes
// back to it. Each move puts the selected workspace on screen behind the
// popup. Letting go of the modifier that opened it (ctrl for ctrl+tab, alt
// for alt+s) commits the selection where the terminal reports key releases
// (keyreports.go); enter, a digit, a click or any other key commits it
// everywhere, and esc goes back to where the popup opened. Nothing closes
// it on a timer: holding ctrl keeps it open. The order changes only on
// commit, or the list would reshuffle under the selection while the user
// cycles; every other switch (SwitchWorkspaceMsg) counts as a use too.
//
// The unnamed workspace closes once left, so while it is on screen nothing
// is shown before a choice, and only a deliberate one (enter, a digit, a
// click on a row) leaves it: letting go of ctrl or a stray key must not
// close it.

// noOtherSession is flashed when the switcher has nothing to switch to.
const noOtherSession = "no other session open"

// Sessions describes the workspaces to the switcher and puts them on screen
// the way the embedding program's own switches do (Config.Sessions). The
// split calls it on the UI goroutine.
type Sessions interface {
	// Describe is what the switcher shows of workspace key.
	Describe(key string) SessionInfo
	// Show is the Done of a switch to workspace key that the switcher makes
	// itself, as the program's own SwitchWorkspaceMsg for it would carry it.
	// ready is false when the workspace cannot come back as it was parked,
	// such as one whose connection dropped: the switcher then does not show
	// it while the user moves past it, and asks Open once it is chosen.
	Show(key string) (done func(WorkspaceSwitch), ready bool)
	// Open is the work that puts workspace key on screen once chosen when
	// Show reported it not ready: reconnecting or rebuilding what it must.
	Open(key string) tea.Cmd
	// CloseNote is what closing workspace key, whose wrapped model is
	// model, would take away in the program's own words (annotations not
	// filed yet), "" for nothing: the split asks before closing its tab
	// then.
	CloseNote(key string, model tea.Model) string
	// Closed tells that the user closed workspace key's tab: its panes are
	// closing, model was its wrapped model, and it is not on screen. The
	// program files what the model leaves and forgets the workspace.
	Closed(key string, model tea.Model)
}

// SessionInfo is what a switcher row shows. It may come from the remote
// side; the split cleans it before drawing it.
type SessionInfo struct {
	Name    string // the project: a T3 project's title, a folder's name
	Machine string // where it runs: local, or the server's host
	Path    string // where it lives, to tell two of the same name apart
	Busy    bool   // an agent is working in it
	Note    string // why it is not shown while the user moves past it, such as "disconnected"
}

// switcherState is the session switcher. Its keys are never written in
// place: older copies of the Model share them.
type switcherState struct {
	open   bool
	keys   []string  // the workspaces listed, in the order they had when it opened
	sel    int       // the selected row, an index into keys
	origin string    // the workspace on screen when it opened; esc brings it back
	hold   bool      // origin is unnamed: nothing is shown before a deliberate choice (see above)
	held   uv.KeyMod // the modifiers of the key that opened it, shift aside: letting go of them commits; none from the palette
}

// used is order with key moved to the front, as a new slice.
func used(order []string, key string) []string {
	out := make([]string, 0, len(order)+1)
	out = append(out, key)
	for _, k := range order {
		if k != key {
			out = append(out, k)
		}
	}
	return out
}

// hasWorkspace reports whether workspace key is open: on screen or parked.
func (m Model) hasWorkspace(key string) bool {
	if key == m.wsKey {
		return true
	}
	_, ok := m.parked[key]
	return ok
}

// openOrder is order without the workspaces that closed since.
func (m Model) openOrder(order []string) []string {
	return slices.DeleteFunc(slices.Clone(order), func(k string) bool { return !m.hasWorkspace(k) })
}

// sessionKeys are the open workspaces, most recently used first: the one on
// screen, then the parked ones; those never used yet, such as the ones not
// opened yet, follow in the order of their tabs.
func (m Model) sessionKeys() []string {
	keys := []string{m.wsKey}
	for _, k := range m.mru {
		if _, ok := m.parked[k]; ok && k != m.wsKey && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, k := range m.tabs {
		if _, ok := m.parked[k]; ok && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	var rest []string // parked ones neither order knows: never expected, listed last
	for k := range m.parked {
		if !slices.Contains(keys, k) {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	return append(keys, rest...)
}

// openSwitcher opens the switcher on the session used before the one on
// screen, or on the least recently used one for a negative step; with no
// other session it says so instead. held are the modifiers of the key that
// opened it, whose release commits.
func (m Model) openSwitcher(step int, held uv.KeyMod) (tea.Model, tea.Cmd) {
	keys := m.sessionKeys()
	if len(keys) < 2 {
		m.flash = noOtherSession
		return m, nil
	}
	m.switcher = switcherState{open: true, keys: keys, origin: m.wsKey, hold: m.wsKey == "", held: held &^ uv.ModShift}
	sel := 1
	if step < 0 {
		sel = len(keys) - 1
	}
	return m.selectSession(sel)
}

// selectSession selects row i and puts it on screen when it can come as it
// was left.
func (m Model) selectSession(i int) (tea.Model, tea.Cmd) {
	m.switcher.sel = i
	if m.switcher.hold {
		return m, nil
	}
	return m.previewSession()
}

// moveSwitcher moves the selection by step rows, wrapping around the ends.
func (m Model) moveSwitcher(step int) (tea.Model, tea.Cmd) {
	n := len(m.switcher.keys)
	return m.selectSession(((m.switcher.sel+step)%n + n) % n)
}

// awaitsRelease reports whether the switcher is open and letting go of the
// modifier that opened it would commit: not from the palette, where nothing
// is held, nor on the unnamed workspace, which only a deliberate choice
// leaves.
func (s switcherState) awaitsRelease() bool {
	return s.open && s.held != 0 && !s.hold
}

// selectedSession is the key of the selected row.
func (s switcherState) selectedSession() string {
	return s.keys[s.sel]
}

// showSession is the Done of a switch to key and whether key can come on
// screen as it was parked (Sessions.Show); without Sessions every workspace
// can. A workspace not opened yet never can: Sessions.Open opens it.
func (m Model) showSession(key string) (func(WorkspaceSwitch), bool) {
	if m.cfg.Sessions == nil {
		return nil, true
	}
	done, ready := m.cfg.Sessions.Show(key)
	return done, ready && !isUnopened(m.workspaceModel(key))
}

// previewSession puts the selected session on screen, unless it cannot come
// as it was left; the origin always can, having been on screen a moment ago.
func (m Model) previewSession() (Model, tea.Cmd) {
	key := m.switcher.selectedSession()
	if key == m.wsKey {
		return m, nil
	}
	done, ready := m.showSession(key)
	if !ready && key != m.switcher.origin {
		return m, nil // left on screen as it is; Open brings it once chosen
	}
	model, cmd := m.switchTo(SwitchWorkspaceMsg{Key: key, Done: done})
	return model.(Model), cmd
}

// commitSwitcher closes the switcher on the selected session: on screen
// already when it could be shown, else put there now, or, when it cannot
// come as it was left, opened by Sessions.Open from the origin, which the
// screen goes back to meanwhile. The session on screen counts as used; a
// sidebar, which the user left for another session, hides.
func (m Model) commitSwitcher() (tea.Model, tea.Cmd) {
	s := m.switcher
	m.switcher = switcherState{}
	if !s.open || len(s.keys) == 0 {
		return m, nil
	}
	key := s.selectedSession()
	var cmds []tea.Cmd
	if key != m.wsKey {
		done, ready := m.showSession(key)
		if ready || key == s.origin {
			model, cmd := m.switchTo(SwitchWorkspaceMsg{Key: key, Done: done})
			m, cmds = model.(Model), append(cmds, cmd)
		} else {
			model, cmd := m.backTo(s.origin)
			m, cmds = model.(Model), append(cmds, cmd, m.scoped(m.cfg.Sessions.Open(key)))
		}
	}
	m.mru = m.openOrder(used(m.mru, m.wsKey))
	if m.wsKey != s.origin {
		model, cmd := m.hideSidebar()
		m, cmds = model.(Model), append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// cancelSwitcher closes the switcher on the origin, the order as it was.
func (m Model) cancelSwitcher() (tea.Model, tea.Cmd) {
	origin := m.switcher.origin
	m.switcher = switcherState{}
	return m.backTo(origin)
}

// closeSwitcher closes the switcher for whatever came in its way: on the
// selection, or on the origin while that is unnamed (see hold).
func (m Model) closeSwitcher() (tea.Model, tea.Cmd) {
	if m.switcher.hold {
		return m.cancelSwitcher()
	}
	return m.commitSwitcher()
}

// backTo puts origin, the workspace the switcher opened on, back on screen.
func (m Model) backTo(origin string) (tea.Model, tea.Cmd) {
	if origin == m.wsKey || !m.hasWorkspace(origin) {
		return m, nil
	}
	done, _ := m.showSession(origin) // it was on screen a moment ago
	return m.switchTo(SwitchWorkspaceMsg{Key: origin, Done: done})
}

// syncSwitcher drops the sessions that closed since the switcher opened
// (synced). With fewer than two left there is nothing to switch to, and it
// closes.
func (m Model) syncSwitcher() (Model, tea.Cmd) {
	m.switcher = m.synced()
	if m.switcher.open && len(m.switcher.keys) < 2 {
		model, cmd := m.closeSwitcher()
		return model.(Model), cmd
	}
	return m, nil
}

// synced is the switcher without the sessions that closed since it opened,
// the selection kept on its session, else on the nearest row left. It
// changes nothing else, so View can use it too.
func (m Model) synced() switcherState {
	s := m.switcher
	if !s.open {
		return s
	}
	keep := m.openOrder(s.keys)
	if len(keep) == len(s.keys) {
		return s
	}
	i := slices.Index(keep, s.selectedSession())
	if i < 0 {
		i = min(s.sel, len(keep)-1)
	}
	if !m.hasWorkspace(s.origin) {
		s.origin = m.wsKey // nothing to go back to: esc stays
	}
	s.keys, s.sel = keep, max(0, i)
	s.open = len(keep) > 0
	return s
}

// inSwitcher runs f on the open switcher once the sessions that closed
// since it opened are dropped from it; f does not run when that closed it.
func (m Model) inSwitcher(f func(Model) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	m, syncCmd := m.syncSwitcher()
	if !m.switcher.open {
		return m, syncCmd
	}
	model, cmd := f(m)
	return model, tea.Batch(syncCmd, cmd)
}

// handleSwitcherKey answers a key Bubble Tea named while the switcher is
// open. A key that is not the switcher's commits and goes no further:
// closing a switcher should not also type. ctrl+↑ and ctrl+↓ while ctrl is
// held are ↑ and ↓; a plain ↑ or ↓ then proves ctrl went up unseen
// (letGo), since arrows carry their modifiers in every encoding.
func (m Model) handleSwitcherKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.inSwitcher(func(m Model) (tea.Model, tea.Cmd) {
		if step, ok := m.heldArrow(msg); ok {
			return m.moveSwitcher(step)
		}
		if (msg.Type == tea.KeyUp || msg.Type == tea.KeyDown) && !msg.Alt && m.letGo(0) {
			return m.commitSwitcher()
		}
		return m.switcherKey(msg)
	})
}

// switcherKey answers a key once the list is in sync; a key reported with
// the modifier still held comes here without it (switcherPress).
func (m Model) switcherKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	switch {
	case k == "esc":
		return m.cancelSwitcher()
	case k == "enter":
		return m.commitSwitcher()
	case m.isSwitchKey(k), k == "tab", k == "down", k == "j":
		return m.moveSwitcher(1)
	case m.isSwitchBackKey(k), k == "shift+tab", k == "up", k == "k":
		return m.moveSwitcher(-1)
	}
	if n, ok := digitKey(msg); ok {
		if n > len(m.switcher.keys) {
			return m, nil // no such row: still choosing
		}
		m.switcher.sel = n - 1
		return m.commitSwitcher()
	}
	return m.closeSwitcher()
}

// heldArrow reads ↑ or ↓ pressed with the modifier that opened the
// switcher, still held, as the plain arrow: the step it moves.
func (m Model) heldArrow(msg tea.KeyMsg) (int, bool) {
	held := m.switcher.held
	switch {
	case held&uv.ModCtrl != 0 && msg.Type == tea.KeyCtrlUp,
		held&uv.ModAlt != 0 && msg.Alt && msg.Type == tea.KeyUp:
		return -1, true
	case held&uv.ModCtrl != 0 && msg.Type == tea.KeyCtrlDown,
		held&uv.ModAlt != 0 && msg.Alt && msg.Type == tea.KeyDown:
		return 1, true
	}
	return 0, false
}

// letGo reports whether a key event with modifiers mods proves that the
// modifiers which opened the switcher are up, though their release never
// reached the split: the terminal reports releases, so every event carries
// the modifiers held, and these are missing. That happens on a quick tap,
// when ctrl goes up before the terminal took the switcher's flags.
func (m Model) letGo(mods uv.KeyMod) bool {
	s := m.switcher
	return !s.hold && s.held != 0 && mods&s.held == 0 && m.keys.releases()
}

// switcherKeyReport answers a decoded key report while the switcher is open,
// in the kitty protocol's encoding or the one before it. Letting go of the
// modifier that opened it commits; a release of any other key is ignored,
// so the tab that opened it changes nothing when it comes up. A key pressed
// with the modifier still held is the key without it: ctrl+j is j.
func (m Model) switcherKeyReport(r keyReport) (tea.Model, tea.Cmd) {
	if r.event != keyRelease && !isModifierKey(r.key.Code) {
		m.flash = "" // a key ends it, as in handleKey
	}
	return m.inSwitcher(func(m Model) (tea.Model, tea.Cmd) {
		s := m.switcher
		mod := modifierOf(r.key.Code)
		switch {
		case s.hold && (r.event == keyRelease || isModifierKey(r.key.Code)):
			return m, nil // only a deliberate choice leaves the unnamed workspace
		case mod != 0 && mod&s.held != 0 && r.event == keyRelease:
			return m.commitSwitcher()
		case mod != 0 && mod&s.held != 0:
			return m, nil // pressed again: still held
		case m.letGo(r.key.Mod):
			return m.switcherLetGo(r)
		case r.event == keyRelease, isModifierKey(r.key.Code):
			return m, nil // another key let go, another modifier, a lock
		}
		return m.switcherPress(r.key)
	})
}

// switcherLetGo answers a key report that proves the modifier which opened
// the switcher went up unseen (letGo): the switch it stood for is made, as
// letting go would have made it. Esc still goes back and a digit still picks
// its row, as asked.
func (m Model) switcherLetGo(r keyReport) (tea.Model, tea.Cmd) {
	if legacy, ok := legacyKey(r.key); ok && r.event != keyRelease {
		if _, digit := digitKey(legacy); digit || legacy.Type == tea.KeyEscape {
			return m.switcherKey(legacy)
		}
	}
	return m.commitSwitcher()
}

// switcherPress answers a key pressed (or repeated) while the switcher is
// open: the switch keys move, and any other key is read without the
// modifier still held.
func (m Model) switcherPress(k uv.KeyPressEvent) (tea.Model, tea.Cmd) {
	switch name := uv.Key(k).Keystroke(); {
	case m.isSwitchKey(name):
		return m.moveSwitcher(1)
	case name == m.cfg.Keys.SwitchBack:
		return m.moveSwitcher(-1)
	}
	if legacy, ok := legacyKey(k); ok && (m.isSwitchKey(legacy.String()) || m.isSwitchBackKey(legacy.String())) {
		return m.switcherKey(legacy) // alt+s, alt+S
	}
	k.Mod &^= m.switcher.held
	if legacy, ok := legacyKey(k); ok {
		return m.switcherKey(legacy)
	}
	return m.closeSwitcher()
}

// heldIn are the modifiers named in key notation k, shift aside: ctrl for
// ctrl+tab and ctrl+shift+tab, alt for alt+s.
func heldIn(k string) uv.KeyMod {
	var held uv.KeyMod
	for _, m := range []struct {
		name string
		mod  uv.KeyMod
	}{{"ctrl+", uv.ModCtrl}, {"alt+", uv.ModAlt}, {"super+", uv.ModSuper}, {"hyper+", uv.ModHyper}, {"meta+", uv.ModMeta}} {
		if strings.Contains(k, m.name) {
			held |= m.mod
		}
	}
	return held
}

// heldName names held for the hint: "ctrl", "alt", "ctrl+alt".
func heldName(held uv.KeyMod) string {
	var names []string
	for _, m := range []struct {
		name string
		mod  uv.KeyMod
	}{{"ctrl", uv.ModCtrl}, {"alt", uv.ModAlt}, {"super", uv.ModSuper}, {"hyper", uv.ModHyper}, {"meta", uv.ModMeta}} {
		if held&m.mod != 0 {
			names = append(names, m.name)
		}
	}
	return strings.Join(names, "+")
}

// otherSwitcherKey answers a key that is not the switcher's, such as one
// with no Bubble Tea name: it closes the switcher (closeSwitcher) and goes
// no further.
func (m Model) otherSwitcherKey() (tea.Model, tea.Cmd) {
	return m.inSwitcher(Model.closeSwitcher)
}

// isSwitchKey reports whether k opens the switcher and moves down in it.
func (m Model) isSwitchKey(k string) bool {
	return k == m.cfg.Keys.Switch || k == m.cfg.Keys.SwitchAlt
}

// isSwitchBackKey reports whether k moves up in the switcher.
func (m Model) isSwitchBackKey(k string) bool {
	return k == m.cfg.Keys.SwitchBack || k == shiftedKey(m.cfg.Keys.SwitchAlt)
}

// shiftedKey is key notation k with shift added, as Bubble Tea names it:
// alt+s gives alt+S. "" when k ends in no lower-case letter.
func shiftedKey(k string) string {
	i := strings.LastIndex(k, "+")
	last := []rune(k[i+1:])
	if len(last) != 1 || !unicode.IsLower(last[0]) {
		return ""
	}
	return k[:i+1] + string(unicode.ToUpper(last[0]))
}

// digitKey reports whether msg is a plain 1…9, which commits that row.
func digitKey(msg tea.KeyMsg) (int, bool) {
	if msg.Alt || msg.Paste || msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return 0, false
	}
	r := msg.Runes[0]
	if r < '1' || r > '9' {
		return 0, false
	}
	return int(r - '0'), true
}

// switcherModifiedKey opens the switcher on a decoded modified key that is
// one of its keys, whatever has focus, as long as no popup is open.
// handled is false for any other key.
func (m Model) switcherModifiedKey(k uv.KeyPressEvent) (model tea.Model, cmd tea.Cmd, handled bool) {
	name := uv.Key(k).Keystroke()
	if m.modal() || (!m.isSwitchKey(name) && name != m.cfg.Keys.SwitchBack) {
		return m, nil, false
	}
	m.flash = ""
	step := 1
	if name == m.cfg.Keys.SwitchBack {
		step = -1
	}
	model, cmd = m.openSwitcher(step, k.Mod)
	return model, cmd, true
}

// switcherMouse handles the mouse while the switcher is open: a click on a
// row commits that row, a click elsewhere commits the selection (or, on an
// unnamed origin, stays), the wheel moves the selection.
func (m Model) switcherMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	return m.inSwitcher(func(m Model) (tea.Model, tea.Cmd) {
		switch {
		case msg.Button == tea.MouseButtonWheelUp:
			return m.moveSwitcher(-1)
		case msg.Button == tea.MouseButtonWheelDown:
			return m.moveSwitcher(1)
		case msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft:
			return m, nil
		}
		row, inside := m.switcherBox().hit(m, msg.X, msg.Y)
		switch {
		case row >= 0:
			m.switcher.sel = row
			return m.commitSwitcher()
		case inside: // the title, the border or the hints
			return m, nil
		}
		return m.closeSwitcher()
	})
}

// switchCommand is the palette's row for the switcher, while there is
// another session to switch to.
func (m Model) switchCommand() (row, bool) {
	if len(m.sessionKeys()) < 2 {
		return row{}, false
	}
	run := func(m Model) (tea.Model, tea.Cmd) {
		return m.openSwitcher(1, 0) // nothing held: it waits for enter
	}
	return row{label: "Switch session…", detail: m.cfg.Keys.Switch + " · " + m.cfg.Keys.SwitchAlt, run: run}, true
}
