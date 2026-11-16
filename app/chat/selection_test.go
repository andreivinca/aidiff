package chat

import (
	"slices"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPane_DragCopiesText(t *testing.T) {
	p, f := newTestPane(t, 80, 24, fixture())
	rows := strings.Split(view(p), "\n")
	y := -1
	for i, row := range rows {
		if i > 0 && i < 1+p.frame.layout.bodyH && strings.TrimSpace(ansi.Strip(ansi.Cut(row, 3, 80))) != "" {
			y = i
			break
		}
	}
	require.Positive(t, y)
	want := strings.TrimRight(ansi.Strip(ansi.Cut(rows[y], 3, 70)), " ")
	p.SendMouse(uv.MouseClickEvent{X: 3, Y: y, Button: uv.MouseLeft})
	p.SendMouse(uv.MouseMotionEvent{X: 70, Y: y, Button: uv.MouseLeft})
	assert.True(t, p.MouseCaptured())
	p.SendMouse(uv.MouseReleaseEvent{X: 70, Y: y, Button: uv.MouseLeft})
	assert.False(t, p.MouseCaptured())
	select {
	case got := <-f.clip.copied:
		assert.Equal(t, want, got)
	case <-time.After(waitLimit):
		t.Fatal("selection was not copied")
	}
	waitUpdate(t, p)
	assert.Contains(t, view(p), "selection copied")
	p.SendText("\x1b")
	locked(p, func() {
		assert.Nil(t, p.selection.rows)
		assert.Empty(t, p.stop.turn, "escape clears selection without stopping the agent")
	})
}

func TestSelectionReverseAndUnicode(t *testing.T) {
	s := textSelection{rows: []string{"   café 界!", "   next line"}, top: 0, bottom: 2,
		start: textPoint{7, 1}, end: textPoint{3, 0}}
	assert.Equal(t, "café 界!\nnext", s.text(20))
	s.start, s.end = s.end, s.start
	assert.Equal(t, "café 界!\nnext", s.text(20))
}

func TestPane_CtrlShiftCCopiesTheSelection(t *testing.T) {
	p, f := newTestPane(t, 80, 24, fixture())
	copied := func() string {
		t.Helper()
		select {
		case got := <-f.clip.copied:
			return got
		case <-time.After(waitLimit):
			t.Fatal("nothing was copied")
		}
		return ""
	}
	ctrlShiftC := uv.KeyPressEvent{Code: 'c', Mod: uv.ModCtrl | uv.ModShift}

	p.SendText("fix the login handler")
	p.SendText("\x1b[1;6D") // ctrl+shift+←: select "handler"
	assert.True(t, p.SendModifiedKey(ctrlShiftC))
	assert.Equal(t, "handler", copied(), "the input's selection is copied")
	waitUpdate(t, p)
	assert.Contains(t, view(p), "selection copied")
	assert.Equal(t, "fix the login handler", p.input.text(), "copying leaves the draft alone")

	p.SendText("\x15") // clear the draft
	rows := strings.Split(view(p), "\n")
	y := slices.IndexFunc(rows, func(r string) bool { return strings.Contains(r, "please fix the login page") })
	require.Positive(t, y)
	p.SendMouse(uv.MouseClickEvent{X: 3, Y: y, Button: uv.MouseLeft})
	p.SendMouse(uv.MouseReleaseEvent{X: 70, Y: y, Button: uv.MouseLeft})
	first := copied()
	waitUpdate(t, p)
	assert.True(t, p.SendModifiedKey(uv.KeyPressEvent{Code: 'C', Mod: uv.ModCtrl | uv.ModShift}), "the shifted letter is the same key")
	assert.Equal(t, first, copied(), "the transcript's selection is copied again")
	locked(p, func() {
		assert.NotNil(t, p.selection.rows, "and stays selected")
	})
	waitUpdate(t, p)

	p.SendText("\x1b")
	assert.True(t, p.SendModifiedKey(ctrlShiftC))
	assert.Contains(t, view(p), "nothing selected to copy")
}

func TestPane_CtrlCCopiesTheOneSelection(t *testing.T) {
	p, f := newTestPane(t, 80, 24, fixture())
	copied := func() string {
		t.Helper()
		select {
		case got := <-f.clip.copied:
			return got
		case <-time.After(waitLimit):
			t.Fatal("nothing was copied")
		}
		return ""
	}
	p.SendText("fix the login handler")
	p.SendText("\x1b[1;6D") // ctrl+shift+←: select "handler"
	p.SendText("\x03")
	assert.Equal(t, "handler", copied(), "ctrl+c copies the input's selection")
	waitUpdate(t, p)
	assert.Equal(t, "fix the login handler", p.input.text())

	rows := strings.Split(view(p), "\n")
	y := slices.IndexFunc(rows, func(r string) bool { return strings.Contains(r, "please fix the login page") })
	require.Positive(t, y)
	p.SendMouse(uv.MouseClickEvent{X: 3, Y: y, Button: uv.MouseLeft})
	locked(p, func() {
		assert.Empty(t, p.input.selected(), "a drag drops the input's selection: one selection at a time")
	})
	p.SendMouse(uv.MouseReleaseEvent{X: 70, Y: y, Button: uv.MouseLeft})
	first := copied()
	waitUpdate(t, p)
	p.SendText("\x03")
	assert.Equal(t, first, copied(), "ctrl+c copies the transcript's selection, which a key would otherwise drop")
	waitUpdate(t, p)
	locked(p, func() {
		assert.NotNil(t, p.selection.rows, "and it stays selected")
	})

	p.SendText("\x1b")
	p.SendText("\x03")
	assert.Contains(t, view(p), "nothing selected to copy")
	assert.Equal(t, "fix the login handler", p.input.text(), "ctrl+c never edits the draft")
}
