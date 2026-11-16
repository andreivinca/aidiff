package clipboard

import (
	"errors"
	"strings"
)

// tool is one display server's clipboard programs and how to call them.
type tool struct {
	display   string // the environment variable set under this display server
	reader    string // the program that reads the clipboard
	writer    string // the program that writes it
	listTypes []string
	readType  func(mime string) []string
	readText  []string
	writeText []string
	// emptyHints are what the reader prints on stderr, failing, when the
	// clipboard holds nothing (or nothing of the asked type).
	emptyHints []string
}

// tools are tried in order: Wayland first, since XWayland sets DISPLAY too.
var tools = []tool{
	{
		display:   "WAYLAND_DISPLAY",
		reader:    "wl-paste",
		writer:    "wl-copy",
		listTypes: []string{"--list-types"},
		readType: func(mime string) []string {
			return []string{"--type", mime}
		},
		readText:   []string{"--no-newline"},
		emptyHints: []string{"Nothing is copied", "No selection", "No suitable type of content copied"},
	},
	{
		display:   "DISPLAY",
		reader:    "xclip",
		writer:    "xclip",
		listTypes: []string{"-selection", "clipboard", "-t", "TARGETS", "-o"},
		readType: func(mime string) []string {
			return []string{"-selection", "clipboard", "-t", mime, "-o"}
		},
		readText:   []string{"-selection", "clipboard", "-o"},
		writeText:  []string{"-selection", "clipboard"},
		emptyHints: []string{"not available"}, // "Error: target TARGETS not available"
	},
}

// readCommand is the reader run with args, its output capped.
func (t tool) readCommand(args []string) command {
	return command{name: t.reader, args: args, limit: maxReadBytes}
}

// isEmpty reports whether err is the reader saying the clipboard is empty.
func (t tool) isEmpty(err error) bool {
	failed, ok := errors.AsType[*exitError](err)
	if !ok {
		return false
	}
	for _, hint := range t.emptyHints {
		if strings.Contains(failed.stderr, hint) {
			return true
		}
	}
	return false
}
