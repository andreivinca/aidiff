package t3palette

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/ui"
)

// connectPickerTitle titles the connect picker, which status updates name.
const connectPickerTitle = "Connect to"

// connectPicker offers the servers aidiff knows of: the local T3 Code and
// servers with a saved login, each marked online or offline once a probe
// answers, then, apart, the hosts of ~/.ssh/config, which are only
// candidates: a T3 Code reached over SSH must run there. Anything else is
// typed, a `t3 pair` link included, which pairs with its server.
func (t *Palette) connectPicker(tea.Model) tea.Cmd {
	items := serverItems(t.cfg.Store)
	cmds := []tea.Cmd{func() tea.Msg {
		return splitview.ShowPickerMsg{
			Title: connectPickerTitle,
			Items: items,
			OnPick: func(it splitview.PickerItem) tea.Cmd {
				return t.connect(it.Label)
			},
			OnText: func(s string) tea.Cmd {
				if spec, ok := t3client.PairingLink(s); ok {
					return t.pairWith(spec, strings.TrimSpace(s))
				}
				return t.connect(serverSpec(s))
			},
			TextLabel: "Connect to %s",
		}
	}}
	return tea.Batch(append(cmds, t.probeItems(items)...)...)
}

// probeTimeout bounds one server's probe; an ssh:// one costs a login.
const probeTimeout = 10 * time.Second

// probeItems checks each T3 server of the connect picker, a command apiece,
// so each row is marked as its answer comes. A server the palette is
// connected to is online without asking.
func (t *Palette) probeItems(items []splitview.PickerItem) []tea.Cmd {
	if t.probe == nil {
		return nil
	}
	t.mu.Lock()
	live := map[string]bool{}
	for s := range t.connected {
		live[s.Label()] = true
	}
	ctx := t.ctx
	t.mu.Unlock()
	var cmds []tea.Cmd
	for _, it := range items {
		if it.Group != "" {
			continue // an ssh host, not known to run T3 Code
		}
		if live[it.Label] {
			cmds = append(cmds, func() tea.Msg { return probedMsg(it, nil) })
			continue
		}
		cmds = append(cmds, func() tea.Msg {
			pctx, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			return probedMsg(it, t.probe(pctx, it.Label))
		})
	}
	return cmds
}

// probedMsg marks it online, or offline when err says it did not answer.
func probedMsg(it splitview.PickerItem, err error) splitview.UpdatePickerItemMsg {
	if err != nil {
		return splitview.UpdatePickerItemMsg{Title: connectPickerTitle, Label: it.Label, Detail: "offline · " + it.Detail}
	}
	return splitview.UpdatePickerItemMsg{Title: connectPickerTitle, Label: it.Label, Badge: "online", Detail: it.Detail}
}

// sshHostsGroup heads the ssh config hosts in the connect picker: they are
// machines aidiff can log in to, not servers known to run T3 Code.
const sshHostsGroup = "SSH hosts · T3 Code must be installed there"

// serverItems lists the connection candidates, each once: the T3 Code
// servers first, the ssh config hosts not among them under sshHostsGroup.
func serverItems(store t3client.TokenStore) []splitview.PickerItem {
	var items []splitview.PickerItem
	seen := map[string]bool{}
	add := func(label, detail, group string) {
		if label == "" || seen[label] {
			return
		}
		seen[label] = true
		items = append(items, splitview.PickerItem{Label: label, Detail: detail, Group: group})
	}
	if _, err := t3client.LocalOrigin(); err == nil {
		add("local", "T3 Code on this machine", "")
	}
	saved, _ := store.Servers() // an unreadable token file is reported when connecting
	for _, s := range saved {
		add(s, "saved login", "")
	}
	for _, h := range t3client.SSHHosts() {
		add("ssh://"+h, "~/.ssh/config", sshHostsGroup)
	}
	return items
}

// serverSpec reads typed text as a server name: a bare name is an ssh host.
func serverSpec(s string) string {
	s = strings.TrimSpace(s)
	if s == "local" || strings.Contains(s, "://") {
		return s
	}
	return "ssh://" + s
}

// connect reaches the server named by spec, makes it the live session and
// shows its projects in the sidebar. The review on screen stays, and so does
// its session while it is there. A connection that lands after the user
// chose another one (chooseLocked) is closed instead: the last choice wins,
// not the last dial to finish. It runs on the UI goroutine, as the choice.
func (t *Palette) connect(spec string) tea.Cmd {
	return t.connectWith(spec, true, func(ctx context.Context) (Session, error) {
		return t.dial(ctx, spec)
	})
}

// pairWith pairs with the server spec, which link, printed by `t3 pair`
// there, names, and connects as connect does; the token is saved under
// spec, so later connections need no link. The link is shown nowhere: its
// credential grants access until spent.
func (t *Palette) pairWith(spec, link string) tea.Cmd {
	if t.pair == nil {
		return flash("T3: pairing is not available here")
	}
	return t.connectWith(spec, false, func(ctx context.Context) (Session, error) {
		return t.pair(ctx, link)
	})
}

