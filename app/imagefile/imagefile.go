// Package imagefile reads the image files a user names, to attach them to a
// message for an agent.
package imagefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotImage means a file's extension is not one of an image type that can
// be attached.
var ErrNotImage = errors.New("not an image file")

// mimeTypes maps the extensions of attachable image files to their media
// types.
var mimeTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
}

// Image is an image file's name, media type and content.
type Image struct {
	Name string
	MIME string
	Data []byte
}

// Reader reads image files on this machine.
type Reader struct{}

// IsImage reports whether path names an image file, by its extension alone.
func (r Reader) IsImage(path string) bool {
	return r.mimeType(path) != ""
}

// Read loads the image file at path; a leading "~/" stands for the home
// directory. check is asked about the file's name and size before it is
// read, and turns it down with its error.
func (r Reader) Read(path string, check func(name string, size int64) error) (Image, error) {
	mime := r.mimeType(path)
	if mime == "" {
		return Image{}, fmt.Errorf("%w: %s", ErrNotImage, path)
	}
	path, err := r.expandHome(path)
	if err != nil {
		return Image{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Image{}, fmt.Errorf("attach: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Image{}, fmt.Errorf("attach: %s is not a regular file", path)
	}
	name := filepath.Base(path)
	if err = check(name, info.Size()); err != nil {
		return Image{}, fmt.Errorf("attach: %w", err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // the user's own file, named by them
	if err != nil {
		return Image{}, fmt.Errorf("attach: %w", err)
	}
	return Image{Name: name, MIME: mime, Data: data}, nil
}

// mimeType is the media type of an image file named path, or "".
func (Reader) mimeType(path string) string {
	return mimeTypes[strings.ToLower(filepath.Ext(path))]
}

// expandHome replaces a leading "~/" with the home directory.
func (Reader) expandHome(path string) (string, error) {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("attach %s: %w", path, err)
	}
	return filepath.Join(home, rest), nil
}
