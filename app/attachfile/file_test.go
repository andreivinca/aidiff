package attachfile

import (
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReader_Stat(t *testing.T) {
	var r Reader
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		return path
	}

	info, err := r.Stat(write("report.pdf", "%PDF-1.7 the rest"))
	require.NoError(t, err)
	assert.Equal(t, Info{Name: "report.pdf", Size: 17, MIME: "application/pdf"}, info)

	info, err = r.Stat(write("Makefile", "build:\n\tgo build\n"))
	require.NoError(t, err)
	assert.Equal(t, "text/plain", info.MIME, "no extension: the content tells, without its charset")

	info, err = r.Stat(write("shot.png", png))
	require.NoError(t, err)
	assert.Equal(t, Info{Name: "shot.png", Size: int64(len(png)), MIME: "image/png", Image: true}, info)

	info, err = r.Stat(write("fake.png", "just text"))
	require.NoError(t, err)
	assert.False(t, info.Image, "an image's name with other content is a file")

	info, err = r.Stat(write("logo.svg", "<svg/>"))
	require.NoError(t, err)
	assert.False(t, info.Image, "an image type that does not go as an image is a file")

	info, err = r.Stat(write("empty.txt", ""))
	require.NoError(t, err)
	assert.Zero(t, info.Size, "an empty file is described; the backend says whether it takes it")

	link := filepath.Join(dir, "latest")
	require.NoError(t, os.Symlink(filepath.Join(dir, "report.pdf"), link))
	info, err = r.Stat(link)
	require.NoError(t, err)
	assert.Equal(t, Info{Name: "latest", Size: 17, MIME: "application/pdf"}, info, "a symlink is followed to its file, and keeps its own name")

	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "notes.md"), []byte("# notes"), 0o600))
	info, err = r.Stat("~/notes.md")
	require.NoError(t, err)
	assert.Equal(t, "notes.md", info.Name)
}

func TestReader_StatRefuses(t *testing.T) {
	var r Reader
	dir := t.TempDir()

	_, err := r.Stat(filepath.Join(dir, "missing.txt"))
	require.ErrorIs(t, err, fs.ErrNotExist)

	_, err = r.Stat(dir)
	require.EqualError(t, err, dir+" is a folder; attach the files in it instead")

	broken := filepath.Join(dir, "broken")
	require.NoError(t, os.Symlink(filepath.Join(dir, "gone"), broken))
	_, err = r.Stat(broken)
	require.ErrorIs(t, err, fs.ErrNotExist, "a symlink to nothing names no file")

	fifo := filepath.Join(dir, "pipe")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	done := make(chan error, 1)
	go func() {
		_, err := r.Stat(fifo)
		done <- err
	}()
	select {
	case err = <-done:
		require.EqualError(t, err, fifo+" is a named pipe, not a regular file")
	case <-time.After(2 * time.Second):
		t.Fatal("looking at a named pipe waited for a writer")
	}
	_, _, err = r.Open(fifo)
	require.ErrorContains(t, err, "named pipe")

	_, err = r.Stat("/dev/null")
	require.EqualError(t, err, "/dev/null is a device, not a regular file")

	sock := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer l.Close()
	_, err = r.Stat(sock)
	require.EqualError(t, err, sock+" is a socket, not a regular file")

	if os.Getuid() != 0 { // root reads anything
		locked := filepath.Join(dir, "secret.txt")
		require.NoError(t, os.WriteFile(locked, []byte("x"), 0o000))
		_, err = r.Stat(locked)
		require.ErrorIs(t, err, fs.ErrPermission, "a file this user cannot read")
	}
}

func TestReader_Open(t *testing.T) {
	var r Reader
	path := filepath.Join(t.TempDir(), "log.txt")
	require.NoError(t, os.WriteFile(path, []byte("first"), 0o600))
	rc, size, err := r.Open(path)
	require.NoError(t, err)
	assert.Equal(t, int64(5), size)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0) //nolint:gosec // a test file in a temporary directory
	require.NoError(t, err)
	_, err = f.WriteString(" and more")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "first", string(data), "no more than the size it had when opened")
	require.NoError(t, rc.Close())

	require.NoError(t, os.Remove(path))
	_, _, err = r.Open(path)
	require.ErrorIs(t, err, fs.ErrNotExist, "a file deleted since it was attached")
}
