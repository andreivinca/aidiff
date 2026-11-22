// Package clipboard reads and writes the system clipboard, which a terminal
// cannot paste from when it holds an image or files copied in a file
// manager. Linux only: wl-clipboard on Wayland, xclip on X11, and for
// writing under WSL, Windows' clip.exe.
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

// Content is what the clipboard held: files a file manager copied, an
// image, or text. All are empty when the clipboard is.
type Content struct {
	Files []string // set when the clipboard holds copied files: their paths on this machine, in order
	Image []byte   // set when the clipboard holds an image
	Mime  string   // the image's media type
	Text  string   // set when it holds text instead
}

// ErrNoTool means no clipboard tool is available on this machine.
var ErrNoTool = errors.New("no clipboard tool: install wl-clipboard (Wayland) or xclip (X11)")

// System is this machine's clipboard.
type System struct{}

// Read returns the files the clipboard holds copied, else its image, else
// its text. Copying files in a file manager offers their list, an image
// file's included, while a screenshot offers the image alone and copied
// text the text alone, so the list goes first.
func (System) Read(ctx context.Context) (Content, error) {
	return local.read(ctx)
}

// Write copies text to the clipboard.
func (System) Write(ctx context.Context, text string) error {
	return local.write(ctx, text)
}

// local is this machine's clipboard.
var local = system{getenv: os.Getenv, lookPath: exec.LookPath, run: runCommand, hostname: os.Hostname}

// system is where the clipboard tools are looked up and run; tests replace
// them all.
type system struct {
	getenv   func(key string) string
	lookPath func(file string) (string, error)
	run      func(ctx context.Context, c command) ([]byte, error)
	hostname func() (string, error) // this machine's name, which a file URI may carry
}

// read lists the clipboard's offered types, then fetches the local files of
// a file list, else the best image type, else the text. A file list naming
// no local file (web addresses only) counts as none.
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
	if files, err := s.readFiles(ctx, t, string(types)); err != nil || len(files) > 0 {
		return Content{Files: files}, err
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

// readFiles fetches the file list the clipboard offers and returns the
// local files it names; none when it offers no list.
func (s system) readFiles(ctx context.Context, t tool, types string) ([]string, error) {
	listType := pickFileList(types)
	if listType == "" {
		return nil, nil
	}
	data, err := s.run(ctx, t.readCommand(t.readType(listType)))
	if t.isEmpty(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	host := ""
	if s.hostname != nil {
		host, _ = s.hostname() // without it a file URI must name no host, or localhost
	}
	return fileList(string(data), host), nil
}

// write copies text to the clipboard.
func (s system) write(ctx context.Context, text string) error {
	t, ok := s.pick(func(t tool) string {
		return t.writer
	})
	if !ok {
		return s.writeWindows(ctx, text)
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
