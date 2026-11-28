package main

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/t3palette"
)

// decodePS reads a PowerShell -EncodedCommand back.
func decodePS(t *testing.T, enc string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(enc)
	require.NoError(t, err)
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func TestTerminal_WindowsMachineGetsPowerShellInTheFolder(t *testing.T) {
	l := terminalLauncher{place: func() t3palette.Place {
		return t3palette.Place{SSHHost: "me@workpc:2222", SSHControlPath: "/tmp/ctl", Dir: "C:/Users/me/it's repo", Windows: true}
	}}
	argv, local := l.argv()
	assert.Empty(t, local)
	require.GreaterOrEqual(t, len(argv), 4)
	assert.Equal(t, []string{"ssh", "-t", "-p", "2222"}, argv[:4])
	assert.Contains(t, strings.Join(argv, " "), "-o ControlPath=/tmp/ctl")
	n := len(argv)
	assert.Equal(t, []string{"--", "me@workpc"}, argv[n-3:n-1])
	cmd := argv[n-1]
	enc, ok := strings.CutPrefix(cmd, "powershell -NoLogo -NoExit -EncodedCommand ")
	require.True(t, ok, cmd)
	assert.Equal(t, "Set-Location -LiteralPath 'C:/Users/me/it''s repo'", decodePS(t, enc))

	l.command = "git status"
	cmd = func() string { a, _ := l.argv(); return a[len(a)-1] }()
	enc, _ = strings.CutPrefix(cmd, "powershell -NoLogo -NoExit -EncodedCommand ")
	assert.Equal(t, "Set-Location -LiteralPath 'C:/Users/me/it''s repo'; git status", decodePS(t, enc), "--terminal-command runs there first")
}

func TestRemoteFolderSource(t *testing.T) {
	src := remoteFolderSource(t3client.RemoteDir{Host: "workpc", Path: "C:/Users/me/repo"})
	assert.Equal(t, "workpc", src.machineName("thishost"), "the status bar names the machine")
	assert.Equal(t, "repo", src.projectName())
	assert.NotNil(t, src.branchLoader(), "its branch is read there")
	assert.Nil(t, src.reverter(options{}), "no revert: its files are not here")
	assert.False(t, editorPolicy(options{}, src).Available)
	assert.Nil(t, remoteEditor(src), "a Windows or unchecked host gets no editor over ssh")
}
