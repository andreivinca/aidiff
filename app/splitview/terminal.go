package splitview

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// Terminal is one pane in the right column: a shell, or anything else that
// draws a screen and takes keys, such as a chat.
//
// The split relies on this contract:
//   - Updates delivers a value whenever the pane wants a redraw, and is closed
//     once the pane is closed, so the split's wait on it ends with the pane.
//   - Exited reports that the pane is done (its program ended, or it closed
//     itself); the split then removes it and calls Close.
//   - Close releases the pane and may take a while (a shell gives its
//     processes a grace period), so the split calls it off the UI goroutine.
//     It must be safe to call more than once.
//   - Render returns the pane's screen at the size of the last Resize.
//
// A pane may also implement MouseCaptured() bool (see mouseCapturer) and
// QuitNote() string (see quitNoter).
type Terminal interface {
	Updates() <-chan struct{}
	Exited() bool
	SendText(s string)
	SendKey(k uv.KeyPressEvent)
	Paste(s string)
	SendMouse(m vt.Mouse)
	Resize(width, height int)
	Render(showCursor bool) string
	Close()
}

// StartFunc launches a pane whose content is width by height cells.
type StartFunc func(width, height int) (Terminal, error)

// mouseCapturer is a pane that holds on to a drag it started (a text
// selection): while MouseCaptured reports true, motion and release events go
// to it wherever the pointer is.
type mouseCapturer interface {
	MouseCaptured() bool
}

// quitNoter is a pane that tells the quit dialog what closing it means, in
// place of the shell's "everything running in it" warning.
type quitNoter interface {
	QuitNote() string
}

// refreshGate is a wrapped model that can be busy: while CanRefresh reports
// false, Config.RefreshMsg is held back.
type refreshGate interface {
	CanRefresh() bool
}
