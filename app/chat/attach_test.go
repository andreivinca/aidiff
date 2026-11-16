package chat

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPane_PastesImages(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	png := []byte("\x89PNG\r\n\x1a\nfake")
	f.clip.set(Clip{Image: png, MIME: "image/png"}, nil)

	p.SendText("look at this")
	p.SendText("\x16") // ctrl+v
	waitUpdate(t, p)
	v := view(p)
	assert.Contains(t, v, "│   look at this [Image #1] █", "the image shows as a token in the input")
	assert.Contains(t, v, "[Image #1] attached: clipboard.png, 12 B")

	f.clip.set(Clip{Text: "and this text"}, nil)
	p.SendText("\x16")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "[Image #1] and this text█", "text on the clipboard is pasted as text")

	f.files["/pics/shot.jpg"] = Image{Name: "shot.jpg", MIME: "image/jpeg", Data: []byte("jpegdata")}
	p.Paste("/pics/shot.jpg")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "[Image #2] █", "a pasted path to an image attaches it")
	p.Paste("/pics/missing.png")
	waitUpdate(t, p)
	assert.Contains(t, p.input.text(), "missing.png", "a path that is no file is pasted as text")
	p.SendText("\x17") // drop that text again

	c := ask(t, p, f, func() {
		p.SendText("\r")
	})
	require.Len(t, c.msg.Images, 2, "the message carries the images its text names")
	assert.Equal(t, Image{Name: "clipboard.png", MIME: "image/png", Data: png}, c.msg.Images[0])
	assert.Equal(t, "shot.jpg", c.msg.Images[1].Name)
	assert.Equal(t, "look at this [Image #1] and this text [Image #2]", c.msg.Text)
	assert.Equal(t, "look at this  and this text", c.msg.Prose(), "the prose leaves the tokens out")

	p.SendText("/attach /pics/shot.jpg\r")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "[Image #3] █")
	p.SendText("\x15") // ctrl+u clears the input, token included
	c = ask(t, p, f, func() {
		p.SendText("no image after all\r")
	})
	assert.Empty(t, c.msg.Images, "a token deleted from the input drops its image")

	p.SendText("/attach /pics/notes.txt\r")
	assert.Contains(t, view(p), "not an image file")
	p.SendText("\x15/attach\r")
	assert.Contains(t, view(p), "/attach <image file>")
	f.clip.set(Clip{}, errors.New("no clipboard tool"))
	p.SendText("\x15\x16")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "clipboard: no clipboard tool")
	f.clip.set(Clip{}, nil)
	p.SendText("\x16")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "the clipboard is empty")
}

func TestPane_BackendTurnsImagesDown(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	f.mu.Lock()
	f.maxImage = 4
	f.mu.Unlock()
	f.clip.set(Clip{Image: []byte("too large"), MIME: "image/png"}, nil)
	p.SendText("\x16")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "clipboard.png is too large")
	assert.Empty(t, p.input.text())

	f.files["/pics/big.png"] = Image{Name: "big.png", MIME: "image/png", Data: []byte("too large")}
	p.SendText("/attach /pics/big.png\r")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "attach: big.png is too large", "a file is checked before it is read")
	locked(p, func() {
		assert.Empty(t, p.images)
	})
}

func TestPane_FollowKeepsTheDraftWithItsImages(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	f.clip.set(Clip{Image: []byte("png"), MIME: "image/png"}, nil)
	p.SendText("see\x16")
	waitUpdate(t, p)
	other := newFakeBackend(fixture())
	follow(t, p, other, "Other thread")
	assert.Equal(t, "see [Image #1] ", p.input.text())
	c := ask(t, p, other, func() {
		p.SendText("\r")
	})
	assert.Len(t, c.msg.Images, 1, "the image goes with the draft that names it, to the new backend")
}

func TestPane_StalePasteIsDropped(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	f.clip.set(Clip{Image: []byte("png"), MIME: "image/png"}, nil)
	f.clip.mu.Lock() // hold the read until the pane moved on
	p.SendText("draft")
	p.SendText("\x16")
	other := newFakeBackend(fixture())
	other.clip = f.clip
	follow(t, p, other, "Other thread")
	f.clip.mu.Unlock()
	p.SendText("\x16")
	waitUpdate(t, p)
	assert.Equal(t, "draft [Image #1] ", p.input.text(), "only the paste made on this backend lands in the draft")
}
