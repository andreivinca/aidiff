package splitview

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	paletteMaxRows   = 10
	paletteWidth     = 64 // the command list
	pickerWidth      = 90 // pickers and prompts carry longer labels
	paletteMinWidth  = 20
	promptTailMarker = "…"
)

// paletteMode is what the ctrl+p popup shows.
type paletteMode int

const (
	modeCommands paletteMode = iota // the command list
	modePick                        // a ShowPickerMsg list
	modePrompt                      // a ShowPromptMsg text line
)

// palette is the ctrl+p popup: the command list, or the picker or prompt a
// command opened.
type palette struct {
	open  bool
	mode  paletteMode
	query string
	sel   int

	// pick and prompt modes
	title       string
	items       []PickerItem
	onPick      func(PickerItem) tea.Cmd
	onText      func(string) tea.Cmd // pick: the free-text row; prompt: enter
	textLabel   string
	placeholder string
}

func newPalette() palette { return palette{open: true} }

// Command is a palette entry the embedding program adds through
// Config.Commands.
type Command struct {
	Name   string
	Detail string // dim text on the right, if any
	// Run starts the command. It receives the wrapped model, so a command can
	// read the review it acts on, and returns the work to do, if any.
	Run func(inner tea.Model) tea.Cmd
}

// PickerItem is one row of a ShowPickerMsg list.
type PickerItem struct {
	Label  string // what the user filters on and picks
	Detail string // dim text on the right, also matched by the filter
	Value  any    // handed back to OnPick untouched
}

// ShowPickerMsg opens a filterable list in the palette. Enter hands the
// selected row to OnPick. With OnText set, typed text that is not a row's
// label is offered as a final row, labeled by TextLabel (a format with one
// %s; "%s" when empty), and Enter on it hands the text to OnText.
type ShowPickerMsg struct {
	Title     string
	Items     []PickerItem
	OnPick    func(PickerItem) tea.Cmd
	OnText    func(string) tea.Cmd
	TextLabel string
}

// ShowPromptMsg asks for one line of text in the palette. Enter hands the
// trimmed text to OnSubmit; an empty line is ignored; esc cancels.
type ShowPromptMsg struct {
	Title       string
	Placeholder string
	OnSubmit    func(string) tea.Cmd
}

// FlashMsg shows a one-line message on the bottom row until the next key.
type FlashMsg struct{ Text string }

// ReplaceInnerMsg swaps the wrapped model for another one, which is
// initialized and sized like the first was. Retire, when set, receives the
// model being replaced first, e.g. to keep its annotations.
type ReplaceInnerMsg struct {
	Model  tea.Model
	Retire func(old tea.Model)
}

// row is one line of the palette in any mode.
type row struct {
	label  string
	detail string
	run    func(Model) (tea.Model, tea.Cmd)
}

