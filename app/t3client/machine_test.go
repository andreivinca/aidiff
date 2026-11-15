package t3client

import (
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func TestSessionMachineName(t *testing.T) {
	local, _ := os.Hostname()
	for _, tt := range []struct{ label, want string }{
		{"local", local},
		{"ssh://boxdev", "boxdev"},
		{"ssh://andrei@boxdev", "boxdev"},
		{"https://workstation.example:3000", "workstation.example"},
		{"http://192.168.1.2:3000", "192.168.1.2"},
		{"http://[::1]:3000", "::1"},
		{"", "remote"},
	} {
		t.Run(tt.label, func(t *testing.T) { assert.Equal(t, tt.want, (&Session{Label: tt.label}).MachineName()) })
	}
}

func TestSessionSSHHost(t *testing.T) {
	for label, want := range map[string]string{
		"ssh://box":              "box",
		"ssh://user@box/":        "user@box",
		"local":                  "",
		"http://127.0.0.1:3773":  "",
		"https://t3.example.com": "",
	} {
		t.Run(label, func(t *testing.T) { assert.Equal(t, want, (&Session{Label: label}).SSHHost()) })
	}
}