// connectWith is connect, reaching the server named by spec with dial. With
// login, an SSH server that wants a password or a host key confirmed gets
// the user's answer (loginThen) and is dialed once more.
func (t *Palette) connectWith(spec string, login bool, dial func(context.Context) (Session, error)) tea.Cmd {
	t.mu.Lock()
	choice := t.chooseLocked()
	t.dialed = dialing{spec: spec, choice: choice, since: time.Now()}
	t.dialTickLocked(choice)
	t.mu.Unlock()
	show := func() tea.Msg { return splitview.ShowSidebarMsg{} } // the panel says it connects, at once
	return tea.Batch(flash("T3: connecting to "+spec+"…"), show, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ConnectTimeout)
		defer cancel()
		s, sh, err := reach(ctx, dial)
		if err != nil && login && t3client.NeedsLogin(err) && t.login != nil {
			if cmd := t.loginThen(spec, err); cmd != nil {
				return cmd()
			}
		}
		if host, isSSH := sshHostOf(spec); err != nil && isSSH && t.isMachine(host, err) {
			return t.machineOpened(choice, host)
		}
		if err != nil {
			t.dialFailed(choice, err)
			return t3Failed(err)
		}
		ok, stale := t.adoptChosen(s, sh, choice)
		if stale {
			return splitview.FlashMsg{Text: "T3: " + spec + " answered after another connection was chosen; not used"}
		}
		if !ok {
			return nil // the program is ending
		}
		connected := tea.BatchMsg{
			func() tea.Msg {
				return ui.MachineIdentityMsg{Name: s.MachineName(), T3: true}
			},
			func() tea.Msg {
				return splitview.ShowSidebarMsg{}
			},
		}
		return withNotices(s, connected)
	})
}

// execProcess hands the screen to a command; a variable for the tests.
var execProcess = tea.ExecProcess

// loginThen hands the screen to the ssh login to spec, which failed with
// err for want of an answer, then dials spec once more, without offering a
// login again: a second refusal is shown as it is. nil when spec has no
// login (not an SSH server).
func (t *Palette) loginThen(spec string, err error) tea.Cmd {
	return t.loginAnd(spec, func(runErr error) tea.Msg {
		t.mu.Lock()
		if t.dialed.spec == spec {
			t.dialed.err = err
		}
		t.mu.Unlock()
		return t3Failed(fmt.Errorf("ssh login to %s did not complete: %w", spec, runErr))
	}, func() tea.Msg {
		return tea.BatchMsg{t.connectWith(spec, false, func(ctx context.Context) (Session, error) {
			return t.dial(ctx, spec)
		})}
	})
}

// loginAnd hands the screen to the ssh login to spec, then runs then; a
// login that did not complete gets failed instead. nil when spec has no
// login (not an SSH server) or none can be offered.
func (t *Palette) loginAnd(spec string, failed func(error) tea.Msg, then func() tea.Msg) tea.Cmd {
	if t.login == nil {
		return nil
	}
	cmd, cleanup, lerr := t.login(spec)
	if lerr != nil {
		return nil
	}
	return execProcess(cmd, func(runErr error) tea.Msg {
		cleanup()
		if runErr != nil {
			return failed(runErr)
		}
		return then()
	})
}

// dialFailed keeps why the dial of choice failed for the panels, unless
// the user chose something else meanwhile.
func (t *Palette) dialFailed(choice uint64, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dialed.choice == choice && t.dialed.spec != "" {
		t.dialed.err = err
	}
}

// dialTickLocked redraws the panels each second while the dial of choice
// goes on, so its elapsed time counts on and nothing looks stuck; Close ends
// it. mu must be held.
func (t *Palette) dialTickLocked(choice uint64) {
	if t.closed {
		return
	}
	t.loops.Go(func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-t.ctx.Done():
				return
			case <-tick.C:
			}
			t.mu.Lock()
			going := t.dialed.choice == choice && t.dialed.spec != "" && t.dialed.err == nil
			p := t.program
			t.mu.Unlock()
			if !going {
				return
			}
			if p != nil {
				p.Send(shellRefreshedMsg{})
			}
		}
	})
}

// takeOver puts s, a new session to old's server (autoreconnect.go), in
// old's place and rebuilds the review on screen over it, with the
// annotations of inner, when that review came over old; the live refresh and
// the chat panes follow it, and the parked reviews over old are rebuilt as
// they come back (adopt). It runs on the UI goroutine, so the review on
// screen is the one the user sees.
func (t *Palette) takeOver(old, s Session, sh t3client.Shell, inner tea.Model) tea.Cmd {
	shown, store := t.OnScreen(), storeOf(inner)
	if !t.adopt(s, sh, old) {
		return flash("T3: the connection changed meanwhile; reconnect canceled")
	}
	if shown == nil || shown.Session != old {
		return func() tea.Msg { return withNotices(s, splitview.FlashMsg{Text: "T3: reconnected to " + s.Label()}) }
	}
	return func() tea.Msg { return withNotices(s, t.reopen(s, sh, shown.Target, store)) }
}

// reopen rebuilds the review of target over s with the annotations in
// store. A thread is looked up again, so it shows as it is now.
func (t *Palette) reopen(s Session, sh t3client.Shell, target t3client.Target, store *annotation.Store) tea.Msg {
	if th := target.Thread; th != nil {
		var err error
		if target, err = t3client.Resolve(sh, th.ID); err != nil {
			return t3Error("reopen thread", err)
		}
	}
	return t.showMsg(t.newReview(s, target), store, "reopen thread")
}

// rebuildParked rebuilds r, a parked review moved to the session that
// replaced its own (repointLocked), over that session, with its annotations
// and the way it compared; a thread is looked up again. Its workspace comes
// on screen with it.
func (t *Palette) rebuildParked(r *Review) tea.Cmd {
	return asking("reopening "+r.Target.Label(), func(ctx context.Context) tea.Msg {
		sh, err := t.fetchShell(ctx, r.Session)
		if err != nil {
			return t3Error("reopen "+r.Target.Label(), err)
		}
		target := r.Target
		if th := target.Thread; th != nil {
			if target, err = t3client.Resolve(sh, th.ID); err != nil {
				return t3Error("reopen thread", err)
			}
		}
		next := &Review{Session: r.Session, Target: target, Source: r.Source, SourceLabel: r.SourceLabel, View: r.View}
		return t.showMsg(next, t.storeFor(r), "reopen thread")
	})
}