// commands lists what the palette offers in the current state.
func (m Model) commands() []row {
	rows := []row{{label: "Open Terminal", run: Model.openTerminal}}
	if len(m.terms) > 0 {
		rows = append(rows, row{label: "Close Terminal", run: Model.closeFocusedTerminal})
	}
	if m.cfg.Commands != nil {
		for _, c := range m.cfg.Commands() {
			rows = append(rows, row{label: c.Name, detail: c.Detail, run: runCommand(c)})
		}
	}
	rows = append(rows, row{label: "Quit", detail: QuitKey, run: Model.askQuit})
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

// pickRow adapts a picker item to a palette row.
func pickRow(it PickerItem, onPick func(PickerItem) tea.Cmd) row {
	return row{label: it.Label, detail: it.Detail, run: func(m Model) (tea.Model, tea.Cmd) {
		if onPick == nil {
			return m, nil
		}
		return m, onPick(it)
	}}
}

// closeFocusedTerminal closes the focused terminal, or the bottom one when the
// diff has focus.
func (m Model) closeFocusedTerminal() (tea.Model, tea.Cmd) {
	if len(m.terms) == 0 {
		return m, nil
	}
	i := m.focus
	if i == focusDiff {
		i = len(m.terms) - 1
	}
	return m.closeTerminal(i)
}

// paletteItems is the row list filtered by the typed query.
func (m Model) paletteItems() []row {
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
		onText := p.onText
		out = append(out, row{label: fmt.Sprintf(p.textLabel, text), run: func(m Model) (tea.Model, tea.Cmd) {
			return m, onText(text)
		}})
	}
	return out
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

func (m Model) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.paletteItems()
	switch msg.String() {
	case "esc", PaletteKey:
		m.palette = palette{}
		return m, nil
	case "enter":
		return m.runPaletteSelection(items)
	case "up", "ctrl+k":
		m.palette.sel--
	case "down", "ctrl+n", "ctrl+j", "tab":
		m.palette.sel++
	case "backspace":
		if r := []rune(m.palette.query); len(r) > 0 {
			m.palette.query = string(r[:len(r)-1])
		}
		m.palette.sel = 0
	default:
		if text, ok := m.palette.typed(msg); ok {
			m.palette.query += text
			m.palette.sel = 0
		}
	}
	n := len(m.paletteItems())
	switch {
	case n == 0:
		m.palette.sel = 0
	case m.palette.sel < 0:
		m.palette.sel = n - 1
	case m.palette.sel >= n:
		m.palette.sel = 0
	}
	return m, nil
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
func (m Model) runPaletteSelection(items []row) (tea.Model, tea.Cmd) {
	p := m.palette
	if p.mode == modePrompt {
		text := strings.TrimSpace(p.query)
		if text == "" || p.onText == nil {
			return m, nil
		}
		m.palette = palette{}
		return m, p.onText(text)
	}
	if p.sel < len(items) {
		run := items[p.sel].run
		m.palette = palette{}
		return run(m)
	}
	return m, nil
}

// paletteView renders the popup box.
func (m Model) paletteView() string {
	p := m.palette
	maxW := paletteWidth
	if p.mode != modeCommands {
		maxW = pickerWidth
	}
	inner := min(maxW, max(paletteMinWidth, m.width-4)) - 2

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	var lines []string
	if p.title != "" {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Render(fitLine(" "+p.title, inner)))
	}
	lines = append(lines, p.inputLine(inner, dim))
	if p.mode == modePrompt {
		lines = append(lines, dim.Render(fitLine(" enter send · esc cancel", inner)))
		return paletteBox(lines)
	}
	lines = append(lines, dim.Render(strings.Repeat("─", inner)))
	items := m.paletteItems()
	if len(items) == 0 {
		empty := "  no matching commands"
		if p.mode == modePick {
			empty = "  no matches"
		}
		lines = append(lines, dim.Render(fitLine(empty, inner)))
	}
	start := max(0, p.sel-paletteMaxRows+1)
	for i := start; i < len(items) && i < start+paletteMaxRows; i++ {
		lines = append(lines, rowLine(items[i], inner, i == p.sel, dim))
	}
	return paletteBox(lines)
}

// inputLine is the query line. A long prompt shows its tail so the cursor
// stays visible; an empty prompt shows the placeholder.
func (p palette) inputLine(inner int, dim lipgloss.Style) string {
	const prefix = " > "
	if p.query == "" && p.placeholder != "" {
		return fitLine(prefix+"█"+dim.Render(p.placeholder), inner)
	}
	q := p.query
	if room := inner - ansi.StringWidth(prefix) - 1; ansi.StringWidth(q) > room && room > 1 {
		q = promptTailMarker + ansi.TruncateLeft(q, ansi.StringWidth(q)-room+1, "")
	}
	return fitLine(prefix+q+"█", inner)
}

