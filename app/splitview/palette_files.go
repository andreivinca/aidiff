package splitview

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// commandPrefix starts a palette query that filters the commands. The
// palette opens with it typed; with Config.Files set, deleting it searches
// files by name instead, and typing it again goes back to the commands.
const commandPrefix = ">"

// FilesMsg answers a Config.Files search: the files found for Query, in
// order, picked with OnPick. Note, when set, is said under the rows, such as
// that only the first matches are listed or why the search failed.
type FilesMsg struct {
	Query  string
	Items  []PickerItem
	OnPick func(PickerItem) tea.Cmd
	Note   string
}

// fileMode reports whether the palette searches files: the command list
// with a query that does not start with commandPrefix, when the program
// searches files at all.
func (m Model) fileMode() bool {
	return m.cfg.Files != nil && m.palette.mode == modeCommands && !strings.HasPrefix(m.palette.query, commandPrefix)
}

// commandQuery is the query the commands are filtered by, commandPrefix
// left out.
func (p palette) commandQuery() string {
	return strings.TrimPrefix(p.query, commandPrefix)
}

// queryChanged refilters the palette after its query changed and, in file
// mode, starts the search for it. The rows found for the previous query stay
// on screen until the answer arrives.
func (m Model) queryChanged() (Model, tea.Cmd) {
	m.palette.sel = 0
	if !m.fileMode() {
		m.palette.files, m.palette.filesNote, m.palette.searching = nil, "", false
		return m.refilterPalette(), nil
	}
	cmd := m.cfg.Files(m.inner, m.palette.query)
	m.palette.searching = cmd != nil
	if cmd == nil {
		m.palette.files, m.palette.filesNote = nil, ""
	}
	return m.refilterPalette(), cmd
}

// showFiles takes a search's answer, unless the query changed since.
func (m Model) showFiles(msg FilesMsg) Model {
	if !m.palette.open || !m.fileMode() || msg.Query != m.palette.query {
		return m
	}
	rows := make([]row, 0, len(msg.Items))
	for _, it := range msg.Items {
		rows = append(rows, pickRow(it, msg.OnPick))
	}
	m.palette.files, m.palette.filesNote, m.palette.searching = rows, msg.Note, false
	m.palette.sel = 0
	return m.refilterPalette()
}

// emptyText is what the palette says when it lists no row.
func (m Model) emptyText() string {
	switch {
	case m.palette.mode == modePick:
		return "  no matches"
	case !m.fileMode():
		return "  no matching commands"
	case m.palette.searching:
		return "  searching…"
	case strings.TrimSpace(m.palette.query) == "":
		return "  type a file name"
	}
	return "  no matching files"
}
