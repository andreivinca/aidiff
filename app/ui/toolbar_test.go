package ui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/keymap"
)

// toolbarModel is a loaded review of two files with the toolbar on, sized
// 120x30.
func toolbarModel(t *testing.T) Model {
	t.Helper()
	m := loadFileIntoModel(t, []string{"a.go", "b.go"}, map[string][]diff.DiffLine{
		"a.go": {{ChangeType: diff.ChangeAdd, Content: "x", NewNum: 1}},
	})
	m.toolbar.on = true
	m.compact.applicable = true
	m.modes.compactContext = 5
	r, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return r.(Model)
}

func screenRows(m Model) []string {
	return strings.Split(ansi.Strip(m.View()), "\n")
}

func key(k string) tea.KeyMsg {
	switch k {
	case "f10":
		return tea.KeyMsg{Type: tea.KeyF10}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		r, _ := m.Update(key(k))
		m = r.(Model)
	}
	return m
}

func click(t *testing.T, m Model, x, y int) (Model, tea.Cmd) {
	t.Helper()
	r, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	return r.(Model), cmd
}

// buttonX is the screen column of the toolbar button whose text holds label.
func buttonX(t *testing.T, m Model, label string) int {
	t.Helper()
	for _, s := range m.toolbarLayout(m.toolbarButtons(), m.layout.width) {
		if strings.Contains(s.text, label) {
			return s.x
		}
	}
	t.Fatalf("no %q button", label)
	return 0
}

func TestToolbar_TopRowAboveBothPanes(t *testing.T) {
	m := toolbarModel(t)
	rows := screenRows(m)
	require.Len(t, rows, 30, "the toolbar and the review fill the window")
	assert.Contains(t, rows[0], "◧ View ▾")
	assert.Contains(t, rows[0], "↕ Whole files ▾", "compact is off")
	assert.Contains(t, rows[0], "? Help ?")
	assert.True(t, strings.HasPrefix(rows[0], " ◧"), "it starts at the left edge, over the tree")
	assert.Contains(t, rows[2], "a.go", "the review lies below it, its file header on its second row")
	assert.Equal(t, 29, m.layout.height, "the review has the rest of the window")

	m.toolbar.on = false
	r, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = r.(Model)
	assert.NotContains(t, screenRows(m)[0], "View ▾")
	assert.Equal(t, 30, m.layout.height)
}

func TestToolbar_ClicksBelowReachTheReviewOneRowHigher(t *testing.T) {
	m := toolbarModel(t)
	m.layout.focus = paneTree
	m, _ = click(t, m, 100, 1+m.diffTopRow()) // the diff's first row, under the toolbar
	assert.Equal(t, paneDiff, m.layout.focus)
	assert.Equal(t, 0, m.nav.diffCursor)
}

func TestToolbar_DrawnWhereClicksLand(t *testing.T) {
	m := toolbarModel(t)
	m.toolbar.compare = func() tea.Cmd { return nil }
	row := screenRows(m)[toolbarRow]
	for _, label := range []string{"View", "Compare", "Notes", "Help"} {
		at := strings.Index(row, label)
		require.GreaterOrEqual(t, at, 0)
		x := buttonX(t, m, label)
		assert.Equal(t, ansi.StringWidth(row[:at]), x+2, "%s: its label starts after its icon and a space", label)
	}
}

func TestToolbar_IconsAreOneCellEverywhere(t *testing.T) {
	m := toolbarModel(t)
	m.toolbar.compare = func() tea.Cmd { return nil }
	m.reload.applicable = true
	for _, b := range m.toolbarButtons() {
		assert.Equal(t, 1, lipgloss.Width(b.icon), "%s: a glyph some terminals draw two cells wide shifts every click after it", b.label)
	}
}

func TestToolbar_KeyboardMenu(t *testing.T) {
	m := toolbarModel(t)
	require.False(t, m.modes.lineNumbers)

	m = press(t, m, "f10")
	require.True(t, m.toolbar.open)
	rows := strings.Join(screenRows(m), "\n")
	assert.NotContains(t, rows, "Changed blocks only", "the Compare tool's choice, not View's")
	assert.Contains(t, screenRows(m)[toolbarRow+2], "Line numbers", "the toggles lead")

	m = press(t, m, "enter") // Line numbers
	assert.True(t, m.modes.lineNumbers)
	assert.True(t, m.toolbar.open, "a toggle keeps the menu open")
	assert.Contains(t, strings.Join(screenRows(m), "\n"), "✓ Line numbers")

	m = press(t, m, "esc")
	assert.False(t, m.toolbar.open)
	assert.False(t, m.paneFocusBusy())
}

