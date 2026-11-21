package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/t3palette"
)

func TestTerminalLauncher_Argv(t *testing.T) {
	at := func(host, dir string) func() t3palette.Place {
		return func() t3palette.Place {
			return t3palette.Place{SSHHost: host, Dir: dir}
		}
	}
	shared := func() t3palette.Place {
		return t3palette.Place{SSHHost: "box", SSHControlPath: "/tmp/aidiff-t3-1/ssh.ctl", Dir: "/srv/wt"}
	}
	tests := []struct {
		name     string
		launcher terminalLauncher
		argv     []string
		dir      string
	}{
		{
			name:     "local shell in the reviewed folder",
			launcher: terminalLauncher{place: at("", "/home/me/wt")},
			dir:      "/home/me/wt",
		},
		{
			name:     "local command is read by a shell",
			launcher: terminalLauncher{command: `tmux new -A -s 'my review'`, place: at("", "/home/me/wt")},
			argv:     []string{"/bin/sh", "-c", `tmux new -A -s 'my review'`},
			dir:      "/home/me/wt",
		},
		{
			name:     "no folder known, e.g. a remote server without ssh",
			launcher: terminalLauncher{command: "zsh -l", place: at("", "")},
			argv:     []string{"/bin/sh", "-c", "zsh -l"},
		},
		{
			name:     "ssh login shell",
			launcher: terminalLauncher{place: at("box", "")},
			argv:     []string{"ssh", "-t", "-o", "ClearAllForwardings=yes", "--", "box", `exec /bin/sh -c 'exec "${SHELL:-/bin/sh}" -l' sh`},
		},
		{
			name:     "ssh in a folder, quoted for any login shell",
			launcher: terminalLauncher{place: at("box", "/srv/it's")},
			argv: []string{"ssh", "-t", "-o", "ClearAllForwardings=yes", "--", "box",
				`exec /bin/sh -c 'cd "$1" 2>/dev/null; shift; exec "${SHELL:-/bin/sh}" -l' sh '/srv/it'\''s'`},
		},
		{
			name:     "ssh runs the terminal command with /bin/sh",
			launcher: terminalLauncher{command: `tmux new -A -s 'my review'`, place: at("box", "/srv/wt")},
			argv: []string{"ssh", "-t", "-o", "ClearAllForwardings=yes", "--", "box",
				`exec /bin/sh -c 'cd "$1" 2>/dev/null; shift; tmux new -A -s '\''my review'\''' sh '/srv/wt'`},
		},
		{
			name:     "ssh to a host:port",
			launcher: terminalLauncher{place: at("me@box:2222", "")},
			argv: []string{"ssh", "-t", "-p", "2222", "-o", "ClearAllForwardings=yes", "--", "me@box",
				`exec /bin/sh -c 'exec "${SHELL:-/bin/sh}" -l' sh`},
		},
		{
			name:     "ssh over the T3 tunnel's connection",
			launcher: terminalLauncher{place: shared},
			argv: []string{"ssh", "-t", "-o", "ClearAllForwardings=yes", "-o", "ControlMaster=no", "-o", "ControlPath=/tmp/aidiff-t3-1/ssh.ctl",
				"--", "box", `exec /bin/sh -c 'cd "$1" 2>/dev/null; shift; exec "${SHELL:-/bin/sh}" -l' sh '/srv/wt'`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argv, dir := tt.launcher.argv()
			assert.Equal(t, tt.argv, argv)
			assert.Equal(t, tt.dir, dir)
		})
	}
}

func TestSSHDestination(t *testing.T) {
	tests := []struct{ host, dest, port string }{
		{"box", "box", ""},
		{"me@box", "me@box", ""},
		{"box:2222", "box", "2222"},
		{"me@box:22", "me@box", "22"},
		{"::1", "::1", ""},
		{"me@fe80::1", "me@fe80::1", ""},
		{"box:", "box:", ""},
		{"box:ssh", "box:ssh", ""},
	}
	for _, tt := range tests {
		dest, port := sshDestination(tt.host)
		assert.Equal(t, tt.dest, dest, tt.host)
		assert.Equal(t, tt.port, port, tt.host)
	}
}

