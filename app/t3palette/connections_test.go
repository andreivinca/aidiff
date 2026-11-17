package t3palette

import (
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
	assert.Contains(t, view, " ▸ local andrei-pc", "only this machine at first, where the review is")
	assert.Contains(t, view, "Connect to a server…")
	assert.NotContains(t, view, "ssh://")

	a := newFakeSession("ssh://a", testShell())
	h.connect(t, "ssh://a", a)
	view = ansi.Strip(sb.View(30, 12, true))
	assert.Contains(t, view, "   ssh://a", "a connected server is listed")
	assert.Contains(t, view, "active", "and marked as the one in use")
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
	assert.Contains(t, view, " ▸ ssh://a", "the review on screen comes from a")

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

func TestConnections_LocalDrivesTheProjects(t *testing.T) {
	h := newHarness(t, Config{NoWatch: true, NoChat: true, LocalName: "andrei-pc"}, nil)
	h.p.localServer = func() bool { return false }
	b := newFakeSession("ssh://b", twoProjectShell())
	h.connect(t, "ssh://b", b)
	projects, conns := h.p.Sidebar(), h.p.Connections()
	assert.Contains(t, ansi.Strip(projects.View(30, 14, false)), "Alpha", "b's projects")

	cmd, hide := conns.Click(connectionsHeader) // this machine
	assert.True(t, hide)
	msgs := run(cmd)
	find[splitview.ShowSidebarMsg](t, msgs)
	assert.Equal(t, ui.MachineIdentityMsg{Name: "andrei-pc"}, find[ui.MachineIdentityMsg](t, msgs))
	assert.Nil(t, h.p.liveSession(), "with no T3 Code here, no server is in use")
	assert.True(t, projects.Available(), "the projects panel still shows")
	view := ansi.Strip(projects.View(30, 14, false))
	assert.Contains(t, view, "local · andrei-pc")
	assert.Contains(t, view, "no T3 Code running")
	assert.NotContains(t, view, "Alpha", "and none of b's projects")
	assert.Contains(t, ansi.Strip(conns.View(40, 12, false)), "andrei-pc · active")
	h.settle()
	assert.Zero(t, b.closes(), "b stays connected")

	cmd, _ = conns.Click(connectionsHeader + 1) // b again
	run(cmd)
	assert.Same(t, b, h.p.liveSession())
	_, err := h.p.fetchShell(t.Context(), b)
	require.NoError(t, err)
	assert.Contains(t, ansi.Strip(projects.View(30, 14, false)), "Alpha", "b's projects are back")

	h.p.localServer = func() bool { return true }
	h.dials["local"] = newFakeSession("local", testShell())
	cmd, _ = conns.Click(connectionsHeader)
	run(cmd)
	assert.Equal(t, "local", h.p.liveSession().Label(), "T3 Code on this machine is connected to")
	assert.Contains(t, ansi.Strip(projects.View(30, 14, false)), "Sabra2", "and its projects show")
	rows := h.p.connRows()
	assert.Len(t, rows, 3, "local stands for its T3 Code: local, b, connect")
	assert.True(t, rows[0].live)
}
