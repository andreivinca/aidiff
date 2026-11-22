package chat

import (
	"context"
	"errors"
	"io"
)

// Clipboard is what ctrl+v pastes from and a selection is copied to.
type Clipboard interface {
	Read(ctx context.Context) (Clip, error)
	Write(ctx context.Context, text string) error
}

// Clip is what the clipboard held: files copied in a file manager, an
// image, or text. All are empty when the clipboard is.
type Clip struct {
	Files []string // set when the clipboard holds copied files: their paths, in order
	Image []byte   // set when the clipboard holds an image
	MIME  string   // the image's media type
	Text  string   // set when it holds text instead
}

// Files reads the files the user attaches. A path may start with "~/" for
// the home directory.
type Files interface {
	// Stat checks the file at path for attaching and describes it, without
	// reading it whole: a file that is no regular file (a symlink is
	// followed), or cannot be read, is turned down.
	Stat(path string) (FileInfo, error)
	// ReadImage loads the image file at path. check is asked about the
	// file's name and size before it is read, and turns it down with its
	// error.
	ReadImage(path string, check func(name string, size int64) error) (Image, error)
	// Open opens the file at path to send it, checked again as Stat checks
	// it: its content, no more than its size now, and that size.
	Open(path string) (io.ReadCloser, int64, error)
}

// FileInfo is what Files.Stat tells of a file.
type FileInfo struct {
	Name  string // its base name
	Size  int64
	MIME  string // its media type
	Image bool   // an image ReadImage takes, by its name and content
}

var (
	errNoClipboard = errors.New("no clipboard is available")
	errNoFiles     = errors.New("files cannot be attached here")
)

// noClipboard is the clipboard of a pane given none.
type noClipboard struct{}

func (noClipboard) Read(context.Context) (Clip, error) {
	return Clip{}, errNoClipboard
}

func (noClipboard) Write(context.Context, string) error {
	return errNoClipboard
}

// noFiles are the files of a pane given none: there are none.
type noFiles struct{}

func (noFiles) Stat(string) (FileInfo, error) {
	return FileInfo{}, errNoFiles
}

func (noFiles) ReadImage(string, func(string, int64) error) (Image, error) {
	return Image{}, errNoFiles
}

func (noFiles) Open(string) (io.ReadCloser, int64, error) {
	return nil, 0, errNoFiles
}
