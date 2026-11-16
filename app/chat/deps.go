package chat

import (
	"context"
	"errors"
)

// Clipboard is what ctrl+v pastes from and a selection is copied to.
type Clipboard interface {
	Read(ctx context.Context) (Clip, error)
	Write(ctx context.Context, text string) error
}

// Clip is what the clipboard held: an image, or text. Both are empty when
// the clipboard is.
type Clip struct {
	Image []byte // set when the clipboard holds an image
	MIME  string // the image's media type
	Text  string // set when it holds text instead
}

// ImageFiles reads the image files the user attaches.
type ImageFiles interface {
	// IsImage reports whether path names an image file, without reading it.
	IsImage(path string) bool
	// Read loads the image file at path. check is asked about the file's
	// name and size before it is read, and turns it down with its error.
	Read(path string, check func(name string, size int64) error) (Image, error)
}

var (
	errNoClipboard = errors.New("no clipboard is available")
	errNoFiles     = errors.New("image files cannot be attached here")
)

// noClipboard is the clipboard of a pane given none.
type noClipboard struct{}

func (noClipboard) Read(context.Context) (Clip, error) {
	return Clip{}, errNoClipboard
}

func (noClipboard) Write(context.Context, string) error {
	return errNoClipboard
}

// noFiles are the image files of a pane given none: there are none.
type noFiles struct{}

func (noFiles) IsImage(string) bool {
	return false
}

func (noFiles) Read(string, func(string, int64) error) (Image, error) {
	return Image{}, errNoFiles
}
