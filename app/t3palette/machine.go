package t3palette

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// An SSH host that runs no T3 Code is a machine of its own, reviewed as
// this one is: connecting to it asks for one of its folders, which opens as
// a local folder does (openLocal), its git and its terminals running there.
// Such a folder is spelled t3client.RemoteDir among this machine's, so the
// history panel, the tabs and the switcher keep it as any other.

// machineTimeout bounds asking a host what shell it runs, or where a
// folder's repository is.
const machineTimeout = 20 * time.Second

// sshHostOf is the host of server spec ssh://host; ok is false for any other
// server.
func sshHostOf(spec string) (string, bool) {
	host, ok := strings.CutPrefix(spec, "ssh://")
	return host, ok && host != ""
}

// isMachine reports whether host, which failed to connect as a T3 server
// with err, is a machine to open as such: it runs no T3 Code, or reads
// commands with a shell the T3 checks cannot run in (PowerShell, cmd).
func (t *Palette) isMachine(host string, err error) bool {
	if t.cfg.Local.Open == nil || t.machineShell == nil || t3client.NeedsLogin(err) {
		return false
	}
	if errors.Is(err, t3client.ErrNoT3Server) {
		return true
	}
	ctx, cancel := context.WithTimeout(t.ctx, machineTimeout)
	defer cancel()
	shell, serr := t.machineShell(ctx, host)
	return serr == nil && shell != t3client.ShellPOSIX
}

// machineOpened clears the dial of choice, which reached a machine rather
// than a T3 server, and asks for a folder on host.
func (t *Palette) machineOpened(choice uint64, host string) tea.Msg {
	t.mu.Lock()
	if t.dialed.choice == choice {
		t.dialed = dialing{}
	}
	t.mu.Unlock()
	return tea.BatchMsg{
		flash("T3: " + host + " runs no T3 Code: open one of its folders"),
		t.machinePicker(host),
	}
}

// machinePicker asks for a folder on host: one opened before, or a path
// typed, which opens the repository it is in.
func (t *Palette) machinePicker(host string) tea.Cmd {
	t.mu.Lock()
	var items []splitview.PickerItem
	for _, dir := range t.history {
		if d, ok := t3client.ParseRemoteDir(dir); ok && d.Host == host {
			items = append(items, splitview.PickerItem{Label: d.Path, Detail: path.Base(d.Path), Value: dir})
		}
	}
	t.mu.Unlock()
	return func() tea.Msg {
		return splitview.ShowPickerMsg{
			Title: "Open a folder on " + host,
			Items: items,
			OnPick: func(it splitview.PickerItem) tea.Cmd {
				dir, _ := it.Value.(string)
				return t.openLocal(dir)
			},
			OnText: func(s string) tea.Cmd {
				return t.openMachineFolder(host, s)
			},
			TextLabel: "Open %s",
		}
	}
}

// machinePath is a folder as typed for a machine: blanks trimmed, a
// Windows path's backslashes made slashes, as git spells it there.
func machinePath(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[1] == ':' {
		s = strings.ReplaceAll(s, `\`, "/")
	}
	return s
}

// openMachineFolder opens the repository folder dir on host is in, as a
// local folder opens.
func (t *Palette) openMachineFolder(host, dir string) tea.Cmd {
	dir = machinePath(dir)
	if dir == "" || t.resolveRemote == nil {
		return nil
	}
	return tea.Batch(flash("opening "+host+":"+dir+"…"), func() tea.Msg {
		ctx, cancel := context.WithTimeout(t.ctx, machineTimeout)
		defer cancel()
		root, err := t.resolveRemote(ctx, host, dir)
		if err != nil {
			return splitview.FlashMsg{Text: "open " + host + ":" + dir + ": " + err.Error()}
		}
		return tea.BatchMsg{t.openLocal(t3client.RemoteDir{Host: host, Path: root}.String())}
	})
}

// machineLabel is the machine a folder of the local panels is on: its host
// for one on an SSH machine, else localLabel.
func machineLabel(dir string) string {
	if d, ok := t3client.ParseRemoteDir(dir); ok {
		return d.Host
	}
	return localLabel
}
