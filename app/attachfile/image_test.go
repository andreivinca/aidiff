package attachfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jpeg and png are content that starts as those images do.
const (
	jpeg = "\xFF\xD8\xFF\xE0jpegdata"
	png  = "\x89PNG\r\n\x1a\npngdata"
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

func TestReader_ReadImage(t *testing.T) {
	var r Reader
	dir := t.TempDir()
	file := filepath.Join(dir, "shot.jpg")
	require.NoError(t, os.WriteFile(file, []byte(jpeg), 0o600))

	img, err := r.ReadImage(file, anySize)
	require.NoError(t, err)
	assert.Equal(t, Image{Name: "shot.jpg", MIME: "image/jpeg", Data: []byte(jpeg)}, img)

	_, err = r.ReadImage(filepath.Join(dir, "notes.txt"), anySize)
	require.ErrorIs(t, err, ErrNotImage)
	assert.Contains(t, err.Error(), "notes.txt")

	_, err = r.ReadImage(filepath.Join(dir, "missing.png"), anySize)
	require.ErrorIs(t, err, fs.ErrNotExist)

	require.NoError(t, os.Mkdir(filepath.Join(dir, "folder.png"), 0o700))
	_, err = r.ReadImage(filepath.Join(dir, "folder.png"), anySize)
	require.ErrorContains(t, err, "is a folder")
}

func TestReader_ReadImageRefusesFilesUnread(t *testing.T) {
	file := filepath.Join(t.TempDir(), "huge.png")
	f, err := os.Create(file) //nolint:gosec // a test file in a temporary directory
	require.NoError(t, err)
	require.NoError(t, f.Truncate(1<<30)) // sparse: the size is there, the data is not
	require.NoError(t, f.Close())
	tooLarge := errors.New("too large")
	var asked string
	_, err = Reader{}.ReadImage(file, func(name string, size int64) error {
		asked = fmt.Sprintf("%s %d", name, size)
		return tooLarge
	})
	require.ErrorIs(t, err, tooLarge)
	assert.Equal(t, "huge.png 1073741824", asked, "the check is asked about the file before it is read")
}

func TestReader_ReadImageExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "cat.gif"), []byte("GIF89a"), 0o600))
	img, err := Reader{}.ReadImage("~/cat.gif", anySize)
	require.NoError(t, err)
	assert.Equal(t, "cat.gif", img.Name)
	assert.Equal(t, "image/gif", img.MIME)
}

func TestReader_ReadImageChecksTheContent(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "notes.png")
	require.NoError(t, os.WriteFile(fake, []byte("just some text"), 0o600))
	_, err := Reader{}.ReadImage(fake, anySize)
	require.ErrorIs(t, err, ErrNotImage, "the extension alone does not make an image")
	assert.Contains(t, err.Error(), "text/plain")

	misnamed := filepath.Join(dir, "shot.jpg")
	require.NoError(t, os.WriteFile(misnamed, []byte(png), 0o600))
	img, err := Reader{}.ReadImage(misnamed, anySize)
	require.NoError(t, err)
	assert.Equal(t, "image/png", img.MIME, "the media type is the content's")
}

func TestReader_ReadImageNoMoreThanChecked(t *testing.T) {
	file := filepath.Join(t.TempDir(), "shot.png")
	require.NoError(t, os.WriteFile(file, []byte(png), 0o600))
	_, err := Reader{}.ReadImage(file, func(string, int64) error {
		f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0) //nolint:gosec // a test file in a temporary directory
		require.NoError(t, err)
		_, err = f.WriteString(strings.Repeat("x", 1<<20)) // grows after its size was approved
		require.NoError(t, err)
		require.NoError(t, f.Close())
		return nil
	})
	require.ErrorContains(t, err, "grew while it was read")
}