// plainWords splits a command line into the words fish, csh, nushell and a
// POSIX shell all read the same way: bare words of letters, digits and
// / . _ + -, and single-quoted strings with no backslash inside, joined by
// \' for a quote. Anything else fails the test: syntax one of them reads
// differently, or not at all.
func plainWords(t *testing.T, line string) []string {
	t.Helper()
	var words []string
	var word strings.Builder
	inWord := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ' ':
			if inWord {
				words, inWord = append(words, word.String()), false
				word.Reset()
			}
			continue
		case c == '\'':
			end := strings.IndexByte(line[i+1:], '\'')
			require.GreaterOrEqual(t, end, 0, "unterminated quote in %q", line)
			quoted := line[i+1 : i+1+end]
			require.NotContains(t, quoted, `\`, "fish reads a backslash in quotes its own way: %q", line)
			word.WriteString(quoted)
			i += end + 1
		case c == '\\' && i+1 < len(line) && line[i+1] == '\'':
			word.WriteByte('\'')
			i++
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("/._+-", c) >= 0:
			word.WriteByte(c)
		default:
			require.Failf(t, "not read alike by every shell", "%q at %d in %q", c, i, line)
		}
		inWord = true
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}

// runAsAnyShell runs a command line the way a login shell that reads no
// POSIX would: split into plain words and executed with no shell, in a new
// folder. SHELL, which the command line ends up exec'ing, is a fake that
// records its folder and arguments, one per line, and runs nothing.
func runAsAnyShell(t *testing.T, line string) []string {
	t.Helper()
	words := plainWords(t, line)
	require.Greater(t, len(words), 3)
	require.Equal(t, []string{"exec", "/bin/sh", "-c"}, words[:3], "an exec of /bin/sh, whatever the login shell")
	dir := t.TempDir()
	login, out := filepath.Join(dir, "login"), filepath.Join(dir, "out")
	require.NoError(t, os.WriteFile(login, []byte("#!/bin/sh\npwd > "+out+"\nfor a; do echo \"$a\" >> "+out+"; done\n"), 0o700)) //nolint:gosec // test fixture
	cmd := exec.Command(words[1], words[2:]...)                                                                                  //nolint:gosec // the command under test
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "SHELL=" + login, "PATH=" + os.Getenv("PATH")}
	got, err := cmd.CombinedOutput()
	require.NoError(t, err, string(got))
	data, err := os.ReadFile(out) //nolint:gosec // test-owned path
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestTerminalLauncher_RemoteShellSuitsAnyLoginShell(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "my wt")
	require.NoError(t, os.Mkdir(folder, 0o700))
	argv, _ := terminalLauncher{place: func() t3palette.Place { return t3palette.Place{SSHHost: "box", Dir: folder} }}.argv()
	assert.Equal(t, []string{folder, "-l"}, runAsAnyShell(t, argv[len(argv)-1]), "the user's shell, as a login shell, in the folder")
}

func TestRemoteEditorArgv_SuitsAnyLoginShell(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "my wt")
	require.NoError(t, os.Mkdir(folder, 0o700))
	argv := remoteEditorArgv(t3palette.Place{SSHHost: "box", Dir: folder}, "src/a.go", 12)
	got := runAsAnyShell(t, argv[len(argv)-1])
	require.Len(t, got, 3)
	assert.Equal(t, []string{folder, "-lic"}, got[:2], "the user's interactive login shell, in the folder")

	// that shell is handed a command line of its own, read alike by any shell too
	bin, out := t.TempDir(), filepath.Join(t.TempDir(), "args")
	nvim := filepath.Join(bin, "nvim")
	require.NoError(t, os.WriteFile(nvim, []byte("#!/bin/sh\nfor a; do echo \"$a\" >> "+out+"; done\n"), 0o700)) //nolint:gosec // test fixture
	words := plainWords(t, got[2])
	require.Equal(t, []string{"exec", "/bin/sh", "-c"}, words[:3])
	cmd := exec.Command(words[1], words[2:]...) //nolint:gosec // the command under test
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "EDITOR=" + nvim}
	res, err := cmd.CombinedOutput()
	require.NoError(t, err, string(res))
	data, err := os.ReadFile(out) //nolint:gosec // test-owned path
	require.NoError(t, err)
	assert.Equal(t, "+12\nsrc/a.go\n", string(data))
}
