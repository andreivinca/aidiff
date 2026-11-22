package chat

import (
	"errors"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPane_PastesCopiedFiles(t *testing.T) {
	p, f := newTestPane(t, 100, 14, fixture())
	f.files["/docs/report.pdf"] = fakeFile{mime: "application/pdf", content: "%PDF-1.7"}
	f.files["/pics/cat.png"] = imageFile(Image{Name: "cat.png", MIME: "image/png", Data: []byte("png")})
	f.files["/docs/notes.txt"] = fakeFile{mime: "text/plain", content: "first draft"}
	f.clip.set(Clip{Files: []string{"/docs/report.pdf", "/pics/cat.png", "/docs/notes.txt"}}, nil)

	p.SendText("see ")
	p.SendText("\x16") // ctrl+v
	settle(t, p)
	assert.Equal(t, "see [File #1: report.pdf] [Image #1] [File #2: notes.txt] ", draft(p),
		"copied files attach in order, an image file as an image, each token naming its file")
	assert.Contains(t, view(p), "3 attached: [File #1] report.pdf, 8 B; [Image #1] cat.png, 3 B; [File #2] notes.txt, 11 B",
		"one note sums them up")

	f.files["/docs/notes.txt"] = fakeFile{mime: "text/plain", content: "edited since"}
	c := ask(t, p, f, func() {
		p.SendText("\r")
	})
	require.Len(t, c.msg.Files, 2)
	require.Len(t, c.msg.Images, 1)
	assert.Equal(t, "report.pdf", c.msg.Files[0].Name)
	assert.Equal(t, "application/pdf", c.msg.Files[0].MIME)
	assert.Equal(t, int64(8), c.msg.Files[0].Size)
	assert.Equal(t, "%PDF-1.7", fileContent(t, c.msg.Files[0]))
	assert.Equal(t, "edited since", fileContent(t, c.msg.Files[1]), "a file is read when the message is sent")
	assert.Equal(t, "see", c.msg.Prose(), "the prose leaves every token out")
	assert.Equal(t, "see [File #1: report.pdf] [Image #1] [File #2: notes.txt]", c.msg.Text)
}

func TestPane_OneFileNoteAndTokenDeletes(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	f.files["/docs/report.pdf"] = fakeFile{mime: "application/pdf", content: strings.Repeat("x", 2400<<10)}
	p.SendText("/attach /docs/report.pdf\r")
	settle(t, p)
	assert.Contains(t, view(p), "[File #1] attached: report.pdf, 2.3 MB")
	assert.Contains(t, view(p), "[File #1: report.pdf] █", "the token names its file in the input")

	p.SendText("\x7f\x7f") // the space after the token, then the token
	assert.Empty(t, draft(p), "backspace takes the token in one press")
	c := ask(t, p, f, func() {
		p.SendText("no file after all\r")
	})
	assert.Empty(t, c.msg.Files, "a token deleted from the input drops its file")
}

func TestPane_NumbersImagesAndFilesApart(t *testing.T) {
	p, f := newTestPane(t, 120, 14, fixture())
	f.files["/a/one.txt"] = fakeFile{content: "1"}
	f.files["/a/two.txt"] = fakeFile{content: "2"}
	f.files["/a/shot.jpg"] = imageFile(Image{Name: "shot.jpg", MIME: "image/jpeg", Data: []byte("jpg")})
	f.clip.set(Clip{Image: []byte("png"), MIME: "image/png"}, nil)
	p.SendText("\x16")
	settle(t, p)
	for _, path := range []string{"/a/one.txt", "/a/shot.jpg", "/a/two.txt"} {
		p.Paste(path)
		settle(t, p)
	}
	assert.Equal(t, "[Image #1] [File #1: one.txt] [Image #2] [File #2: two.txt] ", draft(p))
	c := ask(t, p, f, func() {
		p.SendText("\r")
	})
	assert.Len(t, c.msg.Images, 2)
	assert.Len(t, c.msg.Files, 2)
}

func TestPane_RefusesFilesWithANote(t *testing.T) {
	p, f := newTestPane(t, 100, 14, fixture())
	f.mu.Lock()
	f.maxFile, f.maxAtts = 100, 2
	f.mu.Unlock()
	f.files["/a/folder"] = fakeFile{err: errors.New("/a/folder is a folder; attach the files in it instead")}
	f.files["/a/pipe"] = fakeFile{err: errors.New("/a/pipe is a named pipe, not a regular file")}
	f.files["/a/locked.txt"] = fakeFile{err: errors.New("attach: open /a/locked.txt: permission denied")}
	f.files["/a/empty.txt"] = fakeFile{}
	f.files["/a/huge.bin"] = fakeFile{content: strings.Repeat("x", 101)}
	f.files["/a/ok.txt"] = fakeFile{content: "ok"}

	for path, note := range map[string]string{
		"/a/folder":     "/a/folder is a folder; attach the files in it instead",
		"/a/pipe":       "/a/pipe is a named pipe, not a regular file",
		"/a/locked.txt": "attach: open /a/locked.txt: permission denied",
		"/a/missing":    "attach: stat /a/missing: file does not exist",
		"/a/empty.txt":  "empty.txt is empty",
		"/a/huge.bin":   "huge.bin is too large to send",
	} {
		f.clip.set(Clip{Files: []string{path}}, nil)
		p.SendText("\x16")
		settle(t, p)
		assert.Contains(t, view(p), note, path)
		assert.Empty(t, draft(p), "nothing attached for "+path)
	}

	f.clip.set(Clip{Files: []string{"/a/ok.txt", "/a/folder", "/a/ok.txt", "/a/ok.txt"}}, nil)
	p.SendText("\x16")
	settle(t, p)
	assert.Equal(t, "[File #1: ok.txt] [File #2: ok.txt] ", draft(p), "as many as the message can carry, in order")
	assert.Equal(t, "2 attached: [File #1] ok.txt, 2 B; [File #2] ok.txt, 2 B; left out: /a/folder is a folder; attach the files in it instead; up to 2 attachments a message", p.note)
	f.mu.Lock()
	assert.Len(t, f.checked[len(f.checked)-1], 3, "the backend weighs the draft's attachments with the new one")
	f.mu.Unlock()
}

func TestPane_BigImageGoesAsAFile(t *testing.T) {
	p, f := newTestPane(t, 100, 14, fixture())
	f.mu.Lock()
	f.maxImage = 4
	f.mu.Unlock()
	f.files["/pics/huge.png"] = imageFile(Image{Name: "huge.png", MIME: "image/png", Data: []byte("too large for an image")})
	p.Paste("/pics/huge.png")
	settle(t, p)
	assert.Equal(t, "[File #1: huge.png] ", draft(p))
	assert.Contains(t, view(p), "[File #1] attached: huge.png, 22 B, as a file: too large to go as an image")
	locked(p, func() {
		assert.Empty(t, p.images)
		require.Len(t, p.attached, 1)
		assert.Equal(t, "image/png", p.attached[0].file.MIME, "it keeps its type")
	})
}

func TestPane_PastedPathForms(t *testing.T) {
	tests := []struct {
		paste string
		path  string
	}{
		{"/home/me/My Report.pdf", "/home/me/My Report.pdf"},
		{"'/home/me/My Report.pdf'", "/home/me/My Report.pdf"},
		{`"/home/me/My Report.pdf"` + "\n", "/home/me/My Report.pdf"},
		{`/home/me/My\ Report.pdf`, "/home/me/My Report.pdf"},
		{"file:///home/me/My%20Report.pdf", "/home/me/My Report.pdf"},
		{"file://localhost/home/me/My%20Report.pdf", "/home/me/My Report.pdf"},
		{"~/My Report.pdf", "~/My Report.pdf"},
	}
	for _, tc := range tests {
		t.Run(tc.paste, func(t *testing.T) {
			p, f := newTestPane(t, 100, 14, fixture())
			f.files[tc.path] = fakeFile{mime: "application/pdf", content: "%PDF"}
			p.Paste(tc.paste)
			settle(t, p)
			assert.Equal(t, "[File #1: My Report.pdf] ", draft(p))
		})
	}

	p, f := newTestPane(t, 100, 14, fixture())
	f.files["/a/one.txt"] = fakeFile{content: "1"}
	f.files["/b/two words.txt"] = fakeFile{content: "2"}
	p.Paste(`/a/one.txt '/b/two words.txt'`)
	settle(t, p)
	assert.Equal(t, "[File #1: one.txt] [File #2: two words.txt] ", draft(p), "several dropped files at once")

	p.SendText("\x15")
	p.Paste("/nowhere/at/all.txt")
	settle(t, p)
	assert.Equal(t, "/nowhere/at/all.txt", draft(p), "a path that names no file is pasted as text")
	assert.Empty(t, p.note)

	p.SendText("\x15")
	f.files["/etc/conf.d"] = fakeFile{err: errors.New("/etc/conf.d is a folder; attach the files in it instead")}
	p.Paste("/etc/conf.d")
	settle(t, p)
	assert.Equal(t, "/etc/conf.d", draft(p), "a pasted folder goes in as text")
	assert.Contains(t, view(p), "/etc/conf.d is a folder; attach the files in it instead; pasted as text")

	for _, text := range []string{"/help", "notes.txt", "./notes.txt", "src/main.go", "// a comment", "look at /a/one.txt"} {
		p.SendText("\x15")
		p.Paste(text)
		assert.Equal(t, text, draft(p), "%q stays text, at once", text)
	}
}

func TestPastedPaths(t *testing.T) {
	tests := []struct {
		text string
		want []string
	}{
		{"/a/b.txt", []string{"/a/b.txt"}},
		{"  /a/b.txt \r\n", []string{"/a/b.txt"}},
		{"/a/b c.txt", []string{"/a/b c.txt"}},
		{`/a/it's.txt`, []string{"/a/it's.txt"}},
		{`'/a/it'\''s.txt'`, []string{"/a/it's.txt"}},
		{`"/a/say \"hi\".txt"`, []string{`/a/say "hi".txt`}},
		{`/a/b\ c.txt /d/e.txt`, []string{"/a/b c.txt", "/d/e.txt"}},
		{"file:///a/%E2%9C%93.txt", []string{"/a/✓.txt"}},
		{"FILE:///a/b.txt", []string{"/a/b.txt"}},
		{"file://otherhost/a/b.txt", nil},
		{"https://example.com/a/b.txt", nil},
		{"~/b.txt", []string{"~/b.txt"}},
		{"~user/b.txt", nil},
		{"/help", nil},
		{"/model claude/opus", []string{"/model claude/opus"}}, // looked at, and pasted as text when it is no file
		{"//comment/here", nil},
		{"b.txt", nil},
		{"/a/b.txt\n/c/d.txt", nil},
		{"", nil},
		{"/" + strings.Repeat("a/", 3000), nil},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, pastedPaths(tc.text), tc.text)
	}
	assert.Equal(t, "/a/b c.txt", attachArg(` "/a/b c.txt" `))
	assert.Equal(t, "notes.txt", attachArg("notes.txt"), "/attach takes a relative path")
	assert.Equal(t, "/a/b c.txt", attachArg("file:///a/b%20c.txt"))
	assert.Equal(t, "/a/b c.txt", attachArg("/a/b c.txt"))
}

