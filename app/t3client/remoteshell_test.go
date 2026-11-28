package t3client

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyShell(t *testing.T) {
	assert.Equal(t, ShellPowerShell, classifyShell("5\r\n"))
	assert.Equal(t, ShellPowerShell, classifyShell("7\n"))
	assert.Equal(t, ShellPOSIX, classifyShell(".PSVersion.Major\n"))
	assert.Equal(t, ShellCmd, classifyShell("$PSVersionTable.PSVersion.Major\r\n"))
	assert.Equal(t, ShellPOSIX, classifyShell("\n"), "fish drops the word whole: POSIX enough, as runCommand uses /bin/sh")
}

func TestRemoteDir(t *testing.T) {
	for _, d := range []RemoteDir{
		{Host: "workpc", Path: "C:/Users/me/repo"},
		{Host: "box", Path: "/home/me/repo"},
		{Host: "me@box:2222", Path: "/srv/x"},
		{Host: "fe80::1", Path: "/srv/x"},
		{Host: "workpc", Path: "D:"},
	} {
		got, ok := ParseRemoteDir(d.String())
		require.True(t, ok, d.String())
		assert.Equal(t, d, got)
		assert.Equal(t, d, mustParse(t, filepath.Clean(d.String())), "survives filepath.Clean")
	}
	assert.Equal(t, "ssh:workpc:C:/Users/me", filepath.Dir(RemoteDir{Host: "workpc", Path: "C:/Users/me/repo"}.String()))
	assert.Equal(t, "repo", filepath.Base(RemoteDir{Host: "workpc", Path: "C:/Users/me/repo"}.String()))
	for _, s := range []string{"/home/me/repo", "ssh:workpc", "ssh:workpc:relative", "ssh::/x", "ssh:-oProxy:/x", "ssh:a b:/x"} {
		_, ok := ParseRemoteDir(s)
		assert.False(t, ok, s)
	}
	assert.Equal(t, "workpc:C:/x", RemoteDir{Host: "workpc", Path: "C:/x"}.Label())
}

func mustParse(t *testing.T, s string) RemoteDir {
	t.Helper()
	d, ok := ParseRemoteDir(s)
	require.True(t, ok, s)
	return d
}

func TestWindowsCommandLine(t *testing.T) {
	assert.Equal(t, `diff -U3 -- a.txt`, windowsCommandLine([]string{"diff", "-U3", "--", "a.txt"}))
	assert.Equal(t, `"" "a b" "say \"hi\"" C:\dir\ C:\x\y`, windowsCommandLine([]string{"", "a b", `say "hi"`, `C:\dir\`, `C:\x\y`}))
	assert.Equal(t, `"C:\my dir\\"`, windowsCommandLine([]string{`C:\my dir\`}), "a quoted word's trailing backslashes doubled")
	assert.Equal(t, `"a\\\"b"`, windowsCommandLine([]string{`a\"b`}))
}

// pwsh is a PowerShell to run the scripts with, skipping the test without
// one: AIDIFF_PWSH, else pwsh on PATH.
func pwsh(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("AIDIFF_PWSH"); p != "" {
		return p
	}
	p, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("no PowerShell: set AIDIFF_PWSH or put pwsh on PATH")
	}
	return p
}

func TestPSRunScript(t *testing.T) {
	ps := pwsh(t)
	dir := t.TempDir()
	runPS := func(script string) (string, string, int) {
		t.Helper()
		assert.NotContains(t, script, `"`, "no double quote reaches Windows' command line")
		assert.NotContains(t, script, "  ", "no run of spaces")
		cmd := exec.Command(ps, "-NoLogo", "-NoProfile", "-NonInteractive", "-c", script)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		code := 0
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			code = ee.ExitCode()
		} else {
			require.NoError(t, err)
		}
		return string(out), stderr.String(), code
	}

	// bytes come through as written: NUL, CR LF, no newline added
	out, _, code := runPS(psRunScript(dir, "printf", []string{`a\0b\r\nc`}))
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\x00b\r\nc", out)

	// args arrive whole, in the folder asked for
	weird := []string{"a b", `say "hi"`, `back\slash\`, "ünïcødé", "", "$HOME;'x'"}
	out, _, _ = runPS(psRunScript(dir, "sh", append([]string{"-c", `pwd; for a in "$@"; do printf '[%s]' "$a"; done`, "sh"}, weird...)))
	want, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	assert.Equal(t, want+"\n[a b][say \"hi\"][back\\slash\\][ünïcødé][][$HOME;'x']", out)

	// the exit code and stderr are the program's
	out, stderr, code := runPS(psRunScript("", "sh", []string{"-c", "echo out; echo oops >&2; exit 3"}))
	assert.Equal(t, 3, code)
	assert.Equal(t, "out\n", out)
	assert.Contains(t, stderr, "oops")

	// encoded, as cmd gets it
	enc := strings.TrimPrefix(EncodedPowerShell(psRunScript(dir, "printf", []string{"x y"})), "powershell ")
	cmd := exec.Command(ps, strings.Fields(enc)...)
	b, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "x y", string(b))
}

func TestRunCommandPOSIX(t *testing.T) {
	dir := t.TempDir()
	line := runCommand(ShellPOSIX, dir, "sh", []string{"-c", `pwd; printf '[%s]' "$@"`, "sh", "a b", "it's", "$x"})
	out, err := exec.Command("/bin/sh", "-c", line).Output() // as the remote login shell reads it
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	assert.Equal(t, want+"\n[a b][it's][$x]", string(out))

	out, err = exec.Command("/bin/sh", "-c", runCommand(ShellPOSIX, "", "printf", []string{"home"})).Output()
	require.NoError(t, err)
	assert.Equal(t, "home", string(out), "no folder: the login's own")

	err = exec.Command("/bin/sh", "-c", runCommand(ShellPOSIX, filepath.Join(dir, "missing"), "true", nil)).Run()
	require.Error(t, err, "a folder it cannot enter fails rather than running elsewhere")
}
