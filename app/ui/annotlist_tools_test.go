package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/ui/overlay"
)

// toolsModel is a review with two annotations and the @ list open.
func toolsModel(t *testing.T) Model {
	t.Helper()
	m := testModel([]string{"a.go", "b.go"}, nil)
	m.store.Add(annotation.Annotation{File: "a.go", Line: 5, Type: "+", Comment: "note"})
	m.store.Add(annotation.Annotation{File: "b.go", Line: 0, Comment: "file note"})
	return m
}

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestAnnotListTools_Copy(t *testing.T) {
	m := toolsModel(t)
	assert.False(t, m.buildAnnotListSpec().CanCopy, "no clipboard: no copy tool")

	var copied string
	var count int
	m.copyAnnotations = func(text string, n int) tea.Cmd {
		copied, count = text, n
		return func() tea.Msg { return "copied" }
	}
	spec := m.buildAnnotListSpec()
	assert.True(t, spec.CanCopy)
	assert.True(t, spec.CanDelete)
	m.overlay.OpenAnnotList(spec)
	view := ansi.Strip(m.overlay.Compose(m.View(), overlay.RenderCtx{Width: 120, Height: 30, Resolver: m.resolver}))
	assert.Contains(t, view, "c copy all   D delete all", "the tools under the list")

	r, cmd := m.Update(runeKey('c'))
	m = r.(Model)
	require.NotNil(t, cmd)
	assert.Equal(t, "copied", cmd())
	assert.Equal(t, m.store.FormatOutput(), copied, "formatted as on exit")
	assert.Equal(t, 2, count)
	assert.True(t, m.overlay.Active(), "the list stays open")
	assert.Equal(t, 2, m.store.Count())
}

func TestAnnotListTools_DeleteAllAsksFirst(t *testing.T) {
	m := toolsModel(t)
	var title, button string
	var yes tea.Cmd
	m.revert.confirm = func(ti, _, b string, y, _ tea.Cmd) tea.Cmd {
		title, button, yes = ti, b, y
		return func() tea.Msg { return "asking" }
	}
	m.overlay.OpenAnnotList(m.buildAnnotListSpec())

	r, cmd := m.Update(runeKey('D'))
	m = r.(Model)
	require.NotNil(t, cmd)
	assert.Equal(t, "asking", cmd())
	assert.Equal(t, "Delete all 2 annotations?", title)
	assert.Equal(t, "Delete", button)
	assert.False(t, m.overlay.Active(), "the confirmation takes the list's place")
	assert.Equal(t, 2, m.store.Count(), "nothing deleted before the answer")

	r, _ = m.Update(yes())
	m = r.(Model)
	assert.Zero(t, m.store.Count())
	assert.Equal(t, "Deleted 2 annotations", m.output.hint)
}

func TestAnnotListTools_DeleteAllWithoutPopup(t *testing.T) {
	m := toolsModel(t)
	m.overlay.OpenAnnotList(m.buildAnnotListSpec())
	r, _ := m.Update(runeKey('D'))
	m = r.(Model)
	assert.Zero(t, m.store.Count(), "no popup to ask in: deleted at once")
	assert.False(t, m.overlay.Active())

	r, cmd := m.handleAnnotListTool(overlay.OutcomeAnnotDeleteAll)
	assert.Nil(t, cmd, "nothing left to delete")
	assert.Zero(t, r.(Model).store.Count())
}
