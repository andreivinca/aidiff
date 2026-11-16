package splitview

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// paletteMode is what the palette popup shows.
type paletteMode int

const (
	modeCommands paletteMode = iota // the command list
	modePick                        // a ShowPickerMsg list
	modePrompt                      // a ShowPromptMsg text line
)

// palette is the palette popup: the command list, or the picker or prompt a
// command opened.
type palette struct {
	open  bool
	mode  paletteMode
	query string
	sel   int
	rows  []row // what the query matches; rebuilt whenever it may change

	// pick and prompt modes
	title       string
	items       []PickerItem
	onPick      func(PickerItem) tea.Cmd
	onText      func(string) tea.Cmd // pick: the free-text row; prompt: enter
	textLabel   string
	placeholder string
}

// row is one line of the palette in any mode.
type row struct {
	label  string
	detail string
	group  string // section title; "" for the ungrouped rows on top
	run    func(Model) (tea.Model, tea.Cmd)
}

// openPalette shows the command list.
func (m Model) openPalette() Model {
	m.palette = palette{open: true}
	return m.refilterPalette()
}

// showPicker opens a ShowPickerMsg list in the palette.
func (m Model) showPicker(msg ShowPickerMsg) (tea.Model, tea.Cmd) {
	m.flash = ""
	m.palette = palette{
		open:      true,
		mode:      modePick,
		title:     msg.Title,
		items:     msg.Items,
		onPick:    msg.OnPick,
		onText:    msg.OnText,
		textLabel: msg.TextLabel,
	}
	if m.palette.textLabel == "" {
		m.palette.textLabel = "%s"
	}
	return m.refilterPalette(), nil
}

// showPrompt opens a ShowPromptMsg text line in the palette.
func (m Model) showPrompt(msg ShowPromptMsg) (tea.Model, tea.Cmd) {
	m.flash = ""
	m.palette = palette{
		open:        true,
		mode:        modePrompt,
		title:       msg.Title,
		placeholder: msg.Placeholder,
		onText:      msg.OnSubmit,
	}
	return m, nil
}

// refilterPalette rebuilds the rows for the current query and state, keeping
// the selection in range.
func (m Model) refilterPalette() Model {
	m.palette.rows = m.paletteRows()
	m.palette.sel = min(m.palette.sel, max(0, len(m.palette.rows)-1))
	return m
}

// paletteRows is the row list filtered by the typed query.
func (m Model) paletteRows() []row {
	p := m.palette
	switch p.mode {
	case modePrompt:
		return nil
	case modePick:
		return p.pickRows()
	case modeCommands:
	}
	var out []row
	for _, r := range m.commands() {
		if fuzzyMatch(p.query, r.label) {
			out = append(out, r)
		}
	}
	return out
}

// commands lists what the palette offers in the current state: the
// ungrouped commands, Quit last among them, then each group's commands.
func (m Model) commands() []row {
	rows := []row{{label: "Open Terminal", run: Model.openTerminal}}
	var groups []string
	byGroup := map[string][]row{}
	if m.cfg.Commands != nil {
		for _, c := range m.cfg.Commands() {
			r := row{label: c.Name, detail: c.Detail, group: c.Group, run: runCommand(c)}
			if c.Group == "" {
				rows = append(rows, r)
				continue
			}
			if _, seen := byGroup[c.Group]; !seen {
				groups = append(groups, c.Group)
			}
			byGroup[c.Group] = append(byGroup[c.Group], r)
		}
	}
	rows = append(rows, row{label: "Quit", detail: m.cfg.Keys.Quit, run: Model.askQuit})
	for _, g := range groups {
		rows = append(rows, byGroup[g]...)
	}
	return rows
}

// runCommand adapts an outside command to a palette row.
func runCommand(c Command) func(Model) (tea.Model, tea.Cmd) {
	return func(m Model) (tea.Model, tea.Cmd) {
		if c.Run == nil {
			return m, nil
		}
		return m, c.Run(m.inner)
	}
}

