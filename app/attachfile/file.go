package attachfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// sniffBytes is how much of a file its media type is told from, when its
// name does not tell it.
const sniffBytes = 512

// Info is what attaching a file takes to know of it, read without reading
// the file whole.
type Info struct {
	Name  string // its base name
	Size  int64
	MIME  string // by its extension, else by its first bytes; no parameters
	Image bool   // named and made as an image of a type that can be attached as one
}

// Stat checks the file at path for attaching and describes it; a leading
// "~/" stands for the home directory. It must be a regular file this user
// can read: a symlink is followed to one, and a folder, a named pipe, a
// socket or a device is turned down, as is a file that cannot be opened.
// Only its first bytes are read.
func (r Reader) Stat(path string) (Info, error) {
	f, info, err := r.open(path)
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Info{}, fmt.Errorf("attach: read %s: %w", path, err)
	}
	sniffed := baseType(http.DetectContentType(head[:n]))
	media := baseType(mime.TypeByExtension(filepath.Ext(path)))
	if media == "" {
		media = sniffed
	}
	return Info{Name: filepath.Base(path), Size: info.Size(), MIME: media, Image: r.IsImage(path) && r.attachable(sniffed)}, nil
}

// Open opens the file at path to send it, checked again as Stat checks it:
// its content, no more than its size now, and that size.
func (r Reader) Open(path string) (io.ReadCloser, int64, error) {
	f, info, err := r.open(path)
	if err != nil {
		return nil, 0, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(f, info.Size()), f}, info.Size(), nil
}

// open opens the regular file at path for reading. The path is looked at
// before it is opened, since opening a named pipe waits for a writer, and
// the file opened is looked at again, as the path may name another file by
// then; the open itself does not wait either way.
func (r Reader) open(path string) (*os.File, fs.FileInfo, error) {
	full, err := r.expandHome(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(full) // follows a symlink
	if err != nil {
		return nil, nil, fmt.Errorf("attach: %w", err)
	}
	if err = r.regular(path, info); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(full, os.O_RDONLY|syscall.O_NONBLOCK, 0) //nolint:gosec // the user's own file, named by them
	if err != nil {
		return nil, nil, fmt.Errorf("attach: %w", err)
	}
	if info, err = f.Stat(); err == nil {
		err = r.regular(path, info)
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// regular turns down a file at path that is not a regular one, saying what
// it is.
func (Reader) regular(path string, info fs.FileInfo) error {
	mode := info.Mode()
	switch {
	case mode.IsRegular():
		return nil
	case mode.IsDir():
		return fmt.Errorf("%s is a folder; attach the files in it instead", path)
	case mode&fs.ModeNamedPipe != 0:
		return fmt.Errorf("%s is a named pipe, not a regular file", path)
	case mode&fs.ModeSocket != 0:
		return fmt.Errorf("%s is a socket, not a regular file", path)
	case mode&fs.ModeDevice != 0:
		return fmt.Errorf("%s is a device, not a regular file", path)
	}
	return fmt.Errorf("%s is not a regular file", path)
}

// baseType is a media type without its parameters, "" when there is none.
func baseType(mimeType string) string {
	t, _, _ := strings.Cut(mimeType, ";")
	return strings.ToLower(strings.TrimSpace(t))
}
