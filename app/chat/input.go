package chat

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// SendText takes typed bytes: text goes into the input line, control bytes
// edit it (backspace, ctrl+backspace, ctrl+a, ctrl+e, ctrl+u, ctrl+w), copy
// the selection (ctrl+c) or send it (enter), and
// the escape sequences of modified arrows, home, end and delete move by word
// and select (see editKeyLocked).
// With the slash-command popup open, tab completes the selected row, enter
// completes it and runs a complete command, esc closes the popup. Alt chords
// are dropped, except alt+enter, which inserts a line break (shift+enter
// arrives through SendModifiedKey).
func (p *Pane) SendText(s string) {
	p.lock()
	defer p.mu.Unlock()
	if s == "\x03" && p.stop.turn == "" && !p.activity.open { // ctrl+c, before a key drops the selection
		p.copyKeyLocked()
		return
	}
	if p.selection.rows != nil {
		p.selection = textSelection{}
		p.gen++
		if s == "\x1b" {
			return
		}
	}
	p.gen++
	if p.secretGoneTextLocked(s) { // a secret request just ended (secret.go)
		return
	}
	p.note = ""
	if p.secretTextLocked(s) { // aidiff: a secret the agent asks for (secret.go)
		return
	}
	if s == "\x1b[Z" && p.moveQuestionLocked(-1) {
		return
	}
	if p.modalTextLocked(s) || p.editKeyLocked(s) || p.modifiedTextLocked(s) {
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
		if !p.moveQuestionLocked(1) {
			p.applySuggestionLocked(false)
		}
	case '\x1b':
		if _, _, ok := p.input.selection(); ok {
			p.input.deselect()
			return
		}
		p.escapeLocked()
	case '\n':
		p.insertLocked("\n")
	case 0x7f:
		p.input.backspace()
		p.inputChanged()
	case '\b': // ctrl+backspace, as most terminals send it (and ctrl+h)
		p.input.deleteWordLeft()
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

// insertLocked puts text into the input at the cursor, without the escape
// sequences and control characters a paste may carry; mu must be held.
func (p *Pane) insertLocked(text string) {
	p.input.insert(sanitize(text))
	p.inputChanged()
}

// editKeySeq is a modified arrow, home or end ("CSI 1 ; mod X") or delete
// ("CSI 3 ; mod ~"), as xterm encodes them.
var editKeySeq = regexp.MustCompile(`^\x1b\[(?:1;(\d+)([ABCDHF])|3;(\d+)~)$`)

// xterm modifier bits in the parameter "1 + bits".
const (
	modShift = 1 << iota
	modAlt
	modCtrl
)

// editKeyLocked takes a modified editing key and reports whether s was one:
// ctrl (or alt) with ← → moves by word and with delete deletes the word
// after the cursor; shift with ← → home end extends the selection. A
// modified ↑ ↓ is dropped. mu must be held.
func (p *Pane) editKeyLocked(s string) bool {
	m := editKeySeq.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	param, key := m[1], m[2]
	if key == "" {
		param, key = m[3], "~"
	}
	n, err := strconv.Atoi(param)
	if err != nil || n < 1 {
		return true
	}
	bits := n - 1
	word, extend := bits&(modCtrl|modAlt) != 0, bits&modShift != 0
	e := &p.input
	switch key {
	case "D":
		if word {
			e.moveTo(e.wordLeft(), extend)
		} else if extend {
			e.moveTo(e.cur-1, true)
		} else {
			e.left()
		}
	case "C":
		if word {
			e.moveTo(e.wordRight(), extend)
		} else if extend {
			e.moveTo(e.cur+1, true)
		} else {
			e.right()
		}
	case "H":
		e.moveTo(0, extend)
	case "F":
		e.moveTo(len(e.clusters), extend)
	case "~":
		if word {
			e.deleteWordRight()
		} else {
			e.del()
		}
		p.inputChanged()
	}
	return true
}

// modifiedTextLocked handles alt chords typed as text: alt+enter inserts a
// line break, alt+backspace deletes the word before the cursor, any other is
// dropped.
func (p *Pane) modifiedTextLocked(s string) bool {
	if !strings.HasPrefix(s, "\x1b") || len(s) <= 1 {
		return false
	}
	switch s {
	case "\x1b\r", "\x1b\n":
		p.insertLocked("\n")
	case "\x1b\x7f", "\x1b\b":
		p.input.deleteWordLeft()
		p.inputChanged()
	}
	return true
}

// copyKeyLocked copies the selection, for ctrl+c and ctrl+shift+c: the text
// selected in the input, else the transcript text the mouse selected, which
// stays selected. mu must be held.
func (p *Pane) copyKeyLocked() {
	p.note = ""
	p.gen++
	if text := p.input.selected(); text != "" {
		p.copySelectionLocked(text)
		return
	}
	if text := p.selection.text(p.width); p.selection.rows != nil && text != "" {
		p.copySelectionLocked(text)
		return
	}
	p.note = "nothing selected to copy"
}

// SendModifiedKey takes an enter with a modifier (shift+enter, ctrl+enter,
// alt+enter) as a line break in the input, a backspace with ctrl or alt as
// deleting the word before the cursor, and ctrl+shift+c as copying the
// selection; it reports whether it took k. Any
// other key, and every key while the stop dialog or the activity view is
// open, is left to arrive as its plain key.
func (p *Pane) SendModifiedKey(k uv.KeyPressEvent) bool {
	p.lock()
	defer p.mu.Unlock()
	if k.Mod == 0 || p.stop.turn != "" || p.activity.open {
		return false
	}
	if p.secretLocked() != nil || p.secretGone {
		return true // no line breaks or word edits in a secret, nor in what was meant for one
	}
	if k.Mod&(uv.ModCtrl|uv.ModShift) == uv.ModCtrl|uv.ModShift && unicode.ToLower(k.Code) == 'c' {
		p.copyKeyLocked()
		return true
	}
	wordBack := k.Code == uv.KeyBackspace && k.Mod&(uv.ModCtrl|uv.ModAlt) != 0
	if k.Code != uv.KeyEnter && !wordBack {
		return false
	}
	p.selection = textSelection{}
	p.note = ""
	p.gen++
	if wordBack {
		p.input.deleteWordLeft()
		p.inputChanged()
		return true
	}
	p.insertLocked("\n")
	return true
}

// SendKey moves the cursor (left, right, home, end, delete, and up and down
// between the input's rows), picks a row of the slash-command popup (up,
// down) or scrolls the transcript (up on the input's first row, down on its
// last, page up, page down, and end once the cursor is at the end of the
// input).
func (p *Pane) SendKey(k uv.KeyPressEvent) {
	p.lock()
	defer p.mu.Unlock()
	p.selection = textSelection{}
	p.gen++
	if p.secretGone { // a secret request just ended (secret.go): only enter or esc gets the composer back
		if k.Code == uv.KeyEnter || k.Code == uv.KeyEscape {
			p.secretGone, p.note = false, ""
		}
		return
	}
	p.note = ""
	switch {
	case p.stop.turn != "":
		p.stopKeyLocked(k)
	case p.activity.open:
		p.activityKeyLocked(k)
	case p.secretKeyLocked(k):
	default:
		p.composerKeyLocked(k)
	}
}

// composerKeyLocked handles a key for the input and the transcript; mu must
// be held.
func (p *Pane) composerKeyLocked(k uv.KeyPressEvent) {
	if p.input.empty() && !p.busy {
		if q := p.activeChoiceLocked(); q != nil && len(q.Options) > 0 && (k.Code == uv.KeyUp || k.Code == uv.KeyDown) {
			delta := 1
			if k.Code == uv.KeyUp {
				delta = -1
			}
			p.questionChoice = (p.questionChoice + delta + len(q.Options) + 1) % (len(q.Options) + 1)
			return
		}
		if (k.Code == uv.KeyUp || k.Code == uv.KeyDown) && p.pickedApprovalLocked() != "" {
			p.approvalChoice = (p.approvalChoice + 1) % len(approvalChoices) // two rows: either key moves to the other
			return
		}
	}
	switch k.Code {
	case uv.KeyTab:
		delta := 1
		if k.Mod&uv.ModShift != 0 {
			delta = -1
		}
		if !p.moveQuestionLocked(delta) {
			p.applySuggestionLocked(false)
		}
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
		switch {
		case p.moveSuggestionLocked(-1):
		case p.unqueueLastLocked():
		case p.input.vertical(-1, inputWidth(p.width), k.Mod&uv.ModShift != 0):
		default:
			p.scrollLocked(1)
		}
	case uv.KeyDown:
		if !p.moveSuggestionLocked(1) && !p.input.vertical(1, inputWidth(p.width), k.Mod&uv.ModShift != 0) {
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

// Paste inserts text at the cursor, line breaks included. A single line
// that names files (an absolute or "~/" path, a file:// URI, quoted or
// escaped as terminals paste a dropped file) attaches them instead, and goes
// in as text when it names none that can be attached.
func (p *Pane) Paste(s string) {
	p.lock()
	defer p.mu.Unlock()
	p.selection = textSelection{}
	p.gen++
	if p.secretGone {
		return // a password pasted for the secret request that just ended
	}
	p.note = ""
	p.activity.open = false
	p.stop = stopConfirmation{}
	if p.secretPasteLocked(s) {
		return
	}
	if paths := pastedPaths(s); paths != nil {
		p.attachPathsLocked(paths, s)
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
	var dir int
	switch ev.Button {
	case uv.MouseWheelUp:
		dir = -1
	case uv.MouseWheelDown:
		dir = 1
	default:
		return
	}
	switch {
	case p.stop.turn != "":
		return
	case p.activity.open:
		p.pageActivityLocked(dir * wheelStep)
	default:
		p.scrollLocked(-dir * p.wheel.step(dir, p.now()))
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