// rowLine renders one row: label left, detail right, reversed when selected.
// The label keeps its width; a long detail is shortened to the room left.
func rowLine(r row, inner int, selected bool, dim lipgloss.Style) string {
	const minDetail = 12
	label := "  " + r.label
	line := fitLine(label, inner)
	if r.detail != "" {
		labelW := ansi.StringWidth(label)
		room := inner - labelW - 2 // one gap, one trailing space
		if room < minDetail {
			room = min(minDetail, max(1, inner-10))
			labelW = inner - room - 2
		}
		plain := ansi.Truncate(r.detail, room, "…")
		pad := max(1, inner-labelW-ansi.StringWidth(plain)-1)
		detail := plain
		if !selected {
			detail = dim.Render(plain)
		}
		line = fitLine(fitLine(label, labelW)+strings.Repeat(" ", pad)+detail+" ", inner)
	}
	if selected {
		line = lipgloss.NewStyle().Reverse(true).Render(line)
	}
	return line
}

func paletteBox(lines []string) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")).
		Render(strings.Join(lines, "\n"))
}

// overlayCenter draws box over base, horizontally centered, a third of the
// way down.
func overlayCenter(base, box string, width, height int) string {
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")
	bw := 0
	for _, l := range boxLines {
		bw = max(bw, ansi.StringWidth(l))
	}
	x0 := max(0, (width-bw)/2)
	y0 := max(0, (height-len(boxLines))/3)
	for i, bl := range boxLines {
		y := y0 + i
		if y >= len(baseLines) {
			break
		}
		line := baseLines[y]
		left := fitLine(ansi.Cut(line, 0, x0), x0)
		right := ansi.Cut(line, x0+bw, width)
		baseLines[y] = left + ansi.ResetStyle + fitLine(bl, bw) + ansi.ResetStyle + right
	}
	return strings.Join(baseLines, "\n")
}

// quit dialog buttons
const (
	quitButton = iota
	cancelButton
)

func (m Model) handleQuitDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", QuitKey:
		m.confirmQuit = false
		return m, tea.Quit
	case "n", "N", "esc":
		m.confirmQuit = false
	case "left", "right", "tab", "shift+tab", "h", "l":
		m.quitSel = 1 - m.quitSel
	case "enter", " ":
		m.confirmQuit = false
		if m.quitSel == quitButton {
			return m, tea.Quit
		}
	}
	return m, nil // anything else keeps the dialog open
}

// quitDialogView renders the quit confirmation popup.
func (m Model) quitDialogView() string {
	shells, note := 0, ""
	for _, t := range m.terms {
		if !needsConfirm(t) {
			continue
		}
		if n, ok := t.(QuitNoted); ok {
			note = n.QuitNote()
			continue
		}
		shells++
	}
	detail := note
	switch {
	case shells == 1:
		detail = "Closes the terminal and everything running in it."
	case shells > 1:
		detail = "Closes " + strconv.Itoa(shells) + " terminals and everything running in them."
	case detail == "":
		detail = "Leaves the review."
	}
	inner := min(paletteWidth, max(paletteMinWidth, m.width-4)) - 2

	button := func(label string, selected bool) string {
		st := lipgloss.NewStyle().Padding(0, 2)
		if selected {
			st = st.Reverse(true)
		}
		return st.Render(label)
	}
	buttons := button("Quit", m.quitSel == quitButton) + "  " + button("Cancel", m.quitSel == cancelButton)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	lines := make([]string, 0, 8)
	lines = append(lines, " Quit aidiff?", "")
	for l := range strings.SplitSeq(ansi.Wordwrap(detail, inner-2, ""), "\n") {
		lines = append(lines, " "+l)
	}
	lines = append(lines,
		"",
		lipgloss.PlaceHorizontal(inner, lipgloss.Center, buttons),
		"",
		dim.Render(lipgloss.PlaceHorizontal(inner, lipgloss.Center, "y quit · n/esc cancel · ←→ enter")),
	)
	for i, l := range lines {
		lines[i] = fitLine(l, inner)
	}
	return paletteBox(lines)
}
