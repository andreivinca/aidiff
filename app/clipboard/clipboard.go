// Package clipboard reads the system clipboard, which a terminal cannot
// paste when it holds an image. Linux only: wl-paste on Wayland, xclip on
// X11.
package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Content is what the clipboard held: an image, or text.
type Content struct {
	Image []byte // set when the clipboard holds an image
	Mime  string // the image's media type
	Text  string // set when it holds text instead
}

// ErrNoTool means no clipboard reader is available on this machine.
var ErrNoTool = errors.New("no clipboard tool: install wl-clipboard (Wayland) or xclip (X11)")

// Read returns the clipboard's image when it holds one, else its text.
func Read(ctx context.Context) (Content, error) {
	switch {
	case os.Getenv("WAYLAND_DISPLAY") != "" && has("wl-paste"):
		return read(ctx, "wl-paste",
			[]string{"--list-types"},
			func(mime string) []string { return []string{"--type", mime} },
			[]string{"--no-newline"})
	case os.Getenv("DISPLAY") != "" && has("xclip"):
		return read(ctx, "xclip",
			[]string{"-selection", "clipboard", "-t", "TARGETS", "-o"},
			func(mime string) []string { return []string{"-selection", "clipboard", "-t", mime, "-o"} },
			[]string{"-selection", "clipboard", "-o"})
	}
	return Content{}, ErrNoTool
}

func has(tool string) bool {
	_, err := exec.LookPath(tool)
	return err == nil
}

// read lists the clipboard's offered types with tool, then fetches the best
// image type, or the text when there is no image.
func read(ctx context.Context, tool string, list []string, image func(mime string) []string, text []string) (Content, error) {
	types, err := output(ctx, tool, list...)
	if err != nil {
		return Content{}, err
	}
	if mime := pickImage(string(types)); mime != "" {
		data, ierr := output(ctx, tool, image(mime)...)
		if ierr != nil {
			return Content{}, ierr
		}
		return Content{Image: data, Mime: mime}, nil
	}
	data, err := output(ctx, tool, text...)
	if err != nil {
		return Content{}, err
	}
	return Content{Text: string(data)}, nil
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

func output(ctx context.Context, tool string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, tool, args...).Output() //nolint:gosec // a known tool, fixed arguments
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return nil, fmt.Errorf("%s: %s", tool, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("%s: %w", tool, err)
	}
	return out, nil
}

// Write copies text to the local desktop clipboard.
func Write(ctx context.Context, text string) error {
	var cmd *exec.Cmd
	switch {
	case os.Getenv("WAYLAND_DISPLAY") != "" && has("wl-copy"):
		cmd = exec.CommandContext(ctx, "wl-copy")
	case os.Getenv("DISPLAY") != "" && has("xclip"):
		cmd = exec.CommandContext(ctx, "xclip", "-selection", "clipboard")
	default:
		return ErrNoTool
	}
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copy text: %w", err)
	}
	return nil
}