func TestToolbar_OtherKeyClosesAndActs(t *testing.T) {
	m := toolbarModel(t)
	m = press(t, m, "f10", "w")
	assert.False(t, m.toolbar.open)
	assert.True(t, m.modes.wrap, "w does what it does with no menu open")
}

func TestToolbar_ContextMenuPicksASize(t *testing.T) {
	m := toolbarModel(t)
	m = press(t, m, "f10", "right")
	require.True(t, m.toolbar.open)
	require.Equal(t, 1, m.toolbar.button)
	assert.Contains(t, strings.Join(screenRows(m), "\n"), "✓ Whole files", "whole files on screen: checked, on top")
	m = press(t, m, "down", "down", "down") // past the divider: 1, 2, 3 lines
	r, cmd := m.Update(key("enter"))
	m = r.(Model)
	assert.False(t, m.toolbar.open)
	assert.True(t, m.modes.compact, "a size leaves whole files for the changed blocks")
	assert.Equal(t, 3, m.modes.compactContext)
	assert.NotNil(t, cmd, "the file is fetched again")
	assert.Contains(t, screenRows(m)[toolbarRow], "↕ 3 lines ▾")

	m = press(t, m, "f10", "right")
	require.True(t, m.toolbar.open)
	assert.NotContains(t, strings.Join(screenRows(m), "\n"), "✓ Whole files")
	m = press(t, m, "up", "up", "up", "up", "up", "up", "up", "up") // to the top: Whole files
	r, cmd = m.Update(key("enter"))
	m = r.(Model)
	assert.False(t, m.modes.compact, "Whole files goes back to whole files")
	assert.NotNil(t, cmd, "the file is fetched again")
	assert.Contains(t, screenRows(m)[toolbarRow], "↕ Whole files ▾")
}

func TestToolbar_MouseOpensPicksAndCloses(t *testing.T) {
	m := toolbarModel(t)
	m, _ = click(t, m, buttonX(t, m, "View"), toolbarRow)
	require.True(t, m.toolbar.open)
	box, ok := m.menuGeometry()
	require.True(t, ok)

	m, _ = click(t, m, box.x+3, box.y+1+1) // Wrap lines
	assert.True(t, m.modes.wrap)
	assert.True(t, m.toolbar.open)

	before := m.nav.diffCursor
	m, _ = click(t, m, 110, 20) // outside: closes, and goes no further
	assert.False(t, m.toolbar.open)
	assert.Equal(t, before, m.nav.diffCursor)

	m, _ = click(t, m, buttonX(t, m, "View"), toolbarRow)
	m, _ = click(t, m, buttonX(t, m, "View"), toolbarRow)
	assert.False(t, m.toolbar.open, "a second click on the button closes it")
}

func TestToolbar_PlainButtonRunsAtOnce(t *testing.T) {
	m := toolbarModel(t)
	compared := false
	m.toolbar.compare = func() tea.Cmd {
		compared = true
		return nil
	}
	m, _ = click(t, m, buttonX(t, m, "Compare"), toolbarRow)
	assert.True(t, compared)
	assert.False(t, m.toolbar.open)

	m, _ = click(t, m, buttonX(t, m, "Help"), toolbarRow)
	assert.True(t, m.overlay.Active(), "Help opens its popup")
	assert.False(t, m.toolbar.open)
}

func TestToolbar_NarrowPaneDropsDetail(t *testing.T) {
	m := toolbarModel(t)
	buttons := m.toolbarButtons()
	full := m.toolbarLayout(buttons, 200)
	assert.Contains(t, full[len(full)-1].text, "Help ?")
	short := m.toolbarLayout(buttons, 50)
	assert.Len(t, short, len(buttons))
	assert.NotContains(t, short[len(short)-1].text, "?  ", "keys go first")
	tiny := m.toolbarLayout(buttons, 8)
	require.NotEmpty(t, tiny)
	assert.Less(t, len(tiny), len(buttons), "icons that do not fit are left out")
	for _, s := range tiny {
		assert.LessOrEqual(t, s.x+s.w, 8)
	}
}

