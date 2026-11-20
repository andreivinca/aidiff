package clipboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrite_WSL(t *testing.T) {
	f := &fakeSystem{programs: []string{"clip.exe"}, reply: answers(map[string]string{"clip.exe": ""})}
	require.NoError(t, f.system().write(t.Context(), "aé"))
	assert.Equal(t, []string{"clip.exe"}, f.argvs(), "no Linux clipboard tool: Windows' own")
	assert.Equal(t, []string{"\xff\xfea\x00\xe9\x00"}, f.stdin, "UTF-16LE behind a byte-order mark")

	f = &fakeSystem{env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, programs: []string{"wl-copy", "clip.exe"},
		reply: answers(map[string]string{"wl-copy": ""})}
	require.NoError(t, f.system().write(t.Context(), "x"))
	assert.Equal(t, []string{"wl-copy"}, f.argvs(), "a Linux clipboard tool comes first")

	f = &fakeSystem{}
	require.ErrorIs(t, f.system().write(t.Context(), "x"), ErrNoTool, "neither")
}