func TestTokenName(t *testing.T) {
	assert.Equal(t, "report.pdf", tokenName("report.pdf"))
	assert.Equal(t, "a (draft).txt", tokenName("a [draft].txt"), "no bracket ends the token early")
	assert.Equal(t, "evil name.txt", tokenName("evil\x1b[31m\nname.txt"))
	long := tokenName("quarterly-financial-report-final-v2.pdf")
	assert.Equal(t, "quarterly-financial….pdf", long, "a long name keeps its extension")
	assert.LessOrEqual(t, len([]rune(long)), maxTokenName)
}

func TestPane_SendFailureKeepsTheFiles(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	f.files["/a/one.txt"] = fakeFile{content: "1"}
	p.SendText("/attach /a/one.txt\r")
	settle(t, p)
	p.SendText("read it")
	f.set(errBoom, nil)
	ask(t, p, f, func() {
		p.SendText("\r")
	})
	require.Eventually(t, func() bool {
		return draft(p) == "[File #1: one.txt] read it"
	}, waitLimit, time.Millisecond, "a failed send puts the message back, its file included")
	assert.Contains(t, view(p), "send failed: boom")
	f.set(nil, nil)
	c := ask(t, p, f, func() {
		p.SendText("\r")
	})
	require.Len(t, c.msg.Files, 1, "and the file goes with the retry")
}

