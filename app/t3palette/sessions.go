package t3palette

import (
	"cmp"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/ui"
	"github.com/umputun/revdiff/app/ui/style"
)

// The split's session switcher (ctrl+tab) lists its workspaces. The palette
// says what each one is, and puts it on screen with the Done the sidebars'
// switches carry (switched, switchedLocal), so it stays in line with the
// screen however the user got there. A T3 review whose connection dropped,
// or was replaced by a reconnect, cannot come back as it was parked: it is
// opened only once chosen, reconnecting or rebuilding it on the way.

// Sessions is the split's session switcher's view of the workspaces.
func (t *Palette) Sessions() splitview.Sessions {
	return sessionList{t: t}
}

// sessionList implements splitview.Sessions over the palette's workspaces.
type sessionList struct {
	t *Palette
}

// t3Mark follows the machine of a T3 workspace, as the status bar names a
// T3 review, so a folder and a T3 project of the same folder tell apart.
const t3Mark = " T3"

// workspaceLocked is what workspace key holds: a T3 review, a local folder,
// or neither (the unnamed workspace, or one the palette does not know). mu
// must be held.
func (t *Palette) workspaceLocked(key string) (*Review, *LocalWorkspace) {
	if key == t.ws {
		return t.onScreen, t.local
	}
	if r, ok := t.parked[key]; ok {
		return r, nil
	}
	return nil, t.parkedLocal[key]
}

// Describe names workspace key: a T3 project by its title, its server's
// machine and its folder, with a dot while one of its agents runs (as the
// live server's listing says; another server's is not at hand); a local
// folder by its name, local and its parent folder. Nothing for the unnamed
// workspace, which the split names itself.
func (l sessionList) Describe(key string) splitview.SessionInfo {
	l.t.refreshShell() // the dots follow the listing; an old one is fetched again
	return l.Peek(key)
}

// Peek is Describe from the listing at hand, fetching nothing: the tab
// strip asks on every frame, and its dots follow the listing whenever the
// switcher, the projects panel or a command fetched it.
func (l sessionList) Peek(key string) splitview.SessionInfo {
	t := l.t
	t.mu.Lock()
	defer t.mu.Unlock()
	r, local := t.workspaceLocked(key)
	switch {
	case r != nil:
		return t.describeReviewLocked(r)
	case local != nil:
		return splitview.SessionInfo{
			Name:    display(filepath.Base(local.Dir)),
			Machine: machineLabel(local.Dir),
			Path:    display(homeRelative(filepath.Dir(local.Dir))),
		}
	case t.restored[key] != nil:
		return t.describeRestoredLocked(t.restored[key])
	}
	return splitview.SessionInfo{}
}

// describeReviewLocked is a T3 review's row. The path is the project's
// folder, or the folder it is in when the title already names it. mu must
// be held.
func (t *Palette) describeReviewLocked(r *Review) splitview.SessionInfo {
	p := r.Target.Project
	root := cmp.Or(p.WorkspaceRoot, r.Target.Cwd)
	name := cmp.Or(strings.TrimSpace(p.Title), path.Base(root))
	where := root
	if strings.EqualFold(path.Base(root), name) {
		where = path.Dir(root)
	}
	machine := cmp.Or(r.Session.MachineName(), r.Session.Label())
	busy := t.live != nil && r.Session.Label() == t.live.Label() && projectRunning(t.shell, p)
	return splitview.SessionInfo{
		Name:    display(name),
		Machine: display(machine) + t3Mark,
		Path:    display(where),
		Busy:    busy,
		Note:    t.reviewNoteLocked(r),
	}
}

// reviewNoteLocked says why review r cannot come on screen as it was
// parked, "" when it can. mu must be held.
func (t *Palette) reviewNoteLocked(r *Review) string {
	switch {
	case t.retrying[r.Session]:
		return "reconnecting…"
	case t.stale[r]:
		return "reconnected"
	case ended(r.Session):
		return "disconnected"
	}
	return ""
}

// Show is the Done of a switch to parked workspace key, the one the
// sidebars' switches carry. A T3 review over a connection that dropped, is
// being reconnected, or was replaced, is not ready: it would show errors,
// or start a rebuild for a workspace the user may only be passing by. A
// restored tab is never ready: it opens only through Open.
func (l sessionList) Show(key string) (func(splitview.WorkspaceSwitch), bool) {
	t := l.t
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.restored[key]; ok {
		return t.doneRestored(key), false
	}
	if r, ok := t.parked[key]; ok {
		return t.doneSwitched(key), t.reviewNoteLocked(r) == ""
	}
	if ws, ok := t.parkedLocal[key]; ok {
		return func(sw splitview.WorkspaceSwitch) {
			t.switchedLocal(key, ws, sw)
		}, true
	}
	return nil, true // on screen, or unknown: the split switches as it would without the palette
}

