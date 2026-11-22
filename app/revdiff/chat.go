package main

import (
	"context"
	"io"

	"github.com/umputun/revdiff/app/attachfile"
	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/clipboard"
)

// chatConfig is how chat panes look, what they reach on this machine (its
// clipboard and the files the user attaches), the split's keys they list,
// and where their shortcuts show.
func chatConfig(opts options) chat.Config {
	return chat.Config{
		NoColor:   opts.NoColors,
		Clipboard: chatClipboard{},
		Files:     chatFiles{},
		HostKeys:  []chat.Hint{{Key: paletteKey, Label: "commands"}},
		// the status bar under the whole window carries the focused chat's
		// shortcuts; without one the chat keeps its own hint row
		HintsOutside: !opts.NoStatusBar,
	}
}

// chatClipboard is this machine's clipboard as a chat pane reads and writes
// it.
type chatClipboard struct{}

// Read returns the files the clipboard holds copied, else its image, else
// its text.
func (chatClipboard) Read(ctx context.Context) (chat.Clip, error) {
	c, err := clipboard.System{}.Read(ctx)
	if err != nil {
		return chat.Clip{}, err //nolint:wrapcheck // an adapter: the pane shows the clipboard's own words
	}
	return chat.Clip{Files: c.Files, Image: c.Image, MIME: c.Mime, Text: c.Text}, nil
}

// Write copies text to the clipboard.
func (chatClipboard) Write(ctx context.Context, text string) error {
	return clipboard.System{}.Write(ctx, text) //nolint:wrapcheck // an adapter: the pane shows the clipboard's own words
}

// chatFiles are this machine's files as a chat pane attaches them.
type chatFiles struct{}

// Stat checks the file at path for attaching and describes it.
func (chatFiles) Stat(path string) (chat.FileInfo, error) {
	info, err := attachfile.Reader{}.Stat(path)
	if err != nil {
		return chat.FileInfo{}, err //nolint:wrapcheck // an adapter: the reader's words already name the file
	}
	return chat.FileInfo{Name: info.Name, Size: info.Size, MIME: info.MIME, Image: info.Image}, nil
}

// ReadImage loads the image file at path, once check took its name and
// size.
func (chatFiles) ReadImage(path string, check func(name string, size int64) error) (chat.Image, error) {
	img, err := attachfile.Reader{}.ReadImage(path, check)
	if err != nil {
		return chat.Image{}, err //nolint:wrapcheck // an adapter: the reader's words already name the file
	}
	return chat.Image{Name: img.Name, MIME: img.MIME, Data: img.Data}, nil
}

// Open opens the file at path to send it.
func (chatFiles) Open(path string) (io.ReadCloser, int64, error) {
	return attachfile.Reader{}.Open(path) //nolint:wrapcheck // an adapter: the reader's words already name the file
}
