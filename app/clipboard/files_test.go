package clipboard

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileList(t *testing.T) {
	tests := []struct {
		name, data string
		want       []string
	}{
		{name: "one file", data: "file:///home/me/report.pdf", want: []string{"/home/me/report.pdf"}},
		{name: "encoded spaces and unicode", data: "file:///home/me/My%20Files/r%C3%A9sum%C3%A9%20%E2%9C%93.pdf\r\n",
			want: []string{"/home/me/My Files/résumé ✓.pdf"}},
		{name: "raw unicode", data: "file:///tmp/日本語.txt", want: []string{"/tmp/日本語.txt"}},
		{name: "plus and percent stay as meant", data: "file:///tmp/a+b%2525.txt", want: []string{"/tmp/a+b%25.txt"}},
		{name: "several, CRLF, in order", data: "file:///a/one.txt\r\nfile:///b/two.txt\r\nfile:///c/three.txt\r\n",
			want: []string{"/a/one.txt", "/b/two.txt", "/c/three.txt"}},
		{name: "comments are no files", data: "# copied by a file manager\r\nfile:///a/one.txt\r\n#file:///b/not.txt\r\n", want: []string{"/a/one.txt"}},
		{name: "localhost and this host", data: "file://localhost/a.txt\nfile://devbox/b.txt\nfile://DEVBOX/c.txt", want: []string{"/a.txt", "/b.txt", "/c.txt"}},
		{name: "another host's file is not here", data: "file://otherbox/a.txt\nfile:///b.txt", want: []string{"/b.txt"}},
		{name: "web addresses are no files", data: "https://example.com/a.png\nfile:///c.png\nmailto:x@y", want: []string{"/c.png"}},
		{name: "gnome's copy line", data: "copy\nfile:///home/me/a.txt\nfile:///home/me/b.txt", want: []string{"/home/me/a.txt", "/home/me/b.txt"}},
		{name: "kde's cut line", data: "cut\nfile:///home/me/a.txt", want: []string{"/home/me/a.txt"}},
		{name: "a single-slash file URI", data: "file:/home/me/a.txt", want: []string{"/home/me/a.txt"}},
		{name: "relative and broken", data: "file:a.txt\nfile:///bad%zz\n\n   \n", want: nil},
		{name: "empty", data: "", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, fileList(tc.data, "devbox"))
		})
	}
	assert.Empty(t, fileList("file://devbox/a.txt", ""), "without its own name a machine takes no named host")
}

func TestPickFileList(t *testing.T) {
	assert.Equal(t, "text/uri-list", pickFileList("x-special/gnome-copied-files\ntext/uri-list\nUTF8_STRING\n"), "the standard list first")
	assert.Equal(t, "x-special/gnome-copied-files", pickFileList("TARGETS x-special/gnome-copied-files UTF8_STRING"))
	assert.Equal(t, "x-special/KDE-copied-files", pickFileList("x-special/KDE-copied-files\ntext/plain"), "spelled as offered")
	assert.Empty(t, pickFileList("image/png\ntext/plain\n"))
}

