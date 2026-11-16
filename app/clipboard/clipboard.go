// Package clipboard reads and writes the system clipboard, which a terminal
// cannot paste from when it holds an image. Linux only: wl-clipboard on
// Wayland, xclip on X11.
package clipboard

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// maxReadBytes caps what one read takes from the clipboard.
const maxReadBytes = 64 << 20

// Content is what the clipboard held: an image, or text. Both are empty when
// the clipboard is.
type Content struct {
	Image []byte // set when the clipboard holds an image
	Mime  string // the image's media type
	Text  string // set when it holds text instead
}

// ErrNoTool means no clipboard tool is available on this machine.
var ErrNoTool = errors.New("no clipboard tool: install wl-clipboard (Wayland) or xclip (X11)")

// System is this machine's clipboard.
type System struct{}

// Read returns the clipboard's image when it holds one, else its text.
func (System) Read(ctx context.Context) (Content, error) {
	return local.read(ctx)
}

// Write copies text to the clipboard.
func (System) Write(ctx context.Context, text string) error {
	return local.write(ctx, text)
}

// local is this machine's clipboard.
var local = system{getenv: os.Getenv, lookPath: exec.LookPath, run: runCommand}

// system is where the clipboard tools are looked up and run; tests replace
// all three.
type system struct {
	getenv   func(key string) string
	lookPath func(file string) (string, error)
	run      func(ctx context.Context, c command) ([]byte, error)
}

// read lists the clipboard's offered types, then fetches the best image type,
// or the text when there is no image.
func (s system) read(ctx context.Context) (Content, error) {
	t, ok := s.pick(func(t tool) string {
		return t.reader
	})
	if !ok {
		return Content{}, ErrNoTool
	}
	types, err := s.run(ctx, t.readCommand(t.listTypes))
	if t.isEmpty(err) {
		return Content{}, nil
	}
	if err != nil {
		return Content{}, err
	}
	if mime := pickImage(string(types)); mime != "" {
		image, imageErr := s.run(ctx, t.readCommand(t.readType(mime)))
		if imageErr != nil {
			return Content{}, imageErr
		}
		return Content{Image: image, Mime: mime}, nil
	}
	text, err := s.run(ctx, t.readCommand(t.readText))
	if t.isEmpty(err) {
		return Content{}, nil
	}
	if err != nil {
		return Content{}, err
	}
	return Content{Text: string(text)}, nil
}

// write copies text to the clipboard.
func (s system) write(ctx context.Context, text string) error {
	t, ok := s.pick(func(t tool) string {
		return t.writer
	})
	if !ok {
		return ErrNoTool
	}
	_, err := s.run(ctx, command{name: t.writer, args: t.writeText, stdin: strings.NewReader(text)})
	return err
}

// pick returns the tool for the running display server whose program (as
// chosen by program) is installed.
func (s system) pick(program func(tool) string) (tool, bool) {
	for _, t := range tools {
		if s.getenv(t.display) == "" {
			continue
		}
		if _, err := s.lookPath(program(t)); err == nil {
			return t, true
		}
	}
	return tool{}, false
}

// pickImage chooses the image type to read from the offered ones: PNG when
// there is one, else the first image type.
func pickImage(types string) string {
	first := ""
	for t := range strings.FieldsSeq(types) {
		t = strings.ToLower(t)
		switch {
		case t == "image/png":
			return t
		case strings.HasPrefix(t, "image/") && first == "":
			first = t
		}
	}
	return first
}
