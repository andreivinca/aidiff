package splitview

import (
	"bytes"
	"strconv"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
)

func TestParseKeyReport(t *testing.T) {
	press := func(code rune, mod uv.KeyMod) keyReport {
		return keyReport{key: uv.KeyPressEvent{Code: code, Mod: mod}, event: keyPress}
	}
	tests := []struct {
		seq  string
		want keyReport
		ok   bool
	}{
		// xterm's modifyOtherKeys and the plain CSI u form
		{seq: "\x1b[27;2;13~", want: press(uv.KeyEnter, uv.ModShift), ok: true},
		{seq: "\x1b[13;2u", want: press(uv.KeyEnter, uv.ModShift), ok: true},
		{seq: "\x1b[27;5;13~", want: press(uv.KeyEnter, uv.ModCtrl), ok: true},
		{seq: "\x1b[27;6;65~", want: press('A', uv.ModCtrl|uv.ModShift), ok: true},
		{seq: "\x1b[9;4u", want: press(uv.KeyTab, uv.ModShift|uv.ModAlt), ok: true},
		{seq: "\x1b[13;0u"},
		{seq: "\x1b[5;9z"},
		{seq: "\x1b[27;2;13"},
		{seq: "\x1b[27;2;99999999~"},
		// the kitty protocol: a key alone, events, locks, the modifier keys
		{seq: "\x1b[106u", want: press('j', 0), ok: true},
		{seq: "\x1b[9;5u", want: press(uv.KeyTab, uv.ModCtrl), ok: true},
		{seq: "\x1b[9;5:2u", want: keyReport{key: uv.KeyPressEvent{Code: uv.KeyTab, Mod: uv.ModCtrl}, event: keyRepeat}, ok: true},
		{seq: "\x1b[9;5:3u", want: keyReport{key: uv.KeyPressEvent{Code: uv.KeyTab, Mod: uv.ModCtrl}, event: keyRelease}, ok: true},
		{seq: "\x1b[9;69u", want: press(uv.KeyTab, uv.ModCtrl), ok: true},  // caps lock on
		{seq: "\x1b[9;197u", want: press(uv.KeyTab, uv.ModCtrl), ok: true}, // and num lock
		{seq: "\x1b[57442;1:3u", want: keyReport{key: uv.KeyPressEvent{Code: kittyLeftCtrl}, event: keyRelease}, ok: true},
		{seq: "\x1b[57448;5u", want: press(kittyRightCtrl, uv.ModCtrl), ok: true},
		{seq: "\x1b[115;4u", want: press('s', uv.ModAlt|uv.ModShift), ok: true},
		{seq: "\x1b[106:74;2u", want: press('j', uv.ModShift), ok: true},    // with its shifted key
		{seq: "\x1b[97;5;97u", want: press('a', uv.ModCtrl), ok: true},      // with its text
		{seq: "\x1b[57352;5u", want: press(uv.KeyUp, uv.ModCtrl), ok: true}, // a functional key's own code
		{seq: "\x1b[2;5u", want: press('b', uv.ModCtrl), ok: true},          // ctrl+b sent as its C0 code
		{seq: "\x1b[27u", want: press(uv.KeyEscape, 0), ok: true},           // escape stays escape
		{seq: "\x1b[1;5:3A", want: keyReport{key: uv.KeyPressEvent{Code: uv.KeyUp, Mod: uv.ModCtrl}, event: keyRelease}, ok: true},
		{seq: "\x1b[1;1:2B", want: keyReport{key: uv.KeyPressEvent{Code: uv.KeyDown}, event: keyRepeat}, ok: true},
		{seq: "\x1b[5;5:3~", want: keyReport{key: uv.KeyPressEvent{Code: uv.KeyPgUp, Mod: uv.ModCtrl}, event: keyRelease}, ok: true},
		{seq: "\x1b[3;5~"},   // ctrl+delete, legacy: left for a pane as it came
		{seq: "\x1b[1;5A"},   // Bubble Tea names it
		{seq: "\x1b[9;5:4u"}, // no such event
		{seq: "\x1b[?11u"},   // the flags reply, never a key
		{seq: "\x1b[>11u"},
	}
	for _, tt := range tests {
		got, ok := parseKeyReport([]byte(tt.seq))
		assert.Equal(t, tt.ok, ok, "%q", tt.seq)
		assert.Equal(t, tt.want, got, "%q", tt.seq)
	}
}

