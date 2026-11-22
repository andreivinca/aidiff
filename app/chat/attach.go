package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// clipboardTimeout bounds one clipboard read or write.
const clipboardTimeout = 10 * time.Second

// maxTokenName is how many cells of a file's name its token shows.
const maxTokenName = 24

// pendingImage is an image pasted into the input, shown there as its token.
type pendingImage struct {
	token string // "[Image #1]"
	image Image
}

// pendingFile is a file attached to the input, shown there as its token,
// which names it; it is read when the message is sent.
type pendingFile struct {
	token string // "[File #1: report.pdf]"
	file  File
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

// filesInLocked is the attached files whose token still appears in text; a
// token deleted from the input drops its file. mu must be held.
func (p *Pane) filesInLocked(text string) []pendingFile {
	var out []pendingFile
	for _, f := range p.attached {
		if strings.Contains(text, f.token) {
			out = append(out, f)
		}
	}
	return out
}

// attachmentsLocked is what the draft carries now, for a backend's check of
// one more; mu must be held.
func (p *Pane) attachmentsLocked() []Attachment {
	text := p.input.text()
	var out []Attachment
	for _, img := range p.imagesInLocked(text) {
		out = append(out, Attachment{Kind: AttachedImage, Name: img.image.Name, MIME: img.image.MIME, Size: int64(len(img.image.Data))})
	}
	for _, f := range p.filesInLocked(text) {
		out = append(out, Attachment{Kind: AttachedFile, Name: f.file.Name, MIME: f.file.MIME, Size: f.file.Size})
	}
	return out
}

// pasteClipboardLocked reads the clipboard in the background: copied files
// are attached, an image too, text is inserted. mu must be held.
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
	case len(c.Files) > 0:
		p.attachPathsLocked(c.Files, "")
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
		if err := p.checkAttachmentLocked(Attachment{Kind: AttachedImage, Name: img.Name, MIME: img.MIME, Size: int64(len(img.Data))}); err != nil {
			p.note = err.Error()
			return
		}
		label := p.attachImageLocked(img)
		p.note = fmt.Sprintf("%s attached: %s, %s", label, img.Name, sizeText(int64(len(img.Data))))
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

// checkAttachmentLocked asks a backend that takes files whether the draft
// can carry a with what it carries already; mu must be held.
func (p *Pane) checkAttachmentLocked(a Attachment) error {
	ft, ok := p.backend.(FileTaker)
	if !ok {
		return nil
	}
	return ft.CheckAttachments(append(p.attachmentsLocked(), a)) //nolint:wrapcheck // the backend's words say why
}

// errImagesOnly turns down a file the backend cannot take.
func errImagesOnly(name string) error {
	return fmt.Errorf("%s: this agent takes images only", name)
}

// found is a file looked at for attaching, off the lock: an image read
// whole, a file to read when the message is sent, or why it cannot be
// attached.
type found struct {
	path  string
	info  FileInfo
	image *Image // read: it goes as an image
	big   bool   // an image too large to go as one, so it goes as a file
	err   error
}

// attachPathsLocked attaches the files at paths, in order, looking at them
// in the background: each is checked first, an image the backend takes as
// one is read whole, and any other file is only opened again when the
// message is sent. pasted is the text the paths were pasted as, which goes
// into the input instead when none of them can be attached; "" for /attach
// and the clipboard's files. mu must be held.
func (p *Pane) attachPathsLocked(paths []string, pasted string) {
	p.reading++
	b, files := p.backend, p.files
	_, takesFiles := b.(FileTaker)
	takesFiles = takesFiles || b == nil
	p.goLocked(func() func() {
		out := make([]found, 0, len(paths))
		for _, path := range paths {
			out = append(out, lookAt(files, imageCheck{b}, takesFiles, path))
		}
		return func() {
			p.reading--
			p.attachFoundLocked(out, pasted)
		}
	})
}

// lookAt checks the file at path for attaching, off the lock. An image goes
// as one when the backend takes it so, else as a file when the backend
// takes files.
func lookAt(files Files, images imageCheck, takesFiles bool, path string) found {
	info, err := files.Stat(path)
	if err != nil {
		return found{path: path, err: err}
	}
	if info.Image {
		if err = images.check(info.Name, info.Size); err == nil {
			img, readErr := files.ReadImage(path, images.check)
			if readErr != nil {
				return found{path: path, err: readErr}
			}
			return found{path: path, info: info, image: &img}
		}
		if !takesFiles {
			return found{path: path, err: err}
		}
		return found{path: path, info: info, big: true}
	}
	if !takesFiles {
		return found{path: path, err: errImagesOnly(info.Name)}
	}
	return found{path: path, info: info}
}

// attachFoundLocked attaches what lookAt found, in order, and says so in one
// note, with why any was left out. When nothing pasted could be attached,
// the pasted text goes into the input instead: plainly when it named no
// file at all, else with the reason. mu must be held.
func (p *Pane) attachFoundLocked(all []found, pasted string) {
	var attached, refused []string
	missing := 0
	for _, f := range all {
		label, desc, err := p.attachOneLocked(f)
		switch {
		case err == nil:
			attached = append(attached, label+" "+desc)
			if len(all) == 1 {
				attached[0] = label + " attached: " + desc
			}
		case errors.Is(err, fs.ErrNotExist):
			missing++
			refused = append(refused, err.Error())
		default:
			refused = append(refused, err.Error())
		}
	}
	if pasted != "" && len(attached) == 0 {
		p.insertLocked(strings.ReplaceAll(pasted, "\r\n", "\n"))
		if missing < len(all) {
			p.note = strings.Join(refused, "; ") + "; pasted as text"
		}
		return
	}
	switch {
	case len(attached) == 1 && len(refused) == 0:
		p.note = attached[0]
	case len(attached) > 0:
		p.note = fmt.Sprintf("%d attached: %s", len(attached), strings.Join(attached, "; "))
		if len(refused) > 0 {
			p.note += "; left out: " + strings.Join(refused, "; ")
		}
	default:
		p.note = strings.Join(refused, "; ")
	}
}

// attachOneLocked attaches one found file, after the backend's check of the
// draft with it, and returns how the note names it ("[File #1]") and
// describes it ("report.pdf, 2.3 MB"); mu must be held.
func (p *Pane) attachOneLocked(f found) (label, desc string, err error) {
	if f.err != nil {
		return "", "", f.err
	}
	if f.image != nil {
		img := *f.image
		if err := p.checkAttachmentLocked(Attachment{Kind: AttachedImage, Name: img.Name, MIME: img.MIME, Size: int64(len(img.Data))}); err != nil {
			return "", "", err
		}
		return p.attachImageLocked(img), img.Name + ", " + sizeText(int64(len(img.Data))), nil
	}
	files, path := p.files, f.path
	file := File{Name: f.info.Name, MIME: f.info.MIME, Size: f.info.Size, Open: func() (io.ReadCloser, int64, error) {
		return files.Open(path) //nolint:wrapcheck // the reader's words already name the file
	}}
	if err := p.checkAttachmentLocked(Attachment{Kind: AttachedFile, Name: file.Name, MIME: file.MIME, Size: file.Size}); err != nil {
		return "", "", err
	}
	desc = file.Name + ", " + sizeText(file.Size)
	if f.big {
		desc += ", as a file: too large to go as an image"
	}
	return p.attachFileLocked(file), desc, nil
}

// attachImageLocked keeps the image for the next message and puts its token
// into the input at the cursor; it returns how the note names it. mu must
// be held.
func (p *Pane) attachImageLocked(img Image) string {
	p.imageSeq++
	token := fmt.Sprintf("[Image #%d]", p.imageSeq)
	p.images = append(p.images, pendingImage{token: token, image: img})
	p.insertTokenLocked(token)
	return token
}

// attachFileLocked keeps the file for the next message and puts its token,
// which names it, into the input at the cursor; it returns how the note
// names it. mu must be held.
func (p *Pane) attachFileLocked(f File) string {
	p.fileSeq++
	label := fmt.Sprintf("File #%d", p.fileSeq)
	token := "[" + label + ": " + tokenName(f.Name) + "]"
	p.attached = append(p.attached, pendingFile{token: token, file: f})
	p.insertTokenLocked(token)
	return "[" + label + "]"
}

// insertTokenLocked puts an attachment's token into the input at the
// cursor, apart from the text around it; mu must be held.
func (p *Pane) insertTokenLocked(token string) {
	if before := p.input.before(); before != "" && before != " " && before != "\n" {
		p.input.insert(" ")
	}
	p.input.insertAtom(token)
	p.insertLocked(" ")
}

// tokenName is a file's name as its token shows it: on one line, without
// the brackets that end a token, and no wider than maxTokenName, keeping
// the extension.
func tokenName(name string) string {
	name = strings.NewReplacer("[", "(", "]", ")", "\n", " ", "\t", " ").Replace(sanitize(name))
	if ansi.StringWidth(name) <= maxTokenName {
		return name
	}
	ext := filepath.Ext(name)
	if w := ansi.StringWidth(ext); w > 8 || w >= maxTokenName-2 {
		ext = ""
	}
	return ansi.Truncate(strings.TrimSuffix(name, ext), maxTokenName-ansi.StringWidth(ext)-1, "") + "…" + ext
}

// sizeText is a size in bytes as a note or the transcript shows it.
func sizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
