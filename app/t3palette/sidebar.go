package t3palette

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// sidebarHeader is how many rows the projects sidebar draws above its list:
// the title, the server, a blank line.
const sidebarHeader = 3

// projectsSidebar is the split's sidebar listing the live server's projects,
// numbered; picking one puts it on screen (openProject). It lives on the UI
// goroutine: the split calls it there only.
type projectsSidebar struct {
	t      *Palette
	sel    int // the selected row
	offset int // the first row shown, when they do not all fit
}

// Sidebar is the split's sidebar: the projects of the live server, else,
// while this machine is the selected connection, the history of its
// folders.
func (t *Palette) Sidebar() splitview.Sidebar {
	return &panelSidebar{projects: &projectsSidebar{t: t}, history: &historySidebar{t: t}}
}

// panelSidebar shows the projects sidebar while a server is live, the
// history sidebar otherwise.
type panelSidebar struct {
	projects *projectsSidebar
	history  *historySidebar
}

// current is the sidebar the selected connection shows.
func (p *panelSidebar) current() splitview.Sidebar {
	if p.projects.Available() {
		return p.projects
	}
	return p.history
}

func (p *panelSidebar) Available() bool { return p.current().Available() }
func (p *panelSidebar) Shown()          { p.current().Shown() }
func (p *panelSidebar) View(width, height int, focused bool) string {
	return p.current().View(width, height, focused)
}
func (p *panelSidebar) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) { return p.current().HandleKey(msg) }
func (p *panelSidebar) Click(y int) (tea.Cmd, bool)              { return p.current().Click(y) }

// Available reports whether a server is connected.
func (b *projectsSidebar) Available() bool {
	b.t.mu.Lock()
	defer b.t.mu.Unlock()
	return b.t.live != nil || b.t.dialed.spec != "" // a T3 server's projects only, or the one being connected to
}

// Shown selects the project on screen and refreshes the list when it is
// old.
func (b *projectsSidebar) Shown() {
	b.t.refreshShell()
	v := b.t.state()
	current := v.currentProjectID()
	for i, p := range projects(v.shell) {
		if p.ID == current {
			b.sel = i
			return
		}
	}
	b.sel = 0
}

// View draws the title, the server and the numbered projects: the one on
// screen marked, the selected one highlighted, and a dot for those whose
// agent is running.
func (b *projectsSidebar) View(width, height int, focused bool) string {
	if height < sidebarHeader+2 {
		return ""
	}
	b.t.refreshShell() // a list older than shellMaxAge is fetched again
	v := b.t.state()
	list := projects(v.shell)
	total := len(list)
	if v.live != nil {
		total++ // the Add project… row under the projects
	}
	b.sel = min(b.sel, max(0, total-1))

	faint := lipgloss.NewStyle().Faint(true)
	if v.dial.spec != "" {
		return dialView(v.dial, width, height)
	}
	lines := []string{paneTitle("Projects", focused), faint.Render(" " + serverLabel(v.live)), ""}
	switch {
	case v.live == nil:
		lines = append(lines, faint.Render(" not connected"))
	case len(list) == 0 && v.shell.Projects == nil && v.shellErr != nil: // the fetch failed: say why, not "loading…"
		why := strings.Split(ansi.Wrap(v.shellErr.Error(), max(1, width-1), " "), "\n")
		for _, l := range why[:min(len(why), 3)] {
			lines = append(lines, faint.Render(" "+l))
		}
	case len(list) == 0 && v.shell.Projects == nil && v.retrying:
		lines = append(lines, faint.Render(" reconnecting…"))
	case len(list) == 0 && v.shell.Projects == nil:
		lines = append(lines, faint.Render(" loading…"))
	case len(list) == 0:
		lines = append(lines, faint.Render(" no projects"))
	}

	help := sidebarHelp
	if height < sidebarHeader+len(help)+3 {
		help = nil // too short: the projects come first
	}
	rows := max(1, height-sidebarHeader-len(help))
	b.offset = min(max(b.offset, b.sel-rows+1), b.sel)
	b.offset = max(0, min(b.offset, total-rows))
	current := v.currentProjectID()
	for i := b.offset; i < min(total, b.offset+rows); i++ {
		if i == len(list) {
			lines = append(lines, addProjectRow(width, i == b.sel, focused))
			continue
		}
		lines = append(lines, b.row(i, list[i], v.shell, width, i == b.sel, focused, list[i].ID == current))
	}
	for _, h := range help {
		lines = append(lines, hintRow(h, width))
	}
	return strings.Join(lines[:min(len(lines), height)], "\n")
}

// sidebarHelp is the key help drawn under the projects, after a blank row.
var sidebarHelp = []string{
	"",
	" enter, 1…9   open project",
	" a            add project",
	" ctrl+b       show / hide",
	" alt+1…9      switch tab",
	" alt+s        switch session",
	" alt+← alt+→  move focus",
	" alt+h alt+l  move focus",
}

