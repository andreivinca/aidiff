package t3palette

import (
	"cmp"
	"path"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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
	t := l.t
	t.refreshShell() // the dots follow the listing; an old one is fetched again
	t.mu.Lock()
	defer t.mu.Unlock()
	r, local := t.workspaceLocked(key)
	switch {
	case r != nil:
		return t.describeReviewLocked(r)
	case local != nil:
		return splitview.SessionInfo{
			Name:    display(filepath.Base(local.Dir)),
			Machine: localLabel,
			Path:    display(homeRelative(filepath.Dir(local.Dir))),
		}
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
// or start a rebuild for a workspace the user may only be passing by.
func (l sessionList) Show(key string) (func(splitview.WorkspaceSwitch), bool) {
	t := l.t
	t.mu.Lock()
	defer t.mu.Unlock()
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
// now, the status bar naming its machine.
func (l sessionList) Open(key string) tea.Cmd {
	t := l.t
	t.mu.Lock()
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
