package t3client

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"unicode/utf16"
)

// A machine is an SSH host reached for itself rather than for a T3 Code on
// it: its folders are reviewed as this machine's are, with git and the
// terminals running there. RunOn runs one program on it, in a folder, and
// returns what the program wrote, byte for byte, whatever shell the host
// reads commands with: a POSIX shell, PowerShell or cmd (Windows' OpenSSH
// starts the default shell it is set to).

// RemoteShell is the kind of shell an SSH host reads commands with.
type RemoteShell int

const (
	ShellPOSIX      RemoteShell = iota // sh, bash, zsh…: anything that reads POSIX syntax
	ShellPowerShell                    // Windows PowerShell or pwsh
	ShellCmd                           // cmd.exe, Windows OpenSSH's own default
)

// String names the shell.
func (s RemoteShell) String() string {
	switch s {
	case ShellPowerShell:
		return "PowerShell"
	case ShellCmd:
		return "cmd"
	}
	return "POSIX shell"
}

// Windows reports whether the host is a Windows one, whose folders are
// spelled C:/….
func (s RemoteShell) Windows() bool {
	return s != ShellPOSIX
}

// shellProbe tells the shells apart: PowerShell prints its major version, a
// POSIX shell expands the unset variable to nothing and prints the rest, and
// cmd prints it all as it is.
const shellProbe = "echo $PSVersionTable.PSVersion.Major"

// shells caches each host's shell, which DetectShell found.
var shells struct {
	mu     sync.Mutex
	byHost map[string]RemoteShell
}

// classifyShell reads shellProbe's output.
func classifyShell(out string) RemoteShell {
	out = strings.TrimSpace(out)
	switch {
	case out != "" && strings.Trim(out, "0123456789") == "":
		return ShellPowerShell
	case strings.HasPrefix(out, ".PSVersion"):
		return ShellPOSIX
	case strings.Contains(out, "$PSVersionTable"):
		return ShellCmd
	}
	return ShellPOSIX
}

// DetectShell finds the shell host reads commands with, asking once per
// run: over its login's connection when it has one, never prompting.
func DetectShell(ctx context.Context, host string) (RemoteShell, error) {
	if err := validSSHHost(host); err != nil {
		return ShellPOSIX, err
	}
	shells.mu.Lock()
	s, ok := shells.byHost[host]
	shells.mu.Unlock()
	if ok {
		return s, nil
	}
	out, err := sshOutput(ctx, host, shellProbe)
	if err != nil {
		return ShellPOSIX, fmt.Errorf("reach %s: %w", host, err)
	}
	s = classifyShell(string(out))
	shells.mu.Lock()
	if shells.byHost == nil {
		shells.byHost = map[string]RemoteShell{}
	}
	shells.byHost[host] = s
	shells.mu.Unlock()
	return s, nil
}

// KnownShell is host's shell as DetectShell found it, and whether it did.
func KnownShell(host string) (RemoteShell, bool) {
	shells.mu.Lock()
	defer shells.mu.Unlock()
	s, ok := shells.byHost[host]
	return s, ok
}

// RunError is a program that ran on a machine and failed: its exit code
// and what it wrote to stderr.
type RunError struct {
	Code   int
	Stderr string
}

func (e *RunError) Error() string {
	if msg := strings.TrimSpace(e.Stderr); msg != "" {
		return fmt.Sprintf("exit status %d: %s", e.Code, msg)
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

// RunOn runs program with args on host, in folder dir ("" for the login's
// own), and returns its stdout as written. A program that exits with a
// code other than 0 fails with a *RunError; ssh's own failure (255, the
// host unreachable or refusing) with the error ssh gave.
func RunOn(ctx context.Context, host, dir, program string, args ...string) ([]byte, error) {
	if err := validSSHHost(host); err != nil {
		return nil, err
	}
	shell, err := DetectShell(ctx, host)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "ssh", sshArgv(host, runCommand(shell, dir, program, args))...) //nolint:gosec // host validated by validSSHHost
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() != 255 {
		return stdout.Bytes(), &RunError{Code: exit.ExitCode(), Stderr: stderr.String()}
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("ssh %s: %w: %s", host, err, msg)
		}
		return nil, fmt.Errorf("ssh %s: %w", host, err)
	}
	return stdout.Bytes(), nil
}

// runCommand is the command line that runs program with args in dir on a
// host whose shell is shell.
func runCommand(shell RemoteShell, dir, program string, args []string) string {
	switch shell {
	case ShellPowerShell:
		return psRunScript(dir, program, args)
	case ShellCmd: // cmd has no quoting to trust: PowerShell runs it, the script encoded
		return EncodedPowerShell(psRunScript(dir, program, args))
	}
	words := []string{"exec", "/bin/sh", "-c", shQuote(`[ -z "$1" ] || cd -- "$1" || exit 1; shift; exec "$@"`), "sh", shQuote(dir), shQuote(program)}
	for _, a := range args {
		words = append(words, shQuote(a))
	}
	return strings.Join(words, " ")
}

