package imagefile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anySize takes a file of any size.
func anySize(string, int64) error {
	return nil
}

func TestReader_IsImage(t *testing.T) {
	var r Reader
	assert.True(t, r.IsImage("/tmp/shot.PNG"), "the extension is matched in any case")
	assert.True(t, r.IsImage("photo.jpeg"))
	assert.False(t, r.IsImage("notes.txt"))
	assert.False(t, r.IsImage("png"))
}

func TestReader_Read(t *testing.T) {
	var r Reader
	dir := t.TempDir()
	file := filepath.Join(dir, "shot.jpg")
	require.NoError(t, os.WriteFile(file, []byte("jpegdata"), 0o600))

	img, err := r.Read(file, anySize)
	require.NoError(t, err)
	assert.Equal(t, Image{Name: "shot.jpg", MIME: "image/jpeg", Data: []byte("jpegdata")}, img)

	_, err = r.Read(filepath.Join(dir, "notes.txt"), anySize)
	require.ErrorIs(t, err, ErrNotImage)
	assert.Contains(t, err.Error(), "notes.txt")

	_, err = r.Read(filepath.Join(dir, "missing.png"), anySize)
	require.ErrorIs(t, err, fs.ErrNotExist)

	require.NoError(t, os.Mkdir(filepath.Join(dir, "folder.png"), 0o700))
	_, err = r.Read(filepath.Join(dir, "folder.png"), anySize)
	require.ErrorContains(t, err, "not a regular file")
}

func TestReader_ReadRefusesFilesUnread(t *testing.T) {
	file := filepath.Join(t.TempDir(), "huge.png")
	f, err := os.Create(file) //nolint:gosec // a test file in a temporary directory
	require.NoError(t, err)
	require.NoError(t, f.Truncate(1<<30)) // sparse: the size is there, the data is not
	require.NoError(t, f.Close())
	tooLarge := errors.New("too large")
	var asked string
	_, err = Reader{}.Read(file, func(name string, size int64) error {
		asked = fmt.Sprintf("%s %d", name, size)
		return tooLarge
	})
	require.ErrorIs(t, err, tooLarge)
	assert.Equal(t, "huge.png 1073741824", asked, "the check is asked about the file before it is read")
}

func TestReader_ReadExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "cat.gif"), []byte("GIF89a"), 0o600))
	img, err := Reader{}.Read("~/cat.gif", anySize)
	require.NoError(t, err)
	assert.Equal(t, "cat.gif", img.Name)
	assert.Equal(t, "image/gif", img.MIME)
}
