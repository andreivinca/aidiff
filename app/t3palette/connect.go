package t3palette

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/ui"
)

// connectPicker offers the servers aidiff knows of: the local T3 Code,
// servers with a saved login and the hosts of ~/.ssh/config. Anything else
// is typed.
func (t *Palette) connectPicker(tea.Model) tea.Cmd {
	return func() tea.Msg {
		return splitview.ShowPickerMsg{
			Title: "T3 Code: connect to",
			Items: serverItems(t.cfg.Store),
			OnPick: func(it splitview.PickerItem) tea.Cmd {
				return t.connect(it.Label)
			},
			OnText: func(s string) tea.Cmd {
				return t.connect(serverSpec(s))
			},
			TextLabel: "Connect to %s",
		}
	}
}

// serverItems lists the connection candidates, each once.
func serverItems(store t3client.TokenStore) []splitview.PickerItem {
	var items []splitview.PickerItem
	seen := map[string]bool{}
	add := func(label, detail string) {
		if label == "" || seen[label] {
			return
		}
		seen[label] = true
		items = append(items, splitview.PickerItem{Label: label, Detail: detail})
	}
	if _, err := t3client.LocalOrigin(); err == nil {
		add("local", "T3 Code on this machine")
	}
	saved, _ := store.Servers() // an unreadable token file is reported when connecting
	for _, s := range saved {
		add(s, "saved login")
	}
	for _, h := range t3client.SSHHosts() {
		add("ssh://"+h, "~/.ssh/config")
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
	t.mu.Lock()
	choice := t.chooseLocked()
	t.mu.Unlock()
	return tea.Batch(flash("T3: connecting to "+spec+"…"), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ConnectTimeout)
		defer cancel()
		dial := func(ctx context.Context) (Session, error) {
			return t.dial(ctx, spec)
		}
		s, sh, err := reach(ctx, dial)
		if err != nil {
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
