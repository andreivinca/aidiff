package splitview

import (
	"io"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
)

// Most terminals send shift+enter as a plain enter. Asked for xterm's
// modifyOtherKeys at level 1, they report the keys a modifier would otherwise
// be lost on (shift+enter, ctrl+enter, ctrl+shift+letter...) as
// "CSI 27 ; mod ; code ~", and leave every other key as it was. Some
// terminals report them in the CSI u form, "CSI code ; mod u", instead.
// Bubble Tea v1 names neither form, so the split decodes them: a pane that
// takes modified keys (modifiedKeyReceiver) gets the key with its modifiers,
// and everyone else gets the key a terminal without the mode would have sent.
//
// While the session switcher is open the terminal may also be in the kitty
// keyboard protocol's all-keys mode (keyreports.go), which reports every key
// in the CSI u form, with whether it was pressed, repeated or released. The
// same decoder reads those reports.
const (
	enableModifiedKeys  = "\x1b[>4;1m"
	disableModifiedKeys = "\x1b[>4m"
)

// ReportModifiedKeys asks the terminal behind out to report modified keys,
// and returns the call that restores its default. Write it before the
// program starts and restore once it has ended.
func ReportModifiedKeys(out io.Writer) (restore func()) {
	_, _ = io.WriteString(out, enableModifiedKeys)
	return func() {
		_, _ = io.WriteString(out, disableModifiedKeys)
	}
}

// modifiedKeyReceiver is a pane that acts on keys with their modifiers, such
// as a chat that inserts a line break on shift+enter. SendModifiedKey
// reports whether the pane took k; a key it did not take reaches it as the
// plain key a terminal without modified-key reports would have sent.
type modifiedKeyReceiver interface {
	SendModifiedKey(k uv.KeyPressEvent) bool
}

// keyEvent is what a key report says happened to its key.
type keyEvent int

const (
	keyPress keyEvent = iota + 1
	keyRepeat
	keyRelease
)

// keyReport is a decoded key report: the key, with its modifiers, and what
// happened to it.
type keyReport struct {
	key   uv.KeyPressEvent
	event keyEvent
}

// Modifier bits in the parameter "1 + bits", xterm's and the kitty
// protocol's; the kitty protocol adds super, hyper, meta and the locks.
const (
	xtermShift = 1 << iota
	xtermAlt
	xtermCtrl
	kittySuper
	kittyHyper
	kittyMeta
	kittyCapsLock
	kittyNumLock
)

// The kitty protocol's codes of the modifier and lock keys, which it
// reports as keys of their own in its all-keys mode.
const (
	kittyCapsLockKey = 57358
	kittyNumLockKey  = 57360
	kittyLeftShift   = 57441
	kittyLeftCtrl    = 57442
	kittyLeftAlt     = 57443
	kittyLeftSuper   = 57444
	kittyLeftHyper   = 57445
	kittyLeftMeta    = 57446
	kittyRightShift  = 57447
	kittyRightCtrl   = 57448
	kittyRightAlt    = 57449
	kittyRightSuper  = 57450
	kittyRightHyper  = 57451
	kittyRightMeta   = 57452
	kittyLevel5Shift = 57454
)

// modifierOf is the modifier that key code k is, 0 for any other key.
func modifierOf(k rune) uv.KeyMod {
	switch k {
	case kittyLeftShift, kittyRightShift:
		return uv.ModShift
	case kittyLeftCtrl, kittyRightCtrl:
		return uv.ModCtrl
	case kittyLeftAlt, kittyRightAlt:
		return uv.ModAlt
	case kittyLeftSuper, kittyRightSuper:
		return uv.ModSuper
	case kittyLeftHyper, kittyRightHyper:
		return uv.ModHyper
	case kittyLeftMeta, kittyRightMeta:
		return uv.ModMeta
	}
	return 0
}

