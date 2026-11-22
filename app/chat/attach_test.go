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

	f.files["/pics/shot.jpg"] = imageFile(Image{Name: "shot.jpg", MIME: "image/jpeg", Data: []byte("jpegdata")})
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
	waitUpdate(t, p)
	assert.Contains(t, view(p), "attach: stat /pics/notes.txt: file does not exist", "/attach takes any file, and says when there is none")
	p.SendText("\x15/attach\r")
	assert.Contains(t, view(p), "/attach <file>")
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
	f := newFakeBackend(fixture())
	p := startPaneOn(t, imagesOnly{f}, f, 80, 14)
	f.mu.Lock()
	f.maxImage = 4
	f.mu.Unlock()
	f.clip.set(Clip{Image: []byte("too large"), MIME: "image/png"}, nil)
	p.SendText("\x16")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "clipboard.png is too large")
	assert.Empty(t, p.input.text())

	f.files["/pics/big.png"] = imageFile(Image{Name: "big.png", MIME: "image/png", Data: []byte("too large")})
	p.SendText("/attach /pics/big.png\r")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "big.png is too large", "a file is checked before it is read")
	f.files["/docs/a.pdf"] = fakeFile{mime: "application/pdf", content: "%PDF"}
	p.SendText("\x15/attach /docs/a.pdf\r")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "a.pdf: this agent takes images only")
	locked(p, func() {
		assert.Empty(t, p.images)
		assert.Empty(t, p.attached)
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

func TestEditor_AtomsAreOneCharacter(t *testing.T) {
	var e editor
	e.addAtom("[Image #1]")
	e.insert("see [Image #1] now")
	assert.Len(t, e.clusters, len("see ")+1+len(" now"), "the token is one character")

	e.moveTo(len("see ")+1, false) // right after the token
	e.left()
	assert.Equal(t, len("see "), e.cur, "one move crosses the whole token")
	e.right()
	e.backspace()
	assert.Equal(t, "see  now", e.text(), "one backspace removes the whole token")

	e.clear()
	e.addAtom("[Image #2]")
	e.insert("a [Image #2] b")
	e.moveTo(2, false)
	e.del()
	assert.Equal(t, "a  b", e.text(), "delete removes it whole too")

	e.clear()
	e.addAtom("[Image #3]")
	e.insert("x [Image #3]")
	e.killWord()
	assert.Equal(t, "x ", e.text(), "a word delete takes the token, never half of it")

	e.clear()
	e.insert("[Image #4]")
	assert.Len(t, e.clusters, len("[Image #4]"), "text that is no atom is plain characters")
	e.addAtom("[Image #4]")
	assert.Len(t, e.clusters, 1, "an atom added later joins the text already there")
	assert.Equal(t, 1, e.cur, "and the cursor stays after it")

	e.clear()
	e.insert("a ")
	e.insertAtom("/resume")
	e.backspace()
	assert.Equal(t, "a ", e.text())
	e.insert("/resume")
	assert.Len(t, e.clusters, len("a /resume"), "a deleted atom is forgotten: typed again it is plain text")

	e.clear()
	e.insert("keep this")
	e.moveTo(0, false)
	e.moveTo(4, true) // "keep" selected
	e.insertAtom("[Image #5]")
	assert.Equal(t, "[Image #5] this", e.text(), "an atom replaces the selection")
	assert.Len(t, e.clusters, 1+len(" this"), "and stays one character")
}

func TestPane_ImageTokenDeletesAsABlock(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	f.clip.set(Clip{Image: []byte("\x89PNG\r\n\x1a\nfake"), MIME: "image/png"}, nil)
	p.SendText("look ")
	p.SendText("\x16") // ctrl+v
	waitUpdate(t, p)
	assert.Equal(t, "look [Image #1] ", p.input.text())

	p.SendText("\x7f") // the space after the token
	p.SendText("\x7f")
	assert.Equal(t, "look ", p.input.text(), "backspace takes the token in one press")
	locked(p, func() {
		assert.Empty(t, p.imagesInLocked(p.input.text()), "and the image goes with it")
	})
}