func TestToolbar_MenuKeys(t *testing.T) {
	m := toolbarModel(t)
	assert.Contains(t, screenRows(m)[0], "◧ View ▾ alt+v", "a dropdown shows its key")
	assert.Contains(t, screenRows(m)[0], "▾ alt+c")

	alt := func(m Model, r rune) Model {
		res, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true})
		return res.(Model)
	}
	m = alt(m, 'v')
	require.True(t, m.toolbar.open)
	assert.Equal(t, 0, m.toolbar.button, "alt+v opens View")
	m = alt(m, 'c')
	assert.True(t, m.toolbar.open)
	assert.Equal(t, 1, m.toolbar.button, "alt+c switches to the context menu")
	m = alt(m, 'c')
	assert.False(t, m.toolbar.open, "its own key closes it")

	m.compact.applicable = false
	m = alt(m, 'c')
	assert.False(t, m.toolbar.open, "no context menu where the context cannot change")
	assert.NotEmpty(t, m.compact.hint)
}

func TestToolbar_ViewSwitchesBetweenChangesAndFiles(t *testing.T) {
	m := toolbarModel(t)
	var asked []bool
	m.toolbar.showFiles = func(files bool) tea.Cmd {
		asked = append(asked, files)
		return nil
	}

	r, cmd := m.Update(key("f10"))
	m = r.(Model)
	assert.Nil(t, cmd, "opening View asks for nothing")
	menu := strings.Join(screenRows(m), "\n")
	assert.Contains(t, menu, "✓ Changes")
	assert.Contains(t, menu, "Project files")
	assert.NotContains(t, menu, "Uncommitted", "what the changes compare is the Compare button's")

	m = press(t, m, "down", "enter") // Project files
	assert.Equal(t, []bool{true}, asked)
	assert.False(t, m.toolbar.open)

	m.toolbar.showFiles = nil
	m = press(t, m, "f10")
	assert.NotContains(t, strings.Join(screenRows(m), "\n"), "Project files", "no switch, no rows")
}

func TestToolbar_ViewToggle(t *testing.T) {
	m := toolbarModel(t)
	assert.NotContains(t, screenRows(m)[0], "±", "no switch, no toggle")

	var asked []bool
	m.toolbar.showFiles = func(files bool) tea.Cmd {
		asked = append(asked, files)
		return nil
	}
	row := screenRows(m)[0]
	assert.True(t, strings.HasPrefix(row, " ± ☰  ◧ View"), "the toggle leads the toolbar, its segments one cell apart: %q", row)

	spans := m.toolbarLayout(m.toolbarButtons(), m.layout.width)
	require.GreaterOrEqual(t, len(spans), 3)
	assert.True(t, spans[0].on, "the changes are on screen")
	assert.False(t, spans[1].on)

	m, _ = click(t, m, spans[0].x, toolbarRow)
	assert.Empty(t, asked, "the segment in effect does nothing")
	m, _ = click(t, m, spans[1].x, toolbarRow)
	assert.Equal(t, []bool{true}, asked, "the other switches to the project's files")
	assert.False(t, m.toolbar.open)

	m = press(t, m, "f10")
	assert.Equal(t, keymap.ActionViewMenu, m.toolbarButtons()[m.toolbar.button].action, "f10 still opens View, past the toggle")
}

func TestToolbar_OpeningFromTheTreeRepaintsTheDiffFocused(t *testing.T) {
	m := toolbarModel(t)
	m.layout.focus = paneTree
	m.layout.viewport.SetContent(m.renderDiff())
	unfocused := slices.Clone(m.layout.viewport.lines)

	m = press(t, m, "f10", "esc")
	assert.Equal(t, paneDiff, m.layout.focus)
	assert.Equal(t, strings.Split(m.renderDiff(), "\n"), m.layout.viewport.lines, "the diff is drawn focused")
	assert.NotEqual(t, unfocused, m.layout.viewport.lines, "with its cursor")
}