func TestPane_UploadShowsProgressAndEscCancels(t *testing.T) {
	f := newFakeBackend(fixture())
	f.uploading = make(chan struct{})
	p := New(f, "Fix login", 80, 16, Config{NoColor: true, Clipboard: f.clip, Files: f.files})
	t.Cleanup(p.Close)
	awaitShown(t, p, f)
	f.files["/a/big.iso"] = fakeFile{content: strings.Repeat("x", 4<<20)}
	p.SendText("/attach /a/big.iso\r")
	settle(t, p)
	p.SendText("install this")
	p.SendText("\r")
	c := nextCall(t, f)
	require.Equal(t, "upload", c.kind)
	require.Eventually(t, func() bool {
		return strings.Contains(view(p), "Uploading big.iso: 2.0 MB of 4.0 MB · esc cancels")
	}, waitLimit, 5*time.Millisecond, "the card shows how far the file went")
	assert.Contains(t, view(p), "esc cancel upload", "the hint row says esc stops it")
	assert.NotContains(t, view(p), "esc stop…", "not the agent's stop")

	p.SendText("\x1b")
	settle(t, p)
	require.Eventually(t, func() bool {
		return draft(p) == "[File #1: big.iso] install this"
	}, waitLimit, 5*time.Millisecond, "the message goes back into the input")
	assert.Contains(t, view(p), "the upload was cancelled; the message is back in the input")
	assert.NotContains(t, view(p), "Uploading")
	locked(p, func() {
		assert.False(t, p.busy)
		assert.Nil(t, p.cancelSend)
	})

	p.SendText("\x1b")
	assert.Contains(t, view(p), "Stop agent?", "with nothing going up, esc offers to stop the agent again")
}

