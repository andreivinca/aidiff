package splitview

import (
	"reflect"

	tea "github.com/charmbracelet/bubbletea"
)

// Bubble Tea v1 offers no public way to receive an escape sequence it cannot
// name, such as the CSI u form of shift+enter ("\x1b[13;2u") that terminals
// send with the kitty keyboard protocol: it delivers such a sequence as its
// unexported unknownCSISequenceMsg, a []byte. This file is the one place that
// knows that type, by package path and name, so the focused pane still gets
// the sequence. TestRawSequence_BubbleTeaType feeds the bytes through a real
// Bubble Tea program and fails when an upgrade changes the type.
const (
	teaPkgPath           = "github.com/charmbracelet/bubbletea"
	unknownCSISequenceTy = "unknownCSISequenceMsg"
)

// rawSequence returns the bytes of a sequence Bubble Tea could not name.
func rawSequence(msg tea.Msg) ([]byte, bool) {
	t := reflect.TypeOf(msg)
	if t == nil || t.PkgPath() != teaPkgPath || t.Name() != unknownCSISequenceTy {
		return nil, false
	}
	if t.Kind() != reflect.Slice || t.Elem().Kind() != reflect.Uint8 {
		return nil, false
	}
	return reflect.ValueOf(msg).Bytes(), true
}

// forwardRaw hands an unnamed escape sequence to the focused pane, or to the
// wrapped model when it has focus or a popup is open.
func (m Model) forwardRaw(seq []byte, msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.focus < 0 || m.palette.open || m.quitDialog {
		return m.updateInner(msg)
	}
	m.terms[m.focus].SendText(string(seq))
	return m, nil
}