// isModifierKey reports whether key code k is a modifier or a lock, which
// type nothing.
func isModifierKey(k rune) bool {
	return (k >= kittyLeftShift && k <= kittyLevel5Shift) || (k >= kittyCapsLockKey && k <= kittyNumLockKey)
}

// kittyFunctional are the kitty protocol's codes for keys that have no
// character, as sent in the CSI u form.
var kittyFunctional = map[int]rune{
	57344: uv.KeyEscape, 57345: uv.KeyEnter, 57346: uv.KeyTab, 57347: uv.KeyBackspace,
	57348: uv.KeyInsert, 57349: uv.KeyDelete, 57350: uv.KeyLeft, 57351: uv.KeyRight,
	57352: uv.KeyUp, 57353: uv.KeyDown, 57354: uv.KeyPgUp, 57355: uv.KeyPgDown,
	57356: uv.KeyHome, 57357: uv.KeyEnd,
}

// kittyLetter are the keys the kitty protocol reports as "CSI 1 ; mods X",
// by X.
var kittyLetter = map[byte]rune{
	'A': uv.KeyUp, 'B': uv.KeyDown, 'C': uv.KeyRight, 'D': uv.KeyLeft,
	'H': uv.KeyHome, 'F': uv.KeyEnd, 'E': uv.KeyBegin,
	'P': uv.KeyF1, 'Q': uv.KeyF2, 'R': uv.KeyF3, 'S': uv.KeyF4,
}

// kittyTilde are the keys reported as "CSI number ; mods ~", by number.
var kittyTilde = map[int]rune{
	2: uv.KeyInsert, 3: uv.KeyDelete, 5: uv.KeyPgUp, 6: uv.KeyPgDown, 7: uv.KeyHome, 8: uv.KeyEnd,
	11: uv.KeyF1, 12: uv.KeyF2, 13: uv.KeyF3, 14: uv.KeyF4, 15: uv.KeyF5,
	17: uv.KeyF6, 18: uv.KeyF7, 19: uv.KeyF8, 20: uv.KeyF9, 21: uv.KeyF10, 23: uv.KeyF11, 24: uv.KeyF12,
}

// parseKeyReport decodes a key report Bubble Tea leaves unnamed: xterm's
// "CSI 27 ; mods ; code ~", and the kitty protocol's
// "CSI code[:alternates] [; mods[:event] [; text]] u", with its
// "CSI 1 ; mods : event X" and "CSI number ; mods : event ~" forms for the
// keys it reports the legacy way. Those last two reach the split raw only
// when they carry an event: without one Bubble Tea names them, or, like
// ctrl+delete, they are a legacy key a pane may know, left as they come.
func parseKeyReport(seq []byte) (keyReport, bool) {
	s := string(seq)
	if len(s) < 4 || !strings.HasPrefix(s, "\x1b[") {
		return keyReport{}, false
	}
	body, final := s[2:len(s)-1], s[len(s)-1]
	if strings.Trim(body, "0123456789;:") != "" {
		return keyReport{}, false // a private marker (a reply), or not a key at all
	}
	fields := strings.Split(body, ";")
	sub := func(f, i int) string { // sub-parameter i of field f, "" when absent
		if f >= len(fields) {
			return ""
		}
		parts := strings.Split(fields[f], ":")
		if i >= len(parts) {
			return ""
		}
		return parts[i]
	}
	hasEvent := len(fields) == 2 && strings.Contains(fields[1], ":")
	var code rune
	switch {
	case final == 'u' && len(fields) <= 3:
		c, ok := number(sub(0, 0), -1)
		if !ok || c < 0 {
			return keyReport{}, false
		}
		code = kittyCode(c)
	case final == '~' && len(fields) == 3 && fields[0] == "27" && !strings.Contains(body, ":"):
		c, ok := number(fields[2], -1)
		if !ok || c < 0 {
			return keyReport{}, false
		}
		mods, ok := number(fields[1], 1)
		if !ok || mods < 1 {
			return keyReport{}, false
		}
		return keyReport{key: uv.KeyPressEvent{Code: rune(c), Mod: modsOf(mods)}, event: keyPress}, true
	case final == '~' && hasEvent:
		n, _ := number(sub(0, 0), 0)
		c, ok := kittyTilde[n]
		if !ok {
			return keyReport{}, false
		}
		code = c
	case kittyLetter[final] != 0 && hasEvent && (fields[0] == "" || fields[0] == "1"):
		code = kittyLetter[final]
	default:
		return keyReport{}, false
	}
	mods, ok := number(sub(1, 0), 1)
	if !ok || mods < 1 {
		return keyReport{}, false
	}
	event, ok := number(sub(1, 1), int(keyPress))
	if !ok || event < int(keyPress) || event > int(keyRelease) {
		return keyReport{}, false
	}
	k := uv.KeyPressEvent{Code: code, Mod: modsOf(mods)}
	if c := sub(0, 0); final == 'u' && c != "" {
		k = withC0(k, c)
	}
	return keyReport{key: k, event: keyEvent(event)}, true
}