func TestReadPrefersCopiedFiles(t *testing.T) {
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
			name: "nautilus copying an image file: the file, not its pixels", env: wayland, programs: both,
			reply: answers(map[string]string{
				"wl-paste --list-types":                        "x-special/gnome-copied-files\ntext/uri-list\ntext/plain;charset=utf-8\nimage/png\n",
				"wl-paste --type text/uri-list":                "file:///home/me/Pictures/cat%20photo.png\r\n",
				"wl-paste --type image/png":                    "PNG",
				"wl-paste --type x-special/gnome-copied-files": "copy\nfile:///nope",
			}),
			want:    Content{Files: []string{"/home/me/Pictures/cat photo.png"}},
			wantRan: []string{"wl-paste --list-types", "wl-paste --type text/uri-list"},
		},
		{
			name: "several files on x11, gnome's list only", env: x11, programs: both,
			reply: answers(map[string]string{
				"xclip -selection clipboard -t TARGETS -o":                      "TARGETS\nx-special/gnome-copied-files\nUTF8_STRING\n",
				"xclip -selection clipboard -t x-special/gnome-copied-files -o": "copy\nfile:///a/report.pdf\nfile:///a/notes.txt",
			}),
			want:    Content{Files: []string{"/a/report.pdf", "/a/notes.txt"}},
			wantRan: []string{"xclip -selection clipboard -t TARGETS -o", "xclip -selection clipboard -t x-special/gnome-copied-files -o"},
		},
		{
			name: "a screenshot: the image alone", env: wayland, programs: both,
			reply: answers(map[string]string{
				"wl-paste --list-types":     "image/png\n",
				"wl-paste --type image/png": "PNG",
			}),
			want:    Content{Image: []byte("PNG"), Mime: "image/png"},
			wantRan: []string{"wl-paste --list-types", "wl-paste --type image/png"},
		},
		{
			name: "a browser's copied image: its web address is no file, the pixels are", env: x11, programs: both,
			reply: answers(map[string]string{
				"xclip -selection clipboard -t TARGETS -o":       "TARGETS\ntext/uri-list\nimage/png\ntext/html\n",
				"xclip -selection clipboard -t text/uri-list -o": "https://example.com/cat.png",
				"xclip -selection clipboard -t image/png -o":     "PNG",
			}),
			want: Content{Image: []byte("PNG"), Mime: "image/png"},
			wantRan: []string{"xclip -selection clipboard -t TARGETS -o", "xclip -selection clipboard -t text/uri-list -o",
				"xclip -selection clipboard -t image/png -o"},
		},
		{
			name: "copied text stays text", env: wayland, programs: both,
			reply: answers(map[string]string{
				"wl-paste --list-types": "text/plain;charset=utf-8\nUTF8_STRING\n",
				"wl-paste --no-newline": "/home/me/report.pdf",
			}),
			want:    Content{Text: "/home/me/report.pdf"},
			wantRan: []string{"wl-paste --list-types", "wl-paste --no-newline"},
		},
		{
			name: "a list gone empty meanwhile falls through", env: wayland, programs: both,
			reply: func(c command) ([]byte, error) {
				switch argv(c) {
				case "wl-paste --list-types":
					return []byte("text/uri-list\ntext/plain\n"), nil
				case "wl-paste --type text/uri-list":
					return nil, &exitError{name: c.name, stderr: "No suitable type of content copied", err: errors.New("exit status 1")}
				case "wl-paste --no-newline":
					return []byte("text"), nil
				}
				return nil, errors.New("unexpected " + argv(c))
			},
			want:    Content{Text: "text"},
			wantRan: []string{"wl-paste --list-types", "wl-paste --type text/uri-list", "wl-paste --no-newline"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSystem{env: tc.env, programs: tc.programs, reply: tc.reply, host: "devbox"}
			got, err := f.system().read(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantRan, f.argvs())
		})
	}

	f := &fakeSystem{env: wayland, programs: both, reply: func(c command) ([]byte, error) {
		if argv(c) == "wl-paste --list-types" {
			return []byte("text/uri-list\n"), nil
		}
		return nil, &exitError{name: c.name, stderr: "broken pipe", err: errors.New("exit status 1")}
	}}
	_, err := f.system().read(t.Context())
	require.ErrorContains(t, err, "broken pipe", "a list that cannot be read is reported, not taken for text")
}

// TestReadRunsTheTools reads copied files through real programs named
// wl-paste and xclip, scripts that answer as the tools do, so the commands
// run, and their output read, as on a desktop.
func TestReadRunsTheTools(t *testing.T) {
	dir := t.TempDir()
	writeTool(t, dir, "wl-paste", `case "$*" in
"--list-types") printf 'x-special/gnome-copied-files\ntext/uri-list\nimage/png\n' ;;
"--type text/uri-list") printf 'file:///tmp/My%%20Report.pdf\r\nfile:///tmp/notes.txt\r\n' ;;
*) echo "unexpected $*" >&2; exit 2 ;;
esac`)
	writeTool(t, dir, "xclip", `case "$*" in
"-selection clipboard -t TARGETS -o") printf 'TARGETS\nimage/png\n' ;;
"-selection clipboard -t image/png -o") printf 'PNG' ;;
*) echo "unexpected $*" >&2; exit 2 ;;
esac`)
	t.Setenv("PATH", dir)
	env := map[string]string{"WAYLAND_DISPLAY": "wayland-0"}
	s := system{getenv: func(k string) string { return env[k] }, lookPath: exec.LookPath, run: runCommand, hostname: os.Hostname}
	got, err := s.read(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Content{Files: []string{"/tmp/My Report.pdf", "/tmp/notes.txt"}}, got)

	env = map[string]string{"DISPLAY": ":0"}
	got, err = s.read(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Content{Image: []byte("PNG"), Mime: "image/png"}, got, "no list offered: the image")
}

// writeTool makes an executable shell script named name in dir.
func writeTool(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700)) //nolint:gosec // a test script
}
