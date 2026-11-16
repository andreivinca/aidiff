package chat

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// clipboardTimeout bounds one clipboard read or write.
const clipboardTimeout = 10 * time.Second

// pendingImage is an image pasted into the input, shown there as its token.
type pendingImage struct {
	token string // "[Image #1]"
	image Image
}

// clipboardExtensions names an image pasted from the clipboard after its
// media type.
var clipboardExtensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
	"image/bmp":  ".bmp",
}

// imagesInLocked is the pasted images whose token still appears in text; a
// token deleted from the input drops its image. mu must be held.
func (p *Pane) imagesInLocked(text string) []pendingImage {
	var out []pendingImage
	for _, img := range p.images {
		if strings.Contains(text, img.token) {
			out = append(out, img)
		}
	}
	return out
}

// pasteClipboardLocked reads the clipboard in the background: an image is
// attached, text is inserted. mu must be held.
func (p *Pane) pasteClipboardLocked() {
	p.reading++
	p.goLocked(func() func() {
		ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
		defer cancel()
		c, err := p.clip.Read(ctx)
		return func() {
			p.reading--
			p.pastedLocked(c, err)
		}
	})
}

// pastedLocked takes what the clipboard held; mu must be held.
func (p *Pane) pastedLocked(c Clip, err error) {
	switch {
	case err != nil:
		p.note = "clipboard: " + err.Error()
	case len(c.Image) > 0:
		ext, ok := clipboardExtensions[c.MIME]
		if !ok {
			ext = ".img"
		}
		img := Image{Name: "clipboard" + ext, MIME: c.MIME, Data: c.Image}
		if err := (imageCheck{p.backend}).check(img.Name, int64(len(img.Data))); err != nil {
			p.note = err.Error()
			return
		}
		p.attachImageLocked(img)
	case c.Text != "":
		p.insertLocked(strings.ReplaceAll(c.Text, "\r\n", "\n"))
	default:
		p.note = "the clipboard is empty"
	}
}

// imageCheck is the backend's check of the images it takes. A pane with
// no backend takes any, as it sends no message either.
type imageCheck struct {
	backend Backend
}

func (c imageCheck) check(name string, size int64) error {
	if c.backend == nil {
		return nil
	}
	return c.backend.CheckImage(name, size) //nolint:wrapcheck // the backend's words say why
}

// attachFileLocked reads the image file at path in the background and
// attaches it; the backend turns down a file it cannot take before it is
// read. pasted is the text the path was pasted as, which goes into the
// input instead when the path names no file; "" for /attach. mu must be
// held.
func (p *Pane) attachFileLocked(path, pasted string) {
	p.reading++
	check := imageCheck{p.backend}.check
	p.goLocked(func() func() {
		img, err := p.files.Read(path, check)
		return func() {
			p.reading--
			p.fileReadLocked(img, err, pasted)
		}
	})
}

// fileReadLocked takes a read image file; mu must be held.
func (p *Pane) fileReadLocked(img Image, err error, pasted string) {
	switch {
	case err == nil:
		p.attachImageLocked(img)
	case pasted != "" && errors.Is(err, fs.ErrNotExist):
		p.insertLocked(strings.ReplaceAll(pasted, "\r\n", "\n"))
	default:
		p.note = err.Error()
	}
}

// attachImageLocked keeps the image for the next message and puts its token
// into the input at the cursor; mu must be held.
func (p *Pane) attachImageLocked(img Image) {
	p.imageSeq++
	token := fmt.Sprintf("[Image #%d]", p.imageSeq)
	p.images = append(p.images, pendingImage{token: token, image: img})
	if before := p.input.before(); before != "" && before != " " && before != "\n" {
		p.input.insert(" ")
	}
	p.insertLocked(token + " ")
	p.note = fmt.Sprintf("%s attached: %s, %s", token, img.Name, sizeText(len(img.Data)))
}

func sizeText(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
