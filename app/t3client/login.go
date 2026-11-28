package t3client

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// An SSH host that takes no key (a password, a one-time code, a host key to
// confirm) cannot be reached by the ssh commands aidiff runs on its own:
// they run with BatchMode, as nobody is there to answer. LoginCommand is the
// ssh the user answers instead, with the screen handed over to it. It logs
// in once and stays in the background as a ControlMaster, and from then on
// every ssh command to that host (the runtime state, the token, the tunnel,
// the terminals) goes over its connection, asking nothing. It ends a short
// while after the last of them (loginPersist), so after aidiff exits too.

// loginPersist is how long a login's connection outlives its last user:
// long enough for the gaps of a connect, short enough not to linger long
// after aidiff.
const loginPersist = "60"

// logins are the control sockets of the hosts logged in to (LoginCommand),
// in a private directory made on first use.
var logins struct {
	mu  sync.Mutex
	dir string
}

// loginPath is the control socket of a login to host, creating the
// directory that holds it; a short name, as a socket path is limited.
func loginPath(host string) (string, error) {
	logins.mu.Lock()
	defer logins.mu.Unlock()
	if logins.dir == "" {
		dir, err := loginDir()
		if err != nil {
			return "", err
		}
		logins.dir = dir
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(host))
	return filepath.Join(logins.dir, fmt.Sprintf("%08x", h.Sum32())), nil
}

// loginDir is the directory of this user's login sockets: one per user, so
// runs do not leave one each and a login lingering from the last run
// (loginPersist) serves the next; accepted only when it is this user's own,
// a plain directory nobody else can enter, else a fresh one.
func loginDir() (string, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("aidiff-ssh-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err == nil || errors.Is(err, fs.ErrExist) {
		if fi, lerr := os.Lstat(dir); lerr == nil && fi.IsDir() && fi.Mode().Perm() == 0o700 {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) == os.Getuid() {
				return dir, nil
			}
		}
	}
	dir, err := os.MkdirTemp("", "aidiff-ssh-") // created 0700
	if err != nil {
		return "", fmt.Errorf("create ssh login directory: %w", err)
	}
	return dir, nil
}

// loginControl is the control socket of a login to host that may still be
// up, "" when there is none. A socket whose master is gone does no harm:
// ssh then connects on its own, as it would without one.
func loginControl(host string) string {
	logins.mu.Lock()
	dir := logins.dir
	logins.mu.Unlock()
	if dir == "" {
		return ""
	}
	p, _ := loginPath(host)
	if fi, err := os.Stat(p); err != nil || fi.Mode().Type() != fs.ModeSocket {
		return ""
	}
	return p
}

// loginOptions are the options that send an ssh command to host over its
// login's connection, none when it has none.
func loginOptions(host string) []string {
	if p := loginControl(host); p != "" {
		return []string{"-o", "ControlMaster=no", "-o", "ControlPath=" + p}
	}
	return nil
}

// loginScript says what it logs in to, runs the ssh ("$@") and, when that
// fails, shows ssh's own messages, which it logs to $2, and waits for enter:
// the screen goes back to aidiff as soon as it ends.
const loginScript = `host=$1 log=$2; shift 2
printf '\naidiff: log in to %s\n\n' "$host"
"$@" && exit 0
status=$?
cat "$log" 2>/dev/null
printf '\nThe login failed. Press enter to go back to aidiff. '
read -r _
exit "$status"`

// LoginCommand is the ssh, for the server spec ssh://host, that the user
// answers to log in to host (the screen handed over to it), and the cleanup
// to call once it ended. It fails for a spec that is not an SSH host.
func LoginCommand(spec string) (*exec.Cmd, func(), error) {
	srv, err := parseServer(spec)
	if err != nil {
		return nil, nil, err
	}
	if srv.kind != serverSSH {
		return nil, nil, fmt.Errorf("%s is not reached over SSH", spec)
	}
	host := srv.sshHost
	control, err := loginPath(host)
	if err != nil {
		return nil, nil, err
	}
	log := control + ".log"
	_ = os.Remove(log)
	// -f: into the background once logged in; -E: its messages to log, so
	// the master never writes on aidiff's screen afterwards
	argv := append([]string{"ssh", "-f", "-N", "-E", log,
		"-o", "ControlMaster=yes", "-o", "ControlPath=" + control, "-o", "ControlPersist=" + loginPersist,
		"-o", "StreamLocalBindUnlink=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"},
		SSHArgs(host)...)
	cmd := exec.Command("/bin/sh", append([]string{"-c", loginScript, "sh", host, log}, argv...)...) //nolint:gosec,noctx // host validated by validSSHHost; the user answers it
	return cmd, func() { _ = os.Remove(log) }, nil
}

// NeedsLogin reports whether err is an SSH connection that wants an answer
// from the user: a password or code, or a host key to confirm. LoginCommand
// gets one.
func NeedsLogin(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, s := range []string{"Permission denied", "Host key verification failed", "Too many authentication failures"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// muxForward asks host's login connection, at control, to forward socket
// to remoteAddr (cancel true: to stop doing so).
func muxForward(control, host, socket, remoteAddr string, cancel bool) error {
	op := "forward"
	if cancel {
		op = "cancel"
	}
	ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	args := append([]string{"-o", "ControlPath=" + control, "-O", op, "-L", socket + ":" + remoteAddr}, SSHArgs(host)...)
	out, err := exec.CommandContext(ctx, "ssh", args...).CombinedOutput() //nolint:gosec // host validated by validSSHHost
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return fmt.Errorf("ssh -O %s: %w", op, err)
	}
	return nil
}

// muxHold is the remote command that keeps a session open over host's login
// connection while the tunnel lives: the login outlives its last session by
// loginPersist only. It reads the session's input, aidiff's lifeline, so it
// ends, leaving nothing behind on host, once that closes, aidiff dying
// included, or the session is dropped; and the session ends with the
// connection.
const muxHold = "cat >/dev/null"

// startMuxTunnel is startTunnel over host's login connection, at control:
// the connection forwards the socket, and a session held open over it ties
// the tunnel's life to it.
func startMuxTunnel(host, remoteAddr, control string) (*sshTunnel, error) {
	dir, err := os.MkdirTemp("", "aidiff-t3-") // created 0700
	if err != nil {
		return nil, fmt.Errorf("create ssh tunnel directory: %w", err)
	}
	t := newTunnel(dir)
	t.control = control // terminals share the login's connection
	if ferr := muxForward(control, host, t.socket, remoteAddr, false); ferr != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("forward over the ssh login to %s: %w", host, ferr)
	}
	t.unforward = func() { _ = muxForward(control, host, t.socket, remoteAddr, true) }
	argv := append(append([]string{"ssh"}, sshOptions...), "-o", "ControlMaster=no", "-o", "ControlPath="+control)
	argv = append(append(argv, SSHArgs(host)...), muxHold)
	if serr := t.startHeld(argv); serr != nil {
		t.unforward()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("start ssh tunnel to %s: %w", host, serr)
	}
	return t, nil
}

// startHeld runs argv, an ssh session over a login's connection, in a
// process group of its own, its input the lifeline: no watchdog is needed,
// as the session ends on the lifeline's EOF (muxHold).
func (t *sshTunnel) startHeld(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)             //nolint:gosec,noctx // host validated by validSSHHost; lives for the session
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // keep terminal signals away from it
	cmd.Stderr = t.stderr
	lifeline, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open lifeline: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("run ssh: %w", err)
	}
	t.cmd, t.lifeline = cmd, lifeline
	go t.wait()
	return nil
}