// shQuote quotes s as one word for a POSIX shell.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// psRunScript is PowerShell that runs program with args in dir and copies
// its stdout to its own unchanged: PowerShell re-encodes the output of a
// program it runs as text, line by line, so the program runs as a .NET
// process whose output stream is copied as bytes, its stderr passed on and
// its exit code made the script's. Every value it carries is base64, so
// nothing in a path or an argument is read as PowerShell, and the script
// itself holds no double quote, which Windows' command line would take
// apart on the way to PowerShell, and no run of spaces, which it would
// collapse.
func psRunScript(dir, program string, args []string) string {
	b64 := func(s string) string { return "'" + base64.StdEncoding.EncodeToString([]byte(s)) + "'" }
	return strings.Join([]string{
		"$ErrorActionPreference='Stop'",
		"$u=New-Object Text.UTF8Encoding $false",
		"function d($s){$u.GetString([Convert]::FromBase64String($s))}",
		"$p=New-Object Diagnostics.ProcessStartInfo",
		"$p.FileName=d " + b64(program),
		"$p.Arguments=d " + b64(windowsCommandLine(args)),
		"$w=d " + b64(dir),
		"if($w){$p.WorkingDirectory=$w}",
		"$p.UseShellExecute=$false",
		"$p.RedirectStandardInput=$true",
		"$p.RedirectStandardOutput=$true",
		"$p.RedirectStandardError=$true",
		"$c=[Diagnostics.Process]::Start($p)",
		"$c.StandardInput.Close()",
		"$e=$c.StandardError.ReadToEndAsync()",
		"$o=[Console]::OpenStandardOutput()",
		"$c.StandardOutput.BaseStream.CopyTo($o)",
		"$o.Flush()",
		"$c.WaitForExit()",
		"[Console]::Error.Write($e.Result)",
		"exit $c.ExitCode",
	}, ";")
}

// EncodedPowerShell is a command line that runs script with PowerShell
// whatever shell reads it: the script goes as -EncodedCommand, UTF-16LE in
// base64, which holds no character any shell treats specially.
func EncodedPowerShell(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, 2*len(u))
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return "powershell -NoLogo -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(b)
}

// windowsCommandLine joins args into a Windows command line that a program
// built with the Microsoft C runtime, as git is, splits back into the same
// args: each quoted when it is empty or holds a blank or a quote, with the
// backslashes before a quote doubled.
func windowsCommandLine(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != "" && !strings.ContainsAny(a, " \t\n\v\"") {
			out = append(out, a)
			continue
		}
		var b strings.Builder
		b.WriteByte('"')
		slashes := 0
		for _, r := range a {
			switch r {
			case '\\':
				slashes++
				continue
			case '"':
				b.WriteString(strings.Repeat(`\`, 2*slashes+1))
			default:
				b.WriteString(strings.Repeat(`\`, slashes))
			}
			slashes = 0
			b.WriteRune(r)
		}
		b.WriteString(strings.Repeat(`\`, 2*slashes))
		b.WriteByte('"')
		out = append(out, b.String())
	}
	return strings.Join(out, " ")
}

// RemoteDir is a folder on a machine (see RunOn), as the palette keeps it
// among this machine's folders: "ssh:" + host + ":" + path, in the manner
// of scp, which filepath.Clean, Base and Dir leave meaningful. A path on a
// Windows host is spelled C:/….
type RemoteDir struct {
	Host string
	Path string
}

// remoteDirPrefix starts a RemoteDir's string; a folder of this machine is
// always absolute, so never starts with it.
const remoteDirPrefix = "ssh:"

// String is the folder as the palette keeps it.
func (d RemoteDir) String() string {
	return remoteDirPrefix + d.Host + ":" + d.Path
}

// Label is the folder as people read it: host:path.
func (d RemoteDir) Label() string {
	return d.Host + ":" + d.Path
}

// ParseRemoteDir reads a folder the palette keeps; ok is false for one of
// this machine. The host ends at the first colon followed by the path: "/"
// or a drive letter's "C:/". A port (box:2222) or an IPv6 address has
// colons of its own, never followed by either.
func ParseRemoteDir(s string) (RemoteDir, bool) {
	rest, ok := strings.CutPrefix(s, remoteDirPrefix)
	if !ok {
		return RemoteDir{}, false
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] != ':' || i == 0 {
			continue
		}
		path := rest[i+1:]
		if strings.HasPrefix(path, "/") || isDrivePath(path) {
			if validSSHHost(rest[:i]) != nil {
				return RemoteDir{}, false
			}
			return RemoteDir{Host: rest[:i], Path: path}, true
		}
	}
	return RemoteDir{}, false
}

// isDrivePath reports whether p starts with a drive letter: C:/ or C:
// alone.
func isDrivePath(p string) bool {
	if len(p) < 2 || p[1] != ':' {
		return false
	}
	c := p[0]
	return ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) && (len(p) == 2 || p[2] == '/')
}

// IsRemoteDir reports whether s is a folder on a machine, not this one.
func IsRemoteDir(s string) bool {
	_, ok := ParseRemoteDir(s)
	return ok
}

// LoginControl is the control socket of host's interactive login, "" when
// it has none up (LoginCommand): a terminal shares its connection.
func LoginControl(host string) string {
	return loginControl(host)
}

// InteractivePowerShell is a command line that opens an interactive
// PowerShell in folder dir ("" for the login's own), running command first
// when there is one, on a Windows host, whatever shell reads it: the
// script goes encoded, as EncodedPowerShell's does.
func InteractivePowerShell(dir, command string) string {
	var parts []string
	if dir != "" {
		parts = append(parts, "Set-Location -LiteralPath '"+strings.ReplaceAll(dir, "'", "''")+"'")
	}
	if command != "" {
		parts = append(parts, command)
	}
	enc := strings.TrimPrefix(EncodedPowerShell(strings.Join(parts, "; ")), "powershell -NoLogo -NoProfile -NonInteractive ")
	if len(parts) == 0 {
		return "powershell -NoLogo"
	}
	return "powershell -NoLogo -NoExit " + enc
}
