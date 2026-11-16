package main

import (
	"context"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/clipboard"
	"github.com/umputun/revdiff/app/imagefile"
)

// chatConfig is how chat panes look, what they reach on this machine (its
// clipboard and its image files) and the split's palette key they list.
func chatConfig(opts options) chat.Config {
	return chat.Config{
		NoColor:   opts.NoColors,
		Clipboard: chatClipboard{},
		Files:     chatImages{},
		HostKeys:  []chat.Hint{{Key: paletteKey, Label: "commands"}},
	}
}

// chatClipboard is this machine's clipboard as a chat pane reads and writes
// it.
type chatClipboard struct{}

// Read returns the clipboard's image when it holds one, else its text.
func (chatClipboard) Read(ctx context.Context) (chat.Clip, error) {
	c, err := clipboard.System{}.Read(ctx)
	if err != nil {
		return chat.Clip{}, err //nolint:wrapcheck // an adapter: the pane shows the clipboard's own words
	}
	return chat.Clip{Image: c.Image, MIME: c.Mime, Text: c.Text}, nil
}

// Write copies text to the clipboard.
func (chatClipboard) Write(ctx context.Context, text string) error {
	return clipboard.System{}.Write(ctx, text) //nolint:wrapcheck // an adapter: the pane shows the clipboard's own words
}

// chatImages are this machine's image files as a chat pane attaches them.
type chatImages struct{}

// IsImage reports whether path names an image file, by its extension.
func (chatImages) IsImage(path string) bool {
	return imagefile.Reader{}.IsImage(path)
}

// Read loads the image file at path, once check took its name and size.
func (chatImages) Read(path string, check func(name string, size int64) error) (chat.Image, error) {
	img, err := imagefile.Reader{}.Read(path, check)
	if err != nil {
		return chat.Image{}, err //nolint:wrapcheck // an adapter: the reader's words already name the file
	}
	return chat.Image{Name: img.Name, MIME: img.MIME, Data: img.Data}, nil
}