// number parses a decimal parameter: def when it is empty, false when it is
// out of range for a key.
func number(s string, def int) (int, bool) {
	if s == "" {
		return def, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n > unicode.MaxRune {
		return 0, false
	}
	return n, true
}

// kittyCode is the key a CSI u code names.
func kittyCode(c int) rune {
	if k, ok := kittyFunctional[c]; ok {
		return k
	}
	if c == 8 {
		return uv.KeyBackspace
	}
	return rune(c)
}

// withC0 reads a C0 control code sent as a key, as some terminals do for
// ctrl+letter (WezTerm), as that letter with ctrl; tab, enter, backspace and
// escape stay themselves.
func withC0(k uv.KeyPressEvent, code string) uv.KeyPressEvent {
	c, _ := strconv.Atoi(code)
	switch {
	case c == 0:
		return uv.KeyPressEvent{Code: uv.KeySpace, Mod: k.Mod | uv.ModCtrl}
	case c >= 1 && c <= 26 && c != 8 && c != 9 && c != 13:
		return uv.KeyPressEvent{Code: rune(c + 0x60), Mod: k.Mod | uv.ModCtrl}
	}
	return k
}

// modsOf reads the modifier parameter "1 + bits"; the locks are left out,
// since caps lock or num lock being on changes no key the split acts on.
func modsOf(param int) uv.KeyMod {
	bits := param - 1
	var mods uv.KeyMod
	for _, m := range []struct {
		bit int
		mod uv.KeyMod
	}{{xtermShift, uv.ModShift}, {xtermAlt, uv.ModAlt}, {xtermCtrl, uv.ModCtrl}, {kittySuper, uv.ModSuper}, {kittyHyper, uv.ModHyper}, {kittyMeta, uv.ModMeta}} {
		if bits&m.bit != 0 {
			mods |= m.mod
		}
	}
	return mods
}

// legacyArrows are the keys Bubble Tea names with their shift and ctrl, by
// the key: plain, shift, ctrl, ctrl+shift.
var legacyArrows = map[rune][4]tea.KeyType{
	uv.KeyUp:    {tea.KeyUp, tea.KeyShiftUp, tea.KeyCtrlUp, tea.KeyCtrlShiftUp},
	uv.KeyDown:  {tea.KeyDown, tea.KeyShiftDown, tea.KeyCtrlDown, tea.KeyCtrlShiftDown},
	uv.KeyRight: {tea.KeyRight, tea.KeyShiftRight, tea.KeyCtrlRight, tea.KeyCtrlShiftRight},
	uv.KeyLeft:  {tea.KeyLeft, tea.KeyShiftLeft, tea.KeyCtrlLeft, tea.KeyCtrlShiftLeft},
	uv.KeyHome:  {tea.KeyHome, tea.KeyShiftHome, tea.KeyCtrlHome, tea.KeyCtrlShiftHome},
	uv.KeyEnd:   {tea.KeyEnd, tea.KeyShiftEnd, tea.KeyCtrlEnd, tea.KeyCtrlShiftEnd},
}

// legacyKey is the key a terminal without modified-key reports sends for k:
// the modifier the legacy encoding cannot carry is dropped. It reports false
// for a key that has no legacy form.
func legacyKey(k uv.KeyPressEvent) (tea.KeyMsg, bool) {
	alt := k.Mod&uv.ModAlt != 0
	if v, ok := legacyArrows[k.Code]; ok {
		i := 0
		if k.Mod&uv.ModShift != 0 {
			i |= 1
		}
		if k.Mod&uv.ModCtrl != 0 {
			i |= 2
		}
		return tea.KeyMsg{Type: v[i], Alt: alt}, true
	}
	switch {
	case k.Code == uv.KeyTab && k.Mod&uv.ModShift != 0:
		return tea.KeyMsg{Type: tea.KeyShiftTab, Alt: alt}, true
	case k.Code == uv.KeyEnter, k.Code == uv.KeyTab, k.Code == uv.KeyEscape:
		return tea.KeyMsg{Type: tea.KeyType(k.Code), Alt: alt}, true
	case k.Code == uv.KeyBackspace:
		return tea.KeyMsg{Type: tea.KeyBackspace, Alt: alt}, true
	case k.Code == uv.KeySpace && k.Mod&uv.ModCtrl != 0:
		return tea.KeyMsg{Type: tea.KeyCtrlAt, Alt: alt}, true
	case k.Code == uv.KeySpace:
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}, Alt: alt}, true
	case k.Mod&uv.ModCtrl != 0 && unicode.IsLetter(k.Code) && k.Code < unicode.MaxASCII:
		return tea.KeyMsg{Type: tea.KeyType(unicode.ToLower(k.Code) & 0x1f), Alt: alt}, true
	case k.Mod&uv.ModCtrl == 0 && k.Code <= unicode.MaxRune && unicode.IsPrint(k.Code):
		r := k.Code
		if k.Mod&uv.ModShift != 0 {
			r = unicode.ToUpper(r)
		}
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: alt}, true
	}
	return tea.KeyMsg{}, false
}

