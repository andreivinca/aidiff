package splitview

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// xtermMods is the modifier parameter xterm uses in CSI 1;<mod> sequences.
var xtermMods = map[tea.KeyType]struct {
	final byte
	mod   int
}{
	tea.KeyShiftUp: {'A', 2}, tea.KeyShiftDown: {'B', 2}, tea.KeyShiftRight: {'C', 2}, tea.KeyShiftLeft: {'D', 2},
	tea.KeyCtrlUp: {'A', 5}, tea.KeyCtrlDown: {'B', 5}, tea.KeyCtrlRight: {'C', 5}, tea.KeyCtrlLeft: {'D', 5},
	tea.KeyCtrlShiftUp: {'A', 6}, tea.KeyCtrlShiftDown: {'B', 6}, tea.KeyCtrlShiftRight: {'C', 6}, tea.KeyCtrlShiftLeft: {'D', 6},
	tea.KeyShiftHome: {'H', 2}, tea.KeyShiftEnd: {'F', 2}, tea.KeyCtrlHome: {'H', 5}, tea.KeyCtrlEnd: {'F', 5},
	tea.KeyCtrlShiftHome: {'H', 6}, tea.KeyCtrlShiftEnd: {'F', 6},
}

// plainSpecial maps unmodified special keys to the emulator's own encoder,
// which honors application cursor-key mode (vim, less, Claude Code...).
var plainSpecial = map[tea.KeyType]rune{
	tea.KeyUp: uv.KeyUp, tea.KeyDown: uv.KeyDown, tea.KeyRight: uv.KeyRight, tea.KeyLeft: uv.KeyLeft,
	tea.KeyHome: uv.KeyHome, tea.KeyEnd: uv.KeyEnd, tea.KeyPgUp: uv.KeyPgUp, tea.KeyPgDown: uv.KeyPgDown,
	tea.KeyDelete: uv.KeyDelete, tea.KeyInsert: uv.KeyInsert,
	tea.KeyF1: uv.KeyF1, tea.KeyF2: uv.KeyF2, tea.KeyF3: uv.KeyF3, tea.KeyF4: uv.KeyF4,
	tea.KeyF5: uv.KeyF5, tea.KeyF6: uv.KeyF6, tea.KeyF7: uv.KeyF7, tea.KeyF8: uv.KeyF8,
	tea.KeyF9: uv.KeyF9, tea.KeyF10: uv.KeyF10, tea.KeyF11: uv.KeyF11, tea.KeyF12: uv.KeyF12,
}

// sendKey translates a Bubble Tea key back into the bytes a terminal sends.
func sendKey(t Terminal, msg tea.KeyMsg) {
	if msg.Paste {
		t.Paste(string(msg.Runes))
		return
	}
	alt := ""
	if msg.Alt {
		alt = "\x1b"
	}
	switch {
	case msg.Type == tea.KeyRunes:
		t.SendText(alt + string(msg.Runes))
	case msg.Type == tea.KeySpace:
		t.SendText(alt + " ")
	case msg.Type == tea.KeyShiftTab:
		t.SendText(alt + "\x1b[Z")
	case msg.Type >= 0 && msg.Type < 32, msg.Type == tea.KeyBackspace:
		// C0 controls (ctrl+letter, enter, tab, esc) and DEL: the key type
		// value is the byte itself.
		t.SendText(alt + string(rune(msg.Type))) //nolint:gosec // 0 <= msg.Type < 32 or KeyBackspace: a byte
	default:
		if x, ok := xtermMods[msg.Type]; ok {
			mod := x.mod
			if msg.Alt {
				mod += 2 // xterm modifier bits: shift=1, alt=2, ctrl=4 (param = 1 + bits)
			}
			t.SendText(fmt.Sprintf("\x1b[1;%d%c", mod, x.final))
			return
		}
		if code, ok := plainSpecial[msg.Type]; ok {
			k := uv.KeyPressEvent{Code: code}
			if msg.Alt {
				k.Mod = uv.ModAlt
			}
			t.SendKey(k)
		}
	}
}

// toTermMouse converts a Bubble Tea mouse event into the emulator's type. The
// emulator only forwards it if the child turned on mouse reporting.
func toTermMouse(msg tea.MouseMsg, x, y int) (vt.Mouse, bool) {
	if x < 0 || y < 0 {
		return nil, false
	}
	var btn uv.MouseButton
	switch msg.Button {
	case tea.MouseButtonLeft:
		btn = uv.MouseLeft
	case tea.MouseButtonMiddle:
		btn = uv.MouseMiddle
	case tea.MouseButtonRight:
		btn = uv.MouseRight
	case tea.MouseButtonWheelUp:
		btn = uv.MouseWheelUp
	case tea.MouseButtonWheelDown:
		btn = uv.MouseWheelDown
	case tea.MouseButtonNone:
		btn = uv.MouseNone
	default:
		return nil, false
	}
	var mod uv.KeyMod
	if msg.Shift {
		mod |= uv.ModShift
	}
	if msg.Alt {
		mod |= uv.ModAlt
	}
	if msg.Ctrl {
		mod |= uv.ModCtrl
	}
	m := uv.Mouse{X: x, Y: y, Button: btn, Mod: mod}
	switch {
	case btn == uv.MouseWheelUp || btn == uv.MouseWheelDown:
		return uv.MouseWheelEvent(m), true
	case msg.Action == tea.MouseActionPress:
		return uv.MouseClickEvent(m), true
	case msg.Action == tea.MouseActionRelease:
		return uv.MouseReleaseEvent(m), true
	default:
		return uv.MouseMotionEvent(m), true
	}
}
