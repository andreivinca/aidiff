package t3palette

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

const workpcRepo = "ssh:workpc:C:/Users/me/repo"

// machineHarness is a palette with local folders whose machine checks are
// faked: workpc reads commands with shell, and its folders resolve to
// C:/Users/me/repo.
func machineHarness(t *testing.T, shell t3client.RemoteShell, history ...string) (*harness, *localFake) {
	t.Helper()
	f := &localFake{}
	h := newHarness(t, Config{NoWatch: true, NoChat: true, NoTerminal: true, Local: f.config("", history...)}, nil)
	h.p.machineShell = func(context.Context, string) (t3client.RemoteShell, error) { return shell, nil }
	h.p.resolveRemote = func(_ context.Context, host, dir string) (string, error) {
		if dir == "C:/nowhere" {
			return "", errors.New("C:/nowhere on workpc is not a git repository")
		}
		return "C:/Users/me/repo", nil
	}
	return h, f
}

func TestMachine_HostWithoutT3OpensAFolderPicker(t *testing.T) {
	h, f := machineHarness(t, t3client.ShellPowerShell, "/home/me/local", workpcRepo, "ssh:box:/srv/other")
	h.p.dial = func(context.Context, string) (Session, error) {
		return nil, fmt.Errorf("read T3 state on workpc: exit status 1: At line:1 char:62")
	}
	msgs := run(h.p.connect("ssh://workpc"))
	assert.Contains(t, flashes(msgs), "T3: workpc runs no T3 Code: open one of its folders")
	picker := find[splitview.ShowPickerMsg](t, msgs)
	assert.Equal(t, "Open a folder on workpc", picker.Title)
	require.Len(t, picker.Items, 1, "the folders opened on workpc before, only")
	assert.Equal(t, "C:/Users/me/repo", picker.Items[0].Label)
	assert.Nil(t, dialErr(h), "nothing failed: the panel shows no error")

	show := find[splitview.SwitchWorkspaceMsg](t, run(picker.OnText(` C:\Users\me\repo\src `)))
	assert.Equal(t, localKey(workpcRepo), show.Key)
	show.Done(splitview.WorkspaceSwitch{Created: true})
	assert.Equal(t, []string{workpcRepo}, f.opened, "the repository the typed folder is in, as a folder of the machine")
	assert.Equal(t, workpcRepo, h.p.LocalDir())
	place := h.p.TerminalPlace()
	assert.Equal(t, "workpc", place.SSHHost)
	assert.Equal(t, "C:/Users/me/repo", place.Dir)

	msgs = run(picker.OnText("C:/nowhere"))
	assert.Contains(t, flashes(msgs), "open workpc:C:/nowhere: C:/nowhere on workpc is not a git repository")
}

func TestMachine_POSIXHostWithoutT3State(t *testing.T) {
	h, _ := machineHarness(t, t3client.ShellPOSIX)
	h.p.dial = func(context.Context, string) (Session, error) {
		return nil, fmt.Errorf("connect: box: %w: exit status 3", t3client.ErrNoT3Server)
	}
	msgs := run(h.p.connect("ssh://box"))
	assert.Equal(t, "Open a folder on box", find[splitview.ShowPickerMsg](t, msgs).Title)
}

func TestMachine_OtherFailuresStayFailures(t *testing.T) {
	h, _ := machineHarness(t, t3client.ShellPOSIX)
	h.p.dial = func(context.Context, string) (Session, error) {
		return nil, errors.New("T3 Code is not running on box")
	}
	msgs := run(h.p.connect("ssh://box"))
	assert.Contains(t, flashes(msgs), "T3: T3 Code is not running on box")
	for _, m := range msgs {
		_, picker := m.(splitview.ShowPickerMsg)
		assert.False(t, picker, "a T3 host whose server is down is not a machine")
	}
	require.Error(t, dialErr(h))

	h.p.dial = func(context.Context, string) (Session, error) {
		return nil, errors.New("connect: no such server")
	}
	msgs = run(h.p.connect("https://box.ts.net"))
	assert.Contains(t, flashes(msgs), "T3: connect: no such server", "only an ssh:// server can be a machine")
}

func TestMachine_FolderLabels(t *testing.T) {
	assert.Equal(t, "workpc", machineLabel(workpcRepo))
	assert.Equal(t, localLabel, machineLabel("/home/me/repo"))
	assert.Equal(t, "workpc:C:/Users/me", homeRelative("ssh:workpc:C:/Users/me"))
	assert.Equal(t, "C:/Users/me/repo", machinePath(` C:\Users\me\repo `))
	assert.Equal(t, "/srv/x", machinePath("/srv/x"))
}

// dialErr is why the palette's last dial failed, nil when it did not.
func dialErr(h *harness) error {
	h.p.mu.Lock()
	defer h.p.mu.Unlock()
	return h.p.dialed.err
}

func TestMachine_FolderWhoseLoginEndedLogsInThenOpens(t *testing.T) {
	f := &localFake{}
	h := newHarness(t, Config{NoWatch: true, NoChat: true, NoTerminal: true, Local: f.config("")}, nil)
	logins, _ := fakeLogin(t, h, nil)
	var opens int
	h.p.cfg.Local.Open = func(dir string) (LocalReview, error) {
		opens++
		if opens == 1 {
			return LocalReview{}, errors.New("reach workpc: exit status 255: u@h: Permission denied (password).")
		}
		return LocalReview{Model: fakeModel{}}, nil
	}
	msgs := expand(h.p.folderMsg(localKey(workpcRepo), workpcRepo, false))
	assert.EqualValues(t, 1, logins.Load(), "asked once")
	assert.Equal(t, 2, opens, "opened again once logged in")
	assert.Equal(t, localKey(workpcRepo), find[splitview.SwitchWorkspaceMsg](t, msgs).Key)

	opens = 0
	h.p.cfg.Local.Open = func(string) (LocalReview, error) {
		opens++
		return LocalReview{}, errors.New("reach workpc: Permission denied (password).")
	}
	msgs = expand(h.p.folderMsg(localKey(workpcRepo), workpcRepo, false))
	assert.EqualValues(t, 2, logins.Load(), "one more login, never two in a row")
	assert.Equal(t, 2, opens)
	assert.Contains(t, flashes(msgs), "open repo: reach workpc: Permission denied (password).")

	_ = expand(h.p.folderMsg(localKey("/home/me/repo"), "/home/me/repo", false))
	assert.EqualValues(t, 2, logins.Load(), "a folder of this machine never asks")
}
