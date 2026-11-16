package chat

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// SendText takes typed bytes: text goes into the input line, control bytes
// edit it (backspace, ctrl+a, ctrl+e, ctrl+u, ctrl+w) or send it (enter).
// With the slash-command popup open, tab completes the selected row, enter
// completes it and runs a complete command, esc closes the popup. Alt chords
// are dropped, except alt+enter, which inserts a line break (shift+enter
// arrives through SendModifiedKey).
func (p *Pane) SendText(s string) {
	p.lock()
	defer p.mu.Unlock()
	if p.selection.rows != nil {
		p.selection = textSelection{}
		p.gen++
		if s == "\x1b" {
			return
		}
	}
	p.note = ""
	p.gen++
	if p.modalTextLocked(s) || p.modifiedTextLocked(s) {
		return
	}
	for _, r := range s {
		p.typeLocked(r)
	}
}

// typeLocked takes one typed character or control byte; mu must be held.
func (p *Pane) typeLocked(r rune) {
	switch r {
	case '\r':
		if !p.applySuggestionLocked(true) {
			p.submitLocked()
		}
	case '\t':
		p.applySuggestionLocked(false)
	case '\x1b':
		p.escapeLocked()
	case '\n':
		p.insertLocked("\n")
	case 0x7f, '\b':
		p.input.backspace()
		p.inputChanged()
	case 0x01: // ctrl+a
		p.input.home()
	case 0x05: // ctrl+e
		p.input.end()
	case 0x16: // ctrl+v
		p.pasteClipboardLocked()
	case 0x15: // ctrl+u
		p.clearDraftLocked()
	case 0x17: // ctrl+w
		p.input.killWord()
		p.inputChanged()
	default:
		if r >= 0x20 {
			p.insertLocked(string(r))
		}
	}
}

// insertLocked puts text into the input at the cursor; mu must be held.
func (p *Pane) insertLocked(text string) {
	p.input.insert(text)
	p.inputChanged()
}

// modifiedTextLocked handles alt chords typed as text: alt+enter inserts a
// line break, any other is dropped.
func (p *Pane) modifiedTextLocked(s string) bool {
	if !strings.HasPrefix(s, "\x1b") || len(s) <= 1 {
		return false
	}
	if s == "\x1b\r" || s == "\x1b\n" {
		p.insertLocked("\n")
	}
	return true
}

// SendModifiedKey takes an enter with a modifier (shift+enter, ctrl+enter,
// alt+enter) as a line break in the input, and reports whether it took k.
// Any other key, and every key while the stop dialog or the activity view is
// open, is left to arrive as its plain key.
func (p *Pane) SendModifiedKey(k uv.KeyPressEvent) bool {
	p.lock()
	defer p.mu.Unlock()
	if k.Code != uv.KeyEnter || k.Mod == 0 || p.stop.turn != "" || p.activity.open {
		return false
	}
	p.selection = textSelection{}
	p.note = ""
	p.gen++
	p.insertLocked("\n")
	return true
}

// SendKey moves the cursor (left, right, home, end, delete), picks a row of
// the slash-command popup (up, down) or scrolls the transcript (up, down,
// page up, page down, and end once the cursor is at the end of the input).
func (p *Pane) SendKey(k uv.KeyPressEvent) {
	p.lock()
	defer p.mu.Unlock()
	p.selection = textSelection{}
	p.note = ""
	p.gen++
	switch {
	case p.stop.turn != "":
		p.stopKeyLocked(k)
	case p.activity.open:
		p.activityKeyLocked(k)
	default:
		p.composerKeyLocked(k)
	}
}

// composerKeyLocked handles a key for the input and the transcript; mu must
// be held.
func (p *Pane) composerKeyLocked(k uv.KeyPressEvent) {
	switch k.Code {
	case uv.KeyLeft:
		if p.input.empty() && k.Mod == 0 {
			p.activity.open = true
		} else {
			p.input.left()
		}
	case uv.KeyRight:
		p.input.right()
	case uv.KeyHome:
		p.input.home()
	case uv.KeyEnd:
		p.endLocked()
	case uv.KeyDelete:
		p.input.del()
		p.inputChanged()
	case uv.KeyUp:
		if !p.moveSuggestionLocked(-1) {
			p.scrollLocked(1)
		}
	case uv.KeyDown:
		if !p.moveSuggestionLocked(1) {
			p.scrollLocked(-1)
		}
	case uv.KeyPgUp:
		p.scrollLocked(p.pageLocked())
	case uv.KeyPgDown:
		p.scrollLocked(-p.pageLocked())
	}
}

// endLocked moves the cursor to the end of the input. With the cursor
// already there, as it is while typing, it jumps to the bottom of the
// transcript, which the header offers while scrolled up. mu must be held.
func (p *Pane) endLocked() {
	if p.input.atEnd() {
		p.scroll = 0
		return
	}
	p.input.end()
}

// Paste inserts text at the cursor, line breaks included. A single line that
// names an image file attaches the file instead.
func (p *Pane) Paste(s string) {
	p.lock()
	defer p.mu.Unlock()
	p.selection = textSelection{}
	p.note = ""
	p.gen++
	p.activity.open = false
	p.stop = stopConfirmation{}
	if path := strings.TrimSpace(s); !strings.Contains(path, "\n") && p.files.IsImage(path) {
		p.attachFileLocked(path, s)
		return
	}
	p.insertLocked(strings.ReplaceAll(s, "\r\n", "\n"))
}

// SendMouse selects text or scrolls the transcript with the wheel.
func (p *Pane) SendMouse(m vt.Mouse) {
	ev, wheel := m.(uv.MouseWheelEvent)
	if !wheel {
		p.selectionMouse(m)
		return
	}
	p.lock()
	defer p.mu.Unlock()
	var delta int
	switch ev.Button {
	case uv.MouseWheelUp:
		delta = -3
	case uv.MouseWheelDown:
		delta = 3
	default:
		return
	}
	switch {
	case p.stop.turn != "":
		return
	case p.activity.open:
		p.moveActivityLocked(delta)
	default:
		p.scrollLocked(-delta)
	}
	p.gen++
}

// scrollLocked moves the transcript n lines towards older output, as far as
// the last frame had lines to show. mu must be held.
func (p *Pane) scrollLocked(n int) {
	p.selection = textSelection{}
	top := p.frame.layout.maxScroll
	p.scroll = max(0, min(p.scroll, top)+n)
	p.scroll = min(p.scroll, top)
}

// pageLocked is how far a page key scrolls the transcript; mu must be held.
func (p *Pane) pageLocked() int {
	return max(1, p.frame.layout.bodyH-1)
}
