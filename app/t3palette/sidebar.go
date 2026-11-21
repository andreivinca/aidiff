package t3palette

import (
	"fmt"
	"strings"

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
func (p *panelSidebar) Select(n int) (tea.Cmd, bool)             { return p.current().Select(n) }

// Available reports whether a server is connected.
func (b *projectsSidebar) Available() bool {
	b.t.mu.Lock()
	defer b.t.mu.Unlock()
	return b.t.live != nil // a T3 server's projects only
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
	b.sel = min(b.sel, max(0, len(list)-1))

	bold := lipgloss.NewStyle().Bold(true)
	faint := lipgloss.NewStyle().Faint(true)
	lines := []string{bold.Render(" T3 Projects"), faint.Render(" " + serverLabel(v.live)), ""}
	switch {
	case v.live == nil:
		lines = append(lines, faint.Render(" not connected"))
	case len(list) == 0 && v.shell.Projects == nil && v.shellErr != nil: // the fetch failed: say why, not "loading…"
		why := strings.Split(ansi.Wrap(v.shellErr.Error(), max(1, width-1), " "), "\n")
		for _, l := range why[:min(len(why), 3)] {
			lines = append(lines, faint.Render(" "+l))
		}
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
	b.offset = max(0, min(b.offset, len(list)-rows))
	current := v.currentProjectID()
	for i := b.offset; i < min(len(list), b.offset+rows); i++ {
		lines = append(lines, b.row(i, list[i], v.shell, width, i == b.sel, focused, list[i].ID == current))
	}
	for _, h := range help {
		lines = append(lines, faint.Render(ansi.Truncate(h, width, "…")))
	}
	return strings.Join(lines[:min(len(lines), height)], "\n")
}

// sidebarHelp is the key help drawn under the projects, after a blank row.
var sidebarHelp = []string{
	"",
	" enter        open project",
	" a            add project",
	" ctrl+b       show / hide",
	" ctrl/alt+1…9 switch project",
	" alt+s        switch session",
	" alt+← alt+→  move focus",
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
	style := lipgloss.NewStyle()
	switch {
	case selected && focused:
		style = style.Reverse(true)
	case selected:
		style = style.Underline(true)
	}
	if current {
		style = style.Bold(true)
	}
	return style.Render(text)
}

// HandleKey moves the selection and opens the selected project; a digit
// opens its project, esc hides the sidebar.
func (b *projectsSidebar) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	list := projects(b.t.state().shell)
	switch msg.String() {
	case "up", "k":
		b.sel = max(0, b.sel-1)
	case "down", "j":
		b.sel = max(0, min(len(list)-1, b.sel+1))
	case "home", "g":
		b.sel = 0
	case "end", "G":
		b.sel = max(0, len(list)-1)
	case "enter", " ":
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
	list := projects(b.t.state().shell)
	if y < sidebarHeader || i >= len(list) {
		return nil, false
	}
	b.sel = i
	return b.open(list, i)
}

// Select opens project n, counted from 1.
func (b *projectsSidebar) Select(n int) (tea.Cmd, bool) {
	list := projects(b.t.state().shell)
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

// serverLabel names the live server, "" when there is none.
func serverLabel(s Session) string {
	if s == nil {
		return ""
	}
	return s.Label()
}