func TestKeyFlagsReply(t *testing.T) {
	flags, ok := keyFlagsReply([]byte("\x1b[?11u"))
	assert.True(t, ok)
	assert.Equal(t, 11, flags)
	flags, ok = keyFlagsReply([]byte("\x1b[?0u"))
	assert.True(t, ok)
	assert.Zero(t, flags)
	for _, seq := range []string{"\x1b[11u", "\x1b[?u", "\x1b[?1;2u", "\x1b[?11c", "\x1b[>11u"} {
		_, ok := keyFlagsReply([]byte(seq))
		assert.False(t, ok, "%q", seq)
	}
	assert.Equal(t, "\x1b[>"+strconv.Itoa(keyReportFlags)+"u", pushKeyReports, "the flags pushed are the flags named")
}

func TestLegacyKey(t *testing.T) {
	tests := []struct {
		key  uv.KeyPressEvent
		want tea.KeyMsg
		ok   bool
	}{
		{key: uv.KeyPressEvent{Code: uv.KeyEnter, Mod: uv.ModShift}, want: tea.KeyMsg{Type: tea.KeyEnter}, ok: true},
		{key: uv.KeyPressEvent{Code: uv.KeyEnter, Mod: uv.ModAlt}, want: tea.KeyMsg{Type: tea.KeyEnter, Alt: true}, ok: true},
		{key: uv.KeyPressEvent{Code: uv.KeyTab, Mod: uv.ModShift}, want: tea.KeyMsg{Type: tea.KeyShiftTab}, ok: true},
		{key: uv.KeyPressEvent{Code: uv.KeyTab, Mod: uv.ModCtrl}, want: tea.KeyMsg{Type: tea.KeyTab}, ok: true},
		{key: uv.KeyPressEvent{Code: uv.KeyBackspace, Mod: uv.ModCtrl}, want: tea.KeyMsg{Type: tea.KeyBackspace}, ok: true},
		{key: uv.KeyPressEvent{Code: uv.KeySpace, Mod: uv.ModShift}, want: tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, ok: true},
		{key: uv.KeyPressEvent{Code: uv.KeySpace, Mod: uv.ModCtrl}, want: tea.KeyMsg{Type: tea.KeyCtrlAt}, ok: true},
		{key: uv.KeyPressEvent{Code: 'A', Mod: uv.ModCtrl | uv.ModShift}, want: tea.KeyMsg{Type: tea.KeyCtrlA}, ok: true},
		{key: uv.KeyPressEvent{Code: 'a', Mod: uv.ModShift}, want: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}}, ok: true},
		{key: uv.KeyPressEvent{Code: '1', Mod: uv.ModCtrl}},
		{key: uv.KeyPressEvent{Code: uv.KeyDown}, want: tea.KeyMsg{Type: tea.KeyDown}, ok: true},
		{key: uv.KeyPressEvent{Code: uv.KeyUp, Mod: uv.ModCtrl}, want: tea.KeyMsg{Type: tea.KeyCtrlUp}, ok: true},
		{key: uv.KeyPressEvent{Code: uv.KeyEnd, Mod: uv.ModCtrl | uv.ModShift | uv.ModAlt}, want: tea.KeyMsg{Type: tea.KeyCtrlShiftEnd, Alt: true}, ok: true},
		{key: uv.KeyPressEvent{Code: kittyLeftCtrl}},
	}
	for _, tt := range tests {
		got, ok := legacyKey(tt.key)
		assert.Equal(t, tt.ok, ok, "%+v", tt.key)
		assert.Equal(t, tt.want, got, "%+v", tt.key)
	}
}

func TestReportModifiedKeys(t *testing.T) {
	var out bytes.Buffer
	restore := ReportModifiedKeys(&out)
	assert.Equal(t, "\x1b[>4;1m", out.String(), "modifyOtherKeys level 1 is requested")
	restore()
	assert.Equal(t, "\x1b[>4;1m\x1b[>4m", out.String(), "and reset to the terminal's default")
}
