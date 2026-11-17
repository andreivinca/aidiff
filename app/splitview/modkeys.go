package splitview

import (
	"io"
	"regexp"
	"strconv"
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

var (
	modifyOtherKeysRe = regexp.MustCompile(`^\x1b\[27;(\d+);(\d+)~$`)
	csiURe            = regexp.MustCompile(`^\x1b\[(\d+);(\d+)u$`)
)

// xterm modifier bits in the parameter "1 + bits".
const (
	xtermShift = 1 << iota
	xtermAlt
	xtermCtrl
)

// parseModifiedKey decodes a modified-key report in either form.
func parseModifiedKey(seq []byte) (uv.KeyPressEvent, bool) {
	var mod, code string
	if m := modifyOtherKeysRe.FindSubmatch(seq); m != nil {
		mod, code = string(m[1]), string(m[2])
	} else if m := csiURe.FindSubmatch(seq); m != nil {
		code, mod = string(m[1]), string(m[2])
	} else {
		return uv.KeyPressEvent{}, false
	}
	modValue, err := strconv.Atoi(mod)
	if err != nil || modValue < 1 {
		return uv.KeyPressEvent{}, false
	}
	codeValue, err := strconv.Atoi(code)
	if err != nil || codeValue < 0 || codeValue > unicode.MaxRune {
		return uv.KeyPressEvent{}, false
	}
	bits := modValue - 1
	var mods uv.KeyMod
	if bits&xtermShift != 0 {
		mods |= uv.ModShift
	}
	if bits&xtermAlt != 0 {
		mods |= uv.ModAlt
	}
	if bits&xtermCtrl != 0 {
		mods |= uv.ModCtrl
	}
	return uv.KeyPressEvent{Code: rune(codeValue), Mod: mods}, true
}

// legacyKey is the key a terminal without modified-key reports sends for k:
// the modifier the legacy encoding cannot carry is dropped. It reports false
// for a key that has no legacy form.
func legacyKey(k uv.KeyPressEvent) (tea.KeyMsg, bool) {
	alt := k.Mod&uv.ModAlt != 0
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
	case k.Mod&uv.ModCtrl == 0 && unicode.IsPrint(k.Code):
		r := k.Code
		if k.Mod&uv.ModShift != 0 {
			r = unicode.ToUpper(r)
		}
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: alt}, true
	}
	return tea.KeyMsg{}, false
}

// handleModifiedKey routes a decoded modified key: to the focused pane when
// it takes modified keys, otherwise through the split as its legacy key.
func (m Model) handleModifiedKey(k uv.KeyPressEvent, raw tea.Msg) (tea.Model, tea.Cmd) {
	if n, ok := numberKey(k); ok && m.sidebarAvailable() && !m.palette.open && !m.quitDialog {
		return m.selectNumber(n)
	}
	if m.focus >= 0 && !m.palette.open && !m.quitDialog {
		if r, ok := m.terms[m.focus].(modifiedKeyReceiver); ok && r.SendModifiedKey(k) {
			return m, nil
		}
	}
	if legacy, ok := legacyKey(k); ok {
		return m.update(legacy)
	}
	return m.updateInner(raw)
}
