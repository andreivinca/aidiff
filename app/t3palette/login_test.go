package t3palette

import (
	"context"
	"errors"
	"os/exec"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errDenied is how a connect to an SSH host that wants a password fails.
var errDenied = errors.New("read T3 state on workpc: exit status 255: user@10.0.0.9: Permission denied (publickey,password,keyboard-interactive).")

// fakeLogin stands in for the ssh login and the screen it is handed: the
// login succeeds unless fail is set.
func fakeLogin(t *testing.T, h *harness, fail error) (logins *atomic.Int32, cleaned *atomic.Int32) {
	t.Helper()
	logins, cleaned = &atomic.Int32{}, &atomic.Int32{}
	h.p.login = func(spec string) (*exec.Cmd, func(), error) {
		logins.Add(1)
		return exec.Command("true"), func() { cleaned.Add(1) }, nil
	}
	old := execProcess
	execProcess = func(_ *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		return func() tea.Msg { return fn(fail) }
	}
	t.Cleanup(func() { execProcess = old })
	return logins, cleaned
}

func TestConnect_PasswordHostLogsInThenConnects(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	logins, cleaned := fakeLogin(t, h, nil)
	var dials atomic.Int32
	h.p.dial = func(_ context.Context, spec string) (Session, error) {
		if dials.Add(1) == 1 {
			return nil, errDenied
		}
		return newFakeSession(spec, testShell()), nil
	}
	run(h.p.connect("ssh://workpc"))
	assert.EqualValues(t, 1, logins.Load(), "the user is asked once")
	assert.EqualValues(t, 1, cleaned.Load())
	assert.EqualValues(t, 2, dials.Load(), "dialed again once logged in")
	require.NotNil(t, h.p.liveSession())
	assert.Equal(t, "ssh://workpc", h.p.liveSession().Label())
}

func TestConnect_SecondRefusalIsShownNotAskedAgain(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	logins, _ := fakeLogin(t, h, nil)
	var dials atomic.Int32
	h.p.dial = func(context.Context, string) (Session, error) {
		dials.Add(1)
		return nil, errDenied
	}
	msgs := run(h.p.connect("ssh://workpc"))
	assert.EqualValues(t, 1, logins.Load(), "never a second login in a row")
	assert.EqualValues(t, 2, dials.Load())
	assert.Contains(t, flashes(msgs), "T3: "+errDenied.Error())
}

func TestConnect_FailedLoginIsShown(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	logins, cleaned := fakeLogin(t, h, errors.New("exit status 255"))
	var dials atomic.Int32
	h.p.dial = func(context.Context, string) (Session, error) {
		dials.Add(1)
		return nil, errDenied
	}
	msgs := run(h.p.connect("ssh://workpc"))
	assert.EqualValues(t, 1, logins.Load())
	assert.EqualValues(t, 1, cleaned.Load(), "cleaned up either way")
	assert.EqualValues(t, 1, dials.Load(), "not dialed again")
	assert.Contains(t, flashes(msgs), "T3: ssh login to ssh://workpc did not complete: exit status 255")
}

func TestConnect_OtherFailuresAskNoLogin(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	logins, _ := fakeLogin(t, h, nil)
	h.p.dial = func(context.Context, string) (Session, error) {
		return nil, errors.New("T3 Code is not running on workpc")
	}
	run(h.p.connect("ssh://workpc"))
	assert.Zero(t, logins.Load())
}