// row draws project p, the list's row i.
func (b *projectsSidebar) row(i int, p t3client.Project, sh t3client.Shell, width int, selected, focused, current bool) string {
	num := " "
	if i < 9 {
		num = fmt.Sprint(i + 1)
	}
	mark := "  "
	if current {
		mark = "▸ "
	}
	tail := ""
	if projectRunning(sh, p) {
		tail = " ●"
	}
	title := ansi.Truncate(p.Title, max(1, width-len(num)-len(mark)-ansi.StringWidth(tail)-2), "…")
	text := " " + num + " " + mark + title
	text += strings.Repeat(" ", max(0, width-ansi.StringWidth(text)-ansi.StringWidth(tail))) + tail
	style := rowStyle(selected, focused)
	if current {
		style = style.Bold(true)
	}
	return edged(style, text, selected, focused)
}

// canAdd reports whether the panel has its Add project… row: a server is
// connected, and not being replaced by another one being dialed.
func (b *projectsSidebar) canAdd() bool {
	v := b.t.state()
	return v.live != nil && v.dial.spec == ""
}

// addProjectRow is the row under a panel's list that adds a project, as a
// does: aligned with the names above it, selectable like them.
func addProjectRow(width int, selected, focused bool) string {
	style := rowStyle(selected, focused)
	if !selected {
		style = detailStyle(style)
	}
	text := ansi.Truncate("     + Add project…", width, "…")
	return edged(style, text+strings.Repeat(" ", max(0, width-ansi.StringWidth(text))), selected, focused)
}

// list is the projects the sidebar lists: none while a server is being
// connected to, whose listing is not there yet; the one cached is the
// previous server's, and opening from it would open the wrong one.
func (b *projectsSidebar) list() []t3client.Project {
	v := b.t.state()
	if v.dial.spec != "" {
		return nil
	}
	return projects(v.shell)
}

// HandleKey moves the selection and opens the selected project, or adds
// one on the Add project… row under them; a digit opens its project, esc
// hides the sidebar.
func (b *projectsSidebar) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	list := b.list()
	last := len(list) - 1
	if b.canAdd() {
		last++ // the Add project… row
	}
	switch msg.String() {
	case "up", "k":
		b.sel = max(0, b.sel-1)
	case "down", "j":
		b.sel = max(0, min(last, b.sel+1))
	case "home", "g":
		b.sel = 0
	case "end", "G":
		b.sel = max(0, last)
	case "enter", " ":
		if b.sel == len(list) && b.canAdd() {
			return b.t.addT3Project(nil), true
		}
		return b.open(list, b.sel)
	case "a":
		return b.t.addT3Project(nil), true
	case "esc":
		return nil, true
	default:
		if r := msg.Runes; msg.Type == tea.KeyRunes && len(r) == 1 && r[0] >= '1' && r[0] <= '9' {
			return b.Select(int(r[0] - '0'))
		}
	}
	return nil, false
}

// Click opens the project on row y.
func (b *projectsSidebar) Click(y int) (tea.Cmd, bool) {
	i := b.offset + y - sidebarHeader
	list := b.list()
	if y >= sidebarHeader && i == len(list) && b.canAdd() {
		b.sel = i
		return b.t.addT3Project(nil), true
	}
	if y < sidebarHeader || i >= len(list) {
		return nil, false
	}
	b.sel = i
	return b.open(list, i)
}

// Select opens project n, counted from 1: its digit, pressed in the
// panel.
func (b *projectsSidebar) Select(n int) (tea.Cmd, bool) {
	list := b.list()
	if n < 1 || n > len(list) {
		return flash(fmt.Sprintf("T3: there is no project %d", n)), false
	}
	b.sel = n - 1
	return b.open(list, n-1)
}

// open puts list[i] on screen and hides the sidebar.
func (b *projectsSidebar) open(list []t3client.Project, i int) (tea.Cmd, bool) {
	if i < 0 || i >= len(list) {
		return nil, false
	}
	return b.t.openProject(list[i]), true
}

// projectRunning reports whether an agent runs in one of p's threads.
func projectRunning(sh t3client.Shell, p t3client.Project) bool {
	for _, th := range projectThreads(sh, &p) {
		if th.Running() {
			return true
		}
	}
	return false
}

// dialView is the projects sidebar while a server is being connected to:
// its name, how long the dial has taken, or why it failed. The lists of
// the server or folders chosen before are not shown: they are not what the
// user picked.
func dialView(d dialing, width, height int) string {
	faint := lipgloss.NewStyle().Faint(true)
	lines := []string{paneTitle("Projects", true), faint.Render(ansi.Truncate(" "+d.spec, width, "…")), ""}
	if d.err == nil {
		secs := int(time.Since(d.since) / time.Second)
		lines = append(lines, ansi.Truncate(fmt.Sprintf(" connecting… %ds", secs), width, "…"))
		if secs >= 3 {
			lines = append(lines, "", faint.Render(" an ssh login can take"), faint.Render(" a few seconds"))
		}
	} else {
		lines = append(lines, " couldn't connect:")
		for _, l := range strings.Split(ansi.Wrap(d.err.Error(), max(1, width-1), " "), "\n") {
			lines = append(lines, faint.Render(" "+l))
		}
		lines = append(lines, "", faint.Render(" pick another connection"), faint.Render(" with alt+← or alt+h"))
	}
	return strings.Join(lines[:min(len(lines), height)], "\n")
}

// serverLabel names the live server, "" when there is none.
func serverLabel(s Session) string {
	if s == nil {
		return ""
	}
	return s.Label()
}
