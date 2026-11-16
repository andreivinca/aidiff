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
// lists its projects. The review on screen stays, and so does its session
// while it is there.
func (t *Palette) connect(spec string) tea.Cmd {
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
		if !t.adopt(s, sh, nil) {
			return nil // the program is ending
		}
		picker := t.projectList(s, sh)
		connected := tea.BatchMsg{
			func() tea.Msg {
				return ui.MachineIdentityMsg{Name: s.MachineName(), T3: true}
			},
			func() tea.Msg {
				return picker
			},
		}
		return withNotices(s, connected)
	})
}

// reconnect replaces the live session with a fresh one to the same server,
// made the way the first was. When the review on screen came over the old
// session it is rebuilt over the new one, with its annotations and its
// thread looked up again; the live refresh and the chat panes follow it.
func (t *Palette) reconnect(inner tea.Model) tea.Cmd {
	v := t.state()
	if v.live == nil {
		return t.connectPicker(inner)
	}
	old, shown, store := v.live, v.onScreen, storeOf(inner)
	return tea.Batch(flash("T3: reconnecting to "+old.Label()+"…"), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ConnectTimeout)
		defer cancel()
		s, sh, err := reach(ctx, old.Redial)
		if err != nil {
			return t3Failed(err)
		}
		if !t.adopt(s, sh, old) {
			return splitview.FlashMsg{Text: "T3: the connection changed meanwhile; reconnect canceled"}
		}
		if shown == nil || shown.Session != old || shown != t.OnScreen() {
			return withNotices(s, splitview.FlashMsg{Text: "T3: reconnected to " + s.Label()})
		}
		return withNotices(s, t.reopen(s, sh, shown.Target, store))
	})
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
