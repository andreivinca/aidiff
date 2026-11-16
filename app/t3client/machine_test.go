package t3client

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// detached is a session for spec that holds no connection: it names the
// server without reaching it.
func detached(spec string) (*Session, error) {
	srv, err := parseServer(spec)
	if err != nil {
		return nil, err
	}
	return &Session{server: srv}, nil
}

func TestSessionMachineName(t *testing.T) {
	local, _ := os.Hostname()
	for _, tt := range []struct{ spec, want string }{
		{"local", local},
		{"", local},
		{"ssh://boxdev", "boxdev"},
		{"ssh://andrei@boxdev", "boxdev"},
		{"https://workstation.example:3000", "workstation.example"},
		{"http://192.168.1.2:3000", "192.168.1.2"},
		{"http://[::1]:3000", "::1"},
	} {
		t.Run(tt.spec, func(t *testing.T) {
			s, err := detached(tt.spec)
			require.NoError(t, err)
			assert.Equal(t, tt.want, s.MachineName())
		})
	}
}

func TestSessionIdentity(t *testing.T) {
	for _, tt := range []struct{ spec, label, host string }{
		{"ssh://box", "ssh://box", "box"},
		{"ssh://user@box/", "ssh://user@box", "user@box"},
		{"local", "local", ""},
		{" local ", "local", ""},
		{"http://127.0.0.1:3773/", "http://127.0.0.1:3773", ""},
		{"https://T3.example.com/path", "https://t3.example.com", ""},
	} {
		t.Run(tt.spec, func(t *testing.T) {
			s, err := detached(tt.spec)
			require.NoError(t, err)
			assert.Equal(t, tt.label, s.Label())
			assert.Equal(t, tt.host, s.SSHHost())
			assert.Nil(t, s.Conn(), "a detached session holds no connection")
			assert.NoError(t, s.Close())
		})
	}
}

func TestSessionLocal(t *testing.T) {
	for _, tt := range []struct {
		spec  string
		local bool
	}{
		{"local", true},
		{"http://127.0.0.1:3773", true},
		{"http://localhost:3773", true},
		{"https://[::1]:3773", true},
		{"ssh://box", false},
		{"https://t3.example.com", false},
		{"http://192.168.1.2:3000", false},
	} {
		t.Run(tt.spec, func(t *testing.T) {
			s, err := detached(tt.spec)
			require.NoError(t, err)
			assert.Equal(t, tt.local, s.Local())
		})
	}
}

func TestParseServerValidatesSSHHosts(t *testing.T) {
	for _, h := range []string{"box", "user@box", "box.lan", "box:2222", "user@10.0.0.2", "my_box-1"} {
		_, err := parseServer("ssh://" + h)
		require.NoError(t, err, h)
	}
	for _, h := range []string{"", "-oProxyCommand=x", "a;b", "a b", "a$(x)", "a`x`", "a'b", "a/b", "a\\b", "a|b", "a\tb", "a\nb", "boxé"} {
		_, err := parseServer("ssh://" + h)
		require.ErrorContains(t, err, "invalid ssh host", h)
	}
	_, err := parseServer("ftp://x")
	require.ErrorContains(t, err, "unknown T3 server")
}