func TestPane_FollowStopsTheUpload(t *testing.T) {
	p, f := newTestPane(t, 80, 14, fixture())
	f.mu.Lock()
	f.uploading = make(chan struct{})
	f.mu.Unlock()
	f.files["/a/big.iso"] = fakeFile{content: "iso"}
	p.SendText("/attach /a/big.iso\r")
	settle(t, p)
	p.SendText("\r")
	require.Equal(t, "upload", nextCall(t, f).kind)
	other := newFakeBackend(fixture())
	follow(t, p, other, "Other thread")
	require.Eventually(t, func() bool {
		return draft(p) == "[File #1: big.iso]"
	}, waitLimit, 5*time.Millisecond, "the message whose file was going up is back in the input")
	assert.Contains(t, view(p), "the upload stopped as the chat left its thread")
	select {
	case c := <-f.calls:
		t.Fatalf("the old thread got %s", c.kind)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPane_ImagesOnlyBackendRefusesFilesButTakesImages(t *testing.T) {
	f := newFakeBackend(fixture())
	p := startPaneOn(t, imagesOnly{f}, f, 80, 14)
	f.files["/a/shot.png"] = imageFile(Image{Name: "shot.png", MIME: "image/png", Data: []byte("png")})
	f.files["/a/notes.txt"] = fakeFile{content: "notes"}
	f.clip.set(Clip{Files: []string{"/a/shot.png", "/a/notes.txt"}}, nil)
	p.SendText("\x16")
	settle(t, p)
	assert.Equal(t, "[Image #1] ", draft(p))
	assert.Equal(t, "1 attached: [Image #1] shot.png, 3 B; left out: notes.txt: this agent takes images only", p.note)
}

func TestPane_TranscriptListsAttachments(t *testing.T) {
	conv := fixture()
	conv.Entries = []Entry{UserMessage{Text: "look at these", Attachments: []Attachment{
		{Kind: AttachedImage, Name: "shot.png", Size: 120 << 10},
		{Kind: AttachedFile, Name: "report.pdf", Size: 2400 << 10},
		{Kind: "snapshot", Name: "window", Size: 7},
		{Kind: AttachedFile, Name: "evil\x1b]52;c;aGk=\x07\nname.txt", Size: 3},
	}}}
	p, _ := newTestPane(t, 60, 20, conv)
	v := view(p)
	assert.Contains(t, v, "┃  look at these")
	assert.Contains(t, v, "┃  📎 shot.png · 120 KB · image")
	assert.Contains(t, v, "┃  📎 report.pdf · 2.3 MB", "a file needs no kind")
	assert.Contains(t, v, "┃  📎 window · 7 B · snapshot", "a kind the pane does not know still shows")
	assert.Contains(t, v, "┃  📎 evil name.txt · 3 B", "a hostile name is shown on one line, without its escapes")
	raw := p.Render(true)
	assert.NotContains(t, raw, "]52;", "no clipboard write reaches the terminal")

	p2, _ := newTestPane(t, 60, 20, fixture())
	lines := strings.Split(view(p2), "\n")
	for _, l := range lines {
		assert.NotContains(t, l, "📎", "a message with no attachments lists none")
	}
}

func TestPane_UnqueueSaysWhatStayedBehind(t *testing.T) {
	conv := fixture()
	conv.Queued = []Queued{{ID: "m4", Text: "fix the tests too", Images: 1, Files: 2}}
	p, f := newTestPane(t, 80, 14, conv)
	ask(t, p, f, func() {
		p.SendKey(uv.KeyPressEvent{Code: uv.KeyUp})
	})
	assert.Equal(t, "the queued message is back in the input: enter queues it again, ctrl+u drops it; its 1 image and 2 files stayed behind", p.note)
}

func TestSendTimeout(t *testing.T) {
	assert.Equal(t, requestTimeout, sendTimeout(0))
	assert.Equal(t, requestTimeout+800*time.Second, sendTimeout(50<<20), "50 MB at 64 KB/s")
}

func TestUploadProgress(t *testing.T) {
	var u uploadProgress
	now := time.Unix(0, 0)
	assert.False(t, u.report("m1", "a", 1, 10, now), "nothing reported before the send starts")
	u.start("m1")
	assert.True(t, u.moved())
	assert.True(t, u.report("m1", "a", 1, 10, now), "a file starting redraws")
	assert.False(t, u.report("m1", "a", 2, 10, now.Add(10*time.Millisecond)), "not more than every 100 ms")
	assert.True(t, u.moved(), "though the next frame shows it")
	assert.False(t, u.moved())
	assert.True(t, u.report("m1", "a", 3, 10, now.Add(200*time.Millisecond)))
	assert.True(t, u.report("m1", "a", 10, 10, now.Add(210*time.Millisecond)), "a file ending redraws")
	assert.Equal(t, "Uploading a: 10 B of 10 B · esc cancels", u.label("m1"))
	assert.Empty(t, u.label("m2"))
	u.stop("m2")
	assert.NotEmpty(t, u.label("m1"), "another message's end leaves it")
	u.stop("m1")
	assert.Empty(t, u.label("m1"))
}

// settle waits for the pane's clipboard and file reads to land: pasting
// copied files reads the clipboard, then looks at the files.
func settle(t *testing.T, p *Pane) {
	t.Helper()
	require.Eventually(t, func() bool {
		p.lock()
		defer p.mu.Unlock()
		return p.reading == 0
	}, waitLimit, time.Millisecond, "the pane never settled")
}

// draft is the input's text, read under the pane's lock.
func draft(p *Pane) string {
	p.lock()
	defer p.mu.Unlock()
	return p.input.text()
}

func TestPane_EscTooLateSaysTheMessageWent(t *testing.T) {
	f := newFakeBackend(fixture())
	f.uploading = make(chan struct{})
	p := New(f, "Fix login", 80, 16, Config{NoColor: true, Clipboard: f.clip, Files: f.files})
	t.Cleanup(p.Close)
	awaitShown(t, p, f)
	f.files["/a/big.iso"] = fakeFile{content: "iso"}
	p.SendText("/attach /a/big.iso\r")
	settle(t, p)
	hold := make(chan struct{})
	f.set(nil, hold) // the files are up; the message itself is on its way
	p.SendText("\r")
	require.Equal(t, "upload", nextCall(t, f).kind)
	close(f.uploading)
	require.Equal(t, "send", nextCall(t, f).kind)
	p.SendText("\x1b") // esc while the message goes out: the backend sends it anyway
	close(hold)
	require.Eventually(t, func() bool {
		p.lock()
		defer p.mu.Unlock()
		return !p.busy
	}, waitLimit, time.Millisecond)
	assert.Contains(t, view(p), "the files were already up; the message went out")
	assert.Empty(t, draft(p), "nothing comes back into the input")
}
