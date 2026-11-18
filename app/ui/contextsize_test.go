package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/ui/mocks"
)

func altKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true}
}

func TestModel_ChangeCompactContext_StepsAndRefetches(t *testing.T) {
	var got []int
	renderer := &mocks.RendererMock{
		ChangedFilesFunc: func(string, bool) ([]diff.FileEntry, error) { return nil, nil },
		FileDiffFunc: func(req diff.FileDiffRequest) ([]diff.DiffLine, error) {
			got = append(got, req.ContextLines)
			return nil, nil
		},
	}
	m := testModel([]string{"a.go"}, nil)
	m.diffRenderer = renderer
	m.compact.applicable = true
	m.modes.compact = true
	m.modes.compactContext = 5
	m.file.name = "a.go"

	press := func(r rune) tea.Cmd {
		result, cmd := m.Update(altKey(r))
		m = result.(Model)
		if cmd != nil {
			cmd()
		}
		return cmd
	}

	require.NotNil(t, press('='))
	assert.Equal(t, 6, m.CompactContext())
	require.NotNil(t, press('+'))
	assert.Equal(t, 7, m.CompactContext())
	for range 6 {
		press('-')
	}
	assert.Equal(t, 1, m.CompactContext())
	assert.Nil(t, press('-'), "at one line there is nothing to shrink")
	assert.Equal(t, 1, m.CompactContext())
	assert.Equal(t, "1 line around each change", m.compact.hint)
	for range 4 {
		press('=')
	}
	assert.Equal(t, []int{6, 7, 6, 5, 4, 3, 2, 1, 2, 3, 4, 5}, got, "each change re-fetches with the new context")
	assert.Equal(t, "5 lines around each change", m.compact.hint)
}

func TestModel_ChangeCompactContext_WholeFilesUnchanged(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.compact.applicable = true
	m.modes.compactContext = 5
	m.file.name = "a.go"

	result, cmd := m.Update(altKey('='))
	m = result.(Model)
	assert.Nil(t, cmd)
	assert.Equal(t, 5, m.CompactContext())
	assert.NotEmpty(t, m.compact.hint)
}