// doneSwitched is the Done of a switch to key's parked T3 review.
func (t *Palette) doneSwitched(key string) func(splitview.WorkspaceSwitch) {
	return func(sw splitview.WorkspaceSwitch) {
		t.switched(key, nil, nil, sw)
	}
}

// Open puts parked workspace key on screen once chosen, when Show said it
// cannot come as it was: a review a reconnect replaced the session of is
// rebuilt over the new one, as openProject does; one whose reconnect runs
// comes now and is rebuilt when the reconnect lands (takeOver); one whose
// connection dropped makes that connection the selected one, which starts
// the reconnect, as selecting it in the connections panel does, and comes
// now, the status bar naming its machine. A restored tab opens now
// (restore.go).
func (l sessionList) Open(key string) tea.Cmd {
	t := l.t
	t.mu.Lock()
	if tab, ok := t.restored[key]; ok {
		t.mu.Unlock()
		return t.openRestored(key, *tab)
	}
	r, parked := t.parked[key]
	stale := parked && t.stale[r]
	retrying := parked && t.retrying[r.Session]
	t.mu.Unlock()
	if !parked {
		done, _ := l.Show(key)
		return switchCmd(key, done)
	}
	show := switchCmd(key, t.doneSwitched(key))
	label := r.Session.Label()
	switch {
	case stale:
		return t.rebuildParked(r)
	case retrying:
		return tea.Batch(show, flash("T3: reconnecting to "+label+"…"))
	case ended(r.Session):
		if !t.useLive(r.Session) {
			return flash("T3: " + label + ": the connection was closed; connect to it again under Connections")
		}
		machine := r.Session.MachineName()
		// in order: the identity goes to the review the switch puts on screen
		return tea.Sequence(show, func() tea.Msg { return ui.MachineIdentityMsg{Name: machine, T3: true} })
	}
	return show
}

// CloseNote says what closing workspace key's tab takes off screen: the
// annotations of its review not filed in history yet. Closing files them
// (Closed), and the run forgets them, so the split asks first.
func (l sessionList) CloseNote(_ string, model tea.Model) string {
	m, ok := model.(annotated)
	if !ok || m.Store().Count() == 0 {
		return ""
	}
	t := l.t
	t.mu.Lock()
	filed := t.filed[m.Store()] == m.Store().FormatOutput()
	t.mu.Unlock()
	if filed {
		return ""
	}
	if n := m.Store().Count(); n > 1 {
		return fmt.Sprintf("Its %d annotations go to history; opening it again starts without them.", n)
	}
	return "Its annotation goes to history; opening it again starts without it."
}

// Closed forgets workspace key, whose tab the user closed, model its
// review: what the review leaves is filed in history, as when it leaves
// the screen (a review closed on screen filed it as the split switched
// away, leaveLocked); its chat panes, which the split closed, are
// forgotten; the annotations it kept for the run go, so opening it again
// starts a workspace without them; and the sessions nothing uses any more
// close.
func (l sessionList) Closed(key string, model tea.Model) {
	t := l.t
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || key == t.ws {
		return // still on screen: the split closes only a workspace it left
	}
	var notes []note
	if r, ok := t.parked[key]; ok {
		notes = t.unfiledLocked(model, noteOf(r, "", model, nil))
		delete(t.stale, r)
	} else if ws, ok := t.parkedLocal[key]; ok {
		notes = t.unfiledLocked(model, noteOf(nil, ws.Dir, model, nil))
	}
	delete(t.parked, key)
	delete(t.parkedLocal, key)
	delete(t.restored, key) // closed before it was opened
	t.dropChatsLocked(key)
	if m, ok := model.(annotated); ok {
		t.forgetStoreLocked(m.Store())
	}
	unused := t.takeUnusedLocked()
	t.laterLocked(func() {
		closeAll(unused)
		t.saveNotes(notes)
	})
}

// forgetStoreLocked drops store from what the run keeps: the reviews it
// held the annotations of, and what it filed. mu must be held.
func (t *Palette) forgetStoreLocked(store *annotation.Store) {
	maps.DeleteFunc(t.stores, func(_ string, s *annotation.Store) bool { return s == store })
	delete(t.filed, store)
}

// switchCmd puts workspace key on screen with done as its Done.
func switchCmd(key string, done func(splitview.WorkspaceSwitch)) tea.Cmd {
	return func() tea.Msg {
		return splitview.SwitchWorkspaceMsg{Key: key, Done: done}
	}
}

// display is s fit for one line of the switcher: control characters and
// invisible formatting taken out.
func display(s string) string {
	return style.SanitizeFilenameForDisplay(s)
}
