package t3palette

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/ui"
)

func TestConnections_LocalThenServers(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true, LocalName: "andrei-pc"}, nil)
	sb := h.p.Connections()
	require.True(t, sb.Available(), "this machine is always there")
	view := ansi.Strip(sb.View(30, 12, true))
	assert.Contains(t, view, " ▸ local andrei-pc", "only this machine at first, and it is the selected one")
	assert.Contains(t, view, "Connect to a server…")
	assert.NotContains(t, view, "ssh://")

	a := newFakeSession("ssh://a", testShell())
	h.connect(t, "ssh://a", a)
	view = ansi.Strip(sb.View(30, 12, true))
	assert.Contains(t, view, " ▸ ssh://a", "a connected server is listed, and marked once selected")
	assert.Contains(t, view, "   local andrei-pc", "this machine is no longer the selected one")
	assert.NotContains(t, view, "active", "the mark says it, no word")
	assert.Contains(t, view, "enter   use connection")
}

func TestConnections_UseAnotherServer(t *testing.T) {
	a := newFakeSession("ssh://a", twoProjectShell())
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	b := newFakeSession("ssh://b", testShell())
	h.connect(t, "ssh://b", b)
	require.Same(t, b, h.p.liveSession())

	sb := h.p.Connections()
	sb.Shown()
	view := ansi.Strip(sb.View(40, 12, true))
	assert.Contains(t, view, " ▸ ssh://b", "the arrow marks the selected server, b, not where the review comes from")
	assert.Contains(t, view, "   ssh://a")

	sb.HandleKey(tea.KeyMsg{Type: tea.KeyUp}) // from b, the live one, up to a
	cmd, hide := sb.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, hide)
	msgs := run(cmd)
	find[splitview.ShowSidebarMsg](t, msgs) // its projects show
	assert.Equal(t, "ssh://a", find[ui.MachineIdentityMsg](t, msgs).Name, "the status bar names its machine")
	assert.Same(t, a, h.p.liveSession(), "a is the server in use")
	h.settle()
	assert.Zero(t, b.closes(), "b stays connected, to go back to")

	cmd, _ = sb.Click(connectionsHeader + 3) // the connect row, after local, a and b
	assert.Equal(t, "T3 Code: connect to", find[splitview.ShowPickerMsg](t, run(cmd)).Title)
}

func TestConnections_LocalShowsTheHistory(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, Config{NoWatch: true, NoChat: true, LocalName: "andrei-pc",
		Local: LocalConfig{History: func() []string { return []string{dir} }}}, nil)
	b := newFakeSession("ssh://b", twoProjectShell())
	h.connect(t, "ssh://b", b)
	panel, conns := h.p.Sidebar(), h.p.Connections()
	assert.Contains(t, ansi.Strip(panel.View(30, 14, false)), "Alpha", "b's projects")

	cmd, hide := conns.Click(connectionsHeader) // this machine
	assert.True(t, hide)
	msgs := run(cmd)
	find[splitview.ShowSidebarMsg](t, msgs) // its history shows
	assert.Equal(t, ui.MachineIdentityMsg{Name: "andrei-pc"}, find[ui.MachineIdentityMsg](t, msgs))
	assert.Nil(t, h.p.liveSession(), "no server is in use")
	require.True(t, panel.Available(), "the panel lists this machine's folders")
	view := ansi.Strip(panel.View(40, 14, false))
	assert.Contains(t, view, " History")
	assert.Contains(t, view, filepath.Base(dir))
	assert.NotContains(t, view, "Alpha")
	view = ansi.Strip(conns.View(40, 12, false))
	assert.Contains(t, view, " ▸ local andrei-pc", "the arrow follows the selection")
	assert.Contains(t, view, "   ssh://b")
	h.settle()
	assert.Zero(t, b.closes(), "b stays connected")

	cmd, _ = conns.Click(connectionsHeader + 1) // b again
	run(cmd)
	assert.Same(t, b, h.p.liveSession())
	_, err := h.p.fetchShell(t.Context(), b)
	require.NoError(t, err)
	assert.Contains(t, ansi.Strip(panel.View(30, 14, false)), "Alpha", "b's projects are back")
}

func TestConnections_LocalT3CodeIsAServerOfItsOwn(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true, LocalName: "andrei-pc"}, nil)
	h.connect(t, "local", newFakeSession("local", testShell()))
	rows := h.p.connRows()
	require.Len(t, rows, 3, "local, local T3 Code, connect")
	assert.True(t, rows[0].local)
	assert.False(t, rows[0].live)
	assert.Equal(t, localT3Row, rows[1].label)
	assert.True(t, rows[1].live)
	assert.Contains(t, ansi.Strip(h.p.Sidebar().View(30, 14, false)), "Sabra2", "its projects show")
}

func TestConnections_KeyAfterTheListShrank(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	sb, ok := h.p.Connections().(*connectionsSidebar)
	require.True(t, ok)
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'x'}}, {Type: tea.KeyEnter}, {Type: tea.KeyDown}} {
		sb.sel = 7 // a row of a longer list, drawn before a connection went away
		assert.NotPanics(t, func() { sb.HandleKey(key) }, key.String())
		assert.Equal(t, len(h.p.connRows())-1, sb.sel, "the selection is back on the list")
	}
}
