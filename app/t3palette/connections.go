package t3palette

import (
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/ui"
)

// connectionsHeader is how many rows the connections sidebar draws above
// its list: the title and a blank line.
const connectionsHeader = 2

// connectionsHelp is the key help drawn under the connections.
var connectionsHelp = []string{
	"",
	" enter   use connection",
	" alt+→   back",
}

// connectionsSidebar is the split's outer sidebar: this machine, then the T3
// servers the palette holds a connection to, then a row to connect to
// another. Using a server makes it the one the projects sidebar and the
// commands work with; using this machine shows the history of its folders.
// The T3 Code on this machine, reached through the connect row, is listed
// as a server of its own. It lives on the UI goroutine.
type connectionsSidebar struct {
	t   *Palette
	sel int
}

// connRow is a row of the connections sidebar.
type connRow struct {
	label   string
	detail  string
	session Session // the server's connection; nil for this machine and the connect row
	local   bool    // this machine
	connect bool    // the row that connects to another server
	live    bool    // the selected connection, which the projects sidebar and the commands work with
}

// localLabel is the server name of the T3 Code on this machine, and the
// name of this machine's row.
const localLabel = "local"

// localT3Row names the row of the T3 Code on this machine, apart from this
// machine's own.
const localT3Row = "local T3 Code"

// Connections is the connections sidebar, for the split.
func (t *Palette) Connections() splitview.Sidebar {
	return &connectionsSidebar{t: t}
}

// Available reports true: this machine is always listed.
func (b *connectionsSidebar) Available() bool {
	return true
}

// Shown selects the live server, else this machine.
func (b *connectionsSidebar) Shown() {
	b.sel = 0
	for i, r := range b.t.connRows() {
		if r.live {
			b.sel = i
		}
	}
}

// connRows lists this machine, the servers connected to (the T3 Code on
// this machine among them), by name, and the connect row.
func (t *Palette) connRows() []connRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	byLabel := map[string]Session{}
	for s := range t.held {
		if prev, ok := byLabel[s.Label()]; !ok || (prev != t.live && !t.connected[prev]) {
			byLabel[s.Label()] = s // the live or connected one stands for its server
		}
	}
	liveLabel := ""
	if t.live != nil {
		liveLabel = t.live.Label()
	}
	rows := []connRow{{label: localLabel, detail: t.cfg.LocalName, local: true, live: t.live == nil}} // no server selected: this machine is
	labels := make([]string, 0, len(byLabel))
	for l := range byLabel {
		labels = append(labels, l)
	}
	slices.Sort(labels)
	for _, l := range labels {
		s := byLabel[l]
		name := l
		if l == localLabel {
			name = localT3Row
		}
		rows = append(rows, connRow{label: name, detail: s.MachineName(), session: s, live: l == liveLabel})
	}
	return append(rows, connRow{label: "Connect to a server…", connect: true})
}

// View draws the title, the connections and the key help.
func (b *connectionsSidebar) View(width, height int, focused bool) string {
	if height < connectionsHeader+2 {
		return ""
	}
	rows := b.t.connRows()
	b.sel = min(b.sel, len(rows)-1)
	faint := lipgloss.NewStyle().Faint(true)
	lines := []string{lipgloss.NewStyle().Bold(true).Render(" Connections"), ""}
	for i, r := range rows {
		lines = append(lines, b.row(r, width, i == b.sel, focused))
	}
	if height >= len(lines)+len(connectionsHelp) {
		for _, h := range connectionsHelp {
			lines = append(lines, faint.Render(ansi.Truncate(h, width, "…")))
		}
	}
	return strings.Join(lines[:min(len(lines), height)], "\n")
}

// row draws one connection: ▸ marks the selected one, the detail is dim.
func (b *connectionsSidebar) row(r connRow, width int, selected, focused bool) string {
	mark := "  "
	if r.live {
		mark = "▸ "
	}
	style := lipgloss.NewStyle()
	switch {
	case selected && focused:
		style = style.Reverse(true)
	case selected:
		style = style.Underline(true)
	}
	if r.live {
		style = style.Bold(true)
	}
	label := ansi.Truncate(" "+mark+r.label, width, "…")
	rest := width - ansi.StringWidth(label)
	detail := ""
	if r.detail != "" && rest > 2 {
		detail = ansi.Truncate(" "+r.detail, rest, "…")
	}
	detail += strings.Repeat(" ", max(0, rest-ansi.StringWidth(detail)))
	// each part styled whole: a style nested in another would end it early
	return style.Render(label) + style.Faint(true).Render(detail)
}

// HandleKey moves the selection and uses the selected connection; esc hides
// the sidebar.
func (b *connectionsSidebar) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	rows := b.t.connRows()
	switch msg.String() {
	case "up", "k":
		b.sel = max(0, b.sel-1)
	case "down", "j":
		b.sel = min(len(rows)-1, b.sel+1)
	case "home", "g":
		b.sel = 0
	case "end", "G":
		b.sel = len(rows) - 1
	case "enter", " ":
		return b.use(rows, b.sel)
	case "esc":
		return nil, true
	}
	return nil, false
}

// Click uses the connection on row y.
func (b *connectionsSidebar) Click(y int) (tea.Cmd, bool) {
	rows := b.t.connRows()
	i := y - connectionsHeader
	if i < 0 || i >= len(rows) {
		return nil, false
	}
	b.sel = i
	return b.use(rows, i)
}

// Select does nothing: number keys belong to the projects.
func (b *connectionsSidebar) Select(int) (tea.Cmd, bool) {
	return nil, false
}

// use acts on rows[i]: its connection becomes the selected one, which the
// projects sidebar and the commands work with, and its projects show. This
// machine shows the history of its folders instead. The connect row asks
// which server.
func (b *connectionsSidebar) use(rows []connRow, i int) (tea.Cmd, bool) {
	r := rows[i]
	switch {
	case r.connect:
		return b.t.connectPicker(nil), true
	case r.session != nil:
		if !b.t.useLive(r.session) {
			return flash("T3: " + r.label + ": the connection was closed meanwhile"), true
		}
		return showConnection(r.session.MachineName(), true), true
	}
	b.t.useLocal()
	return showConnection(b.t.cfg.LocalName, false), true
}

// showConnection names the machine of a connection in the status bar and
// shows its sidebar: a T3 server's projects, this machine's history.
func showConnection(machine string, t3 bool) tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return ui.MachineIdentityMsg{Name: machine, T3: t3} },
		func() tea.Msg { return splitview.ShowSidebarMsg{} },
	)
}

// useLocal selects this machine: there is no live server, so the sidebar
// shows the history of its folders. The connections stay open.
func (t *Palette) useLocal() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.live, t.project = nil, nil
	t.shell, t.shellAt = t3client.Shell{}, time.Time{}
	t.closeLaterLocked(t.takeUnusedLocked())
}

// useLive makes s, a session the palette holds, the one the commands and the
// projects sidebar work with. A session nothing uses any more is closed. It
// reports false when s is no longer held.
func (t *Palette) useLive(s Session) bool {
	t.mu.Lock()
	if t.closed || !t.held[s] {
		t.mu.Unlock()
		return false
	}
	if t.live != s {
		if t.live == nil || t.live.Label() != s.Label() {
			t.project = nil // a project belongs to its server
		}
		t.live = s
		t.shell, t.shellAt = t3client.Shell{}, time.Time{} // the other server's listing
		t.closeLaterLocked(t.takeUnusedLocked())
	}
	t.mu.Unlock()
	t.refreshShell()
	return true
}