// handleKeyReport routes a decoded key report: to the open switcher, which
// takes every key; otherwise a press goes on as a modified key. A release,
// or a modifier key, is only reported while the switcher's flags are on the
// terminal; one that comes after the switcher closed, before the terminal
// took them back, has nothing to type and goes nowhere.
func (m Model) handleKeyReport(r keyReport, seq []byte, raw tea.Msg) (tea.Model, tea.Cmd) {
	if m.switcher.open {
		return m.switcherKeyReport(r)
	}
	if r.event == keyRelease || isModifierKey(r.key.Code) {
		return m, nil
	}
	return m.handleModifiedKey(r.key, seq, raw)
}

// handleModifiedKey routes a decoded modified key: to the focused pane when
// it takes modified keys, otherwise through the split as its legacy key. A
// key with no legacy form goes on as the raw sequence seq (forwardRaw).
func (m Model) handleModifiedKey(k uv.KeyPressEvent, seq []byte, raw tea.Msg) (tea.Model, tea.Cmd) {
	// the switcher's keys are claimed whatever has focus, a terminal
	// included, before they could reach it or be taken for a plain tab
	if model, cmd, ok := m.switcherModifiedKey(k); ok {
		return model, cmd
	}
	if n, ok := numberKey(k); ok && !m.modal() {
		return m.selectTab(n)
	}
	if m.focus >= 0 && !m.modal() {
		if r, ok := m.terms[m.focus].(modifiedKeyReceiver); ok && r.SendModifiedKey(k) {
			return m, nil
		}
	}
	if legacy, ok := legacyKey(k); ok {
		return m.update(legacy)
	}
	return m.forwardRaw(seq, raw)
}
