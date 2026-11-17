package splitview

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
)

func TestParseModifiedKey(t *testing.T) {
	tests := []struct {
		seq  string
		want uv.KeyPressEvent
		ok   bool
	}{
		{seq: "\x1b[27;2;13~", want: uv.KeyPressEvent{Code: uv.KeyEnter, Mod: uv.ModShift}, ok: true},
		{seq: "\x1b[13;2u", want: uv.KeyPressEvent{Code: uv.KeyEnter, Mod: uv.ModShift}, ok: true},
		{seq: "\x1b[27;5;13~", want: uv.KeyPressEvent{Code: uv.KeyEnter, Mod: uv.ModCtrl}, ok: true},
		{seq: "\x1b[27;6;65~", want: uv.KeyPressEvent{Code: 'A', Mod: uv.ModCtrl | uv.ModShift}, ok: true},
		{seq: "\x1b[9;4u", want: uv.KeyPressEvent{Code: uv.KeyTab, Mod: uv.ModShift | uv.ModAlt}, ok: true},
		{seq: "\x1b[13;0u"},
		{seq: "\x1b[5;9z"},
		{seq: "\x1b[27;2;13"},
		{seq: "\x1b[27;2;99999999~"},
	}
	for _, tt := range tests {
		got, ok := parseModifiedKey([]byte(tt.seq))
		assert.Equal(t, tt.ok, ok, "%q", tt.seq)
		assert.Equal(t, tt.want, got, "%q", tt.seq)
	}
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
