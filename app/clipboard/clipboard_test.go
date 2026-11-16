package clipboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPickImage(t *testing.T) {
	assert.Equal(t, "image/png", pickImage("text/plain\nimage/jpeg\nimage/png\n"), "png wins")
	assert.Equal(t, "image/jpeg", pickImage("TEXT image/jpeg image/webp"), "else the first image type")
	assert.Empty(t, pickImage("text/plain\nUTF8_STRING\n"))
	assert.Empty(t, pickImage(""))
}

// fakeSystem is a machine with the given environment and programs, whose
// program runs are answered by reply.
type fakeSystem struct {
	env      map[string]string
	programs []string
	reply    func(c command) ([]byte, error)
	ran      []command
	stdin    []string
}

func (f *fakeSystem) system() system {
	return system{
		getenv: func(key string) string {
			return f.env[key]
		},
		lookPath: func(file string) (string, error) {
			for _, p := range f.programs {
				if p == file {
					return "/usr/bin/" + p, nil
				}
			}
			return "", exec.ErrNotFound
		},
		run: func(_ context.Context, c command) ([]byte, error) {
			f.ran = append(f.ran, c)
			if c.stdin != nil {
				b, err := io.ReadAll(c.stdin)
				if err != nil {
					return nil, fmt.Errorf("read stdin: %w", err)
				}
				f.stdin = append(f.stdin, string(b))
			}
			return f.reply(c)
		},
	}
}

// argv is how a command reads on a command line.
func argv(c command) string {
	return strings.Join(append([]string{c.name}, c.args...), " ")
}

func (f *fakeSystem) argvs() []string {
	out := make([]string, len(f.ran))
	for i, c := range f.ran {
		out[i] = argv(c)
	}
	return out
}

// answers replies per command line; unknown lines fail.
func answers(byArgv map[string]string) func(c command) ([]byte, error) {
	return func(c command) ([]byte, error) {
		out, ok := byArgv[argv(c)]
		if !ok {
			return nil, errors.New("unexpected " + argv(c))
		}
		return []byte(out), nil
	}
}

// emptyClipboard fails the way the reader does on an empty clipboard.
func emptyClipboard(stderr string) func(c command) ([]byte, error) {
	return func(c command) ([]byte, error) {
		return nil, &exitError{name: c.name, stderr: stderr, err: errors.New("exit status 1")}
	}
}

func TestRead(t *testing.T) {
	wayland := map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}
	x11 := map[string]string{"DISPLAY": ":0"}
	both := []string{"wl-paste", "wl-copy", "xclip"}

	tests := []struct {
		name     string
		env      map[string]string
		programs []string
		reply    func(c command) ([]byte, error)
		want     Content
		wantRan  []string
	}{
		{
			name: "wayland image", env: wayland, programs: both,
			reply: answers(map[string]string{
				"wl-paste --list-types":     "text/plain\nimage/png\n",
				"wl-paste --type image/png": "PNG",
			}),
			want:    Content{Image: []byte("PNG"), Mime: "image/png"},
			wantRan: []string{"wl-paste --list-types", "wl-paste --type image/png"},
		},
		{
			name: "wayland text", env: wayland, programs: both,
			reply: answers(map[string]string{
				"wl-paste --list-types": "text/plain\n",
				"wl-paste --no-newline": "hello",
			}),
			want:    Content{Text: "hello"},
			wantRan: []string{"wl-paste --list-types", "wl-paste --no-newline"},
		},
		{
			name: "x11, also under wayland without wl-paste", env: wayland, programs: []string{"xclip"},
			reply: answers(map[string]string{
				"xclip -selection clipboard -t TARGETS -o":    "TARGETS\nimage/jpeg\n",
				"xclip -selection clipboard -t image/jpeg -o": "JPG",
			}),
			want:    Content{Image: []byte("JPG"), Mime: "image/jpeg"},
			wantRan: []string{"xclip -selection clipboard -t TARGETS -o", "xclip -selection clipboard -t image/jpeg -o"},
		},
		{
			name: "empty wayland clipboard", env: wayland, programs: both,
			reply:   emptyClipboard("Nothing is copied"),
			wantRan: []string{"wl-paste --list-types"},
		},
		{
			name: "empty x11 clipboard", env: x11, programs: both,
			reply:   emptyClipboard("Error: target TARGETS not available"),
			wantRan: []string{"xclip -selection clipboard -t TARGETS -o"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSystem{env: tc.env, programs: tc.programs, reply: tc.reply}
			got, err := f.system().read(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantRan, f.argvs())
			for _, c := range f.ran {
				assert.Equal(t, int64(maxReadBytes), c.limit, "every read is capped")
			}
		})
	}
}

func TestReadFailures(t *testing.T) {
	f := &fakeSystem{env: map[string]string{"DISPLAY": ":0"}, programs: []string{"xclip"},
		reply: emptyClipboard("Error: Can't open display: :0")}
	_, err := f.system().read(t.Context())
	require.Error(t, err, "a failure that is not an empty clipboard is reported")
	assert.Contains(t, err.Error(), "Can't open display")

	f = &fakeSystem{env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, programs: []string{"xclip"}}
	_, err = f.system().read(t.Context())
	require.ErrorIs(t, err, ErrNoTool, "xclip is no use without an X display")
	assert.Empty(t, f.ran)
}

func TestWrite(t *testing.T) {
	f := &fakeSystem{env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, programs: []string{"wl-paste", "wl-copy"},
		reply: answers(map[string]string{"wl-copy": ""})}
	require.NoError(t, f.system().write(t.Context(), "copied"))
	assert.Equal(t, []string{"wl-copy"}, f.argvs())
	assert.Equal(t, []string{"copied"}, f.stdin)
	assert.Zero(t, f.ran[0].limit, "a writer's output is not read: its server process keeps it open")

	f = &fakeSystem{env: map[string]string{"DISPLAY": ":0"}, programs: []string{"xclip"},
		reply: answers(map[string]string{"xclip -selection clipboard": ""})}
	require.NoError(t, f.system().write(t.Context(), "x"))
	assert.Equal(t, []string{"xclip -selection clipboard"}, f.argvs())

	f = &fakeSystem{env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, programs: []string{"wl-paste"}}
	require.ErrorIs(t, f.system().write(t.Context(), "x"), ErrNoTool, "wl-paste alone cannot write")
}

func TestRunCommand(t *testing.T) {
	out, err := runCommand(t.Context(), command{name: "sh", args: []string{"-c", "printf hello"}, limit: 10})
	require.NoError(t, err)
	assert.Equal(t, "hello", string(out))

	_, err = runCommand(t.Context(), command{name: "sh", args: []string{"-c", "head -c 100 /dev/zero"}, limit: 10})
	require.Error(t, err, "output beyond the limit is refused")

	_, err = runCommand(t.Context(), command{name: "sh", args: []string{"-c", "echo nope >&2; exit 1"}, limit: 10})
	var failed *exitError
	require.ErrorAs(t, err, &failed)
	assert.Equal(t, "nope", failed.stderr, "a failing program's message is kept")

	out, err = runCommand(t.Context(), command{name: "sh", args: []string{"-c", "cat >/dev/null"}, stdin: strings.NewReader("x")})
	require.NoError(t, err)
	assert.Nil(t, out, "a writer's output is not read")
}