// pickRows filters the picker's items and appends the free-text row.
func (p palette) pickRows() []row {
	var out []row
	text := strings.TrimSpace(p.query)
	exact := false
	for _, it := range p.items {
		if fuzzyMatch(p.query, it.Label+" "+it.Detail) {
			out = append(out, pickRow(it, p.onPick))
		}
		exact = exact || strings.EqualFold(it.Label, text)
	}
	if text != "" && p.onText != nil && !exact {
		out = append(out, textRow(fmt.Sprintf(p.textLabel, text), text, p.onText))
	}
	return out
}

// pickRow adapts a picker item to a palette row.
func pickRow(it PickerItem, onPick func(PickerItem) tea.Cmd) row {
	run := func(m Model) (tea.Model, tea.Cmd) {
		if onPick == nil {
			return m, nil
		}
		return m, onPick(it)
	}
	return row{label: it.Label, detail: it.Detail, run: run}
}

// textRow is the picker's free-text row: it hands text to onText.
func textRow(label, text string, onText func(string) tea.Cmd) row {
	run := func(m Model) (tea.Model, tea.Cmd) {
		return m, onText(text)
	}
	return row{label: label, run: run}
}

// fuzzyMatch reports whether every character of query appears in s in order,
// ignoring case and spaces ("opt" matches "Open Terminal").
func fuzzyMatch(query, s string) bool {
	s = strings.ToLower(s)
	i := 0
	for _, r := range strings.ToLower(query) {
		if unicode.IsSpace(r) {
			continue
		}
		j := strings.IndexRune(s[i:], r)
		if j < 0 {
			return false
		}
		i += j + len(string(r))
	}
	return true
}

// handlePaletteKey edits the query, moves the selection (up/shift+tab/ctrl+k
// and down/tab/ctrl+j, wrapping around) or runs the selection.
func (m Model) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", m.cfg.Keys.Palette:
		m.palette = palette{}
		return m, nil
	case "enter":
		return m.runPaletteSelection()
	case "up", "shift+tab", "ctrl+k":
		m.palette.move(-1)
	case "down", "tab", "ctrl+j":
		m.palette.move(1)
	case "backspace":
		if r := []rune(m.palette.query); len(r) > 0 {
			m.palette.query = string(r[:len(r)-1])
		}
		m.palette.sel = 0
		m = m.refilterPalette()
	default:
		if text, ok := m.palette.typed(msg); ok {
			m.palette.query += text
			m.palette.sel = 0
			m = m.refilterPalette()
		}
	}
	return m, nil
}

// move shifts the selection by delta rows, wrapping around the ends.
func (p *palette) move(delta int) {
	n := len(p.rows)
	if n == 0 {
		p.sel = 0
		return
	}
	p.sel = ((p.sel+delta)%n + n) % n
}

// typed returns the text a key adds to the query. The command list takes
// typed characters only; pickers and prompts also take pasted text, with line
// breaks flattened to spaces.
func (p palette) typed(msg tea.KeyMsg) (string, bool) {
	if msg.Alt || (msg.Type != tea.KeyRunes && msg.Type != tea.KeySpace) {
		return "", false
	}
	if msg.Paste && p.mode == modeCommands {
		return "", false
	}
	if msg.Type == tea.KeySpace {
		return " ", true
	}
	text := string(msg.Runes)
	if msg.Paste {
		text = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(text)
	}
	return text, true
}

// runPaletteSelection acts on enter: the prompt's text, or the selected row.
func (m Model) runPaletteSelection() (tea.Model, tea.Cmd) {
	p := m.palette
	if p.mode == modePrompt {
		text := strings.TrimSpace(p.query)
		if text == "" || p.onText == nil {
			return m, nil
		}
		m.palette = palette{}
		return m, p.onText(text)
	}
	if p.sel < len(p.rows) {
		run := p.rows[p.sel].run
		m.palette = palette{}
		return run(m)
	}
	return m, nil
}
