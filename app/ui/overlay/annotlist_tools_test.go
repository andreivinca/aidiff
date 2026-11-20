package overlay

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"github.com/umputun/revdiff/app/keymap"
)

func toolsList(canCopy, canDelete bool) *annotListOverlay {
	a := &annotListOverlay{height: 30, popupWidth: 80}
	a.open(AnnotListSpec{Items: []AnnotationItem{{AnnotationTarget: AnnotationTarget{File: "a.go", Line: 1}}}, CanCopy: canCopy, CanDelete: canDelete})
	return a
}

func TestAnnotListTools_Keys(t *testing.T) {
	key := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	a := toolsList(true, true)
	assert.Equal(t, OutcomeAnnotCopyAll, a.handleKey(key('c'), keymap.Action("")).Kind)
	assert.Equal(t, OutcomeAnnotDeleteAll, a.handleKey(key('D'), keymap.Action("")).Kind)

	off := toolsList(false, false)
	assert.Equal(t, OutcomeNone, off.handleKey(key('c'), keymap.Action("")).Kind, "not offered: the keys do nothing")
	assert.Equal(t, OutcomeNone, off.handleKey(key('D'), keymap.Action("")).Kind)
	assert.Zero(t, off.toolsHeight(), "and the list keeps its height")

	empty := &annotListOverlay{}
	empty.open(AnnotListSpec{CanCopy: true, CanDelete: true})
	assert.Empty(t, empty.tools(), "nothing to act on")
}

func TestAnnotListTools_Click(t *testing.T) {
	a := toolsList(true, true)
	// content starts at x=2, y=2; one item row, a blank row, then the tools
	row := 2 + a.maxVisible(a.height) + 1
	assert.Equal(t, OutcomeAnnotCopyAll, a.handleLeftClick(2+2, row).Kind, "on \"c copy all\"")
	assert.Equal(t, OutcomeAnnotDeleteAll, a.handleLeftClick(2+2+len("c copy all")+len(toolsGap), row).Kind, "on \"D delete all\"")
	assert.Equal(t, OutcomeNone, a.handleLeftClick(2+2+len("c copy all")+1, row).Kind, "between them")
	assert.Equal(t, OutcomeNone, a.handleLeftClick(2+2, row-1).Kind, "the blank row")

	d := toolsList(false, true)
	assert.Equal(t, OutcomeAnnotDeleteAll, d.handleLeftClick(2+2, row).Kind, "delete alone comes first")
}
