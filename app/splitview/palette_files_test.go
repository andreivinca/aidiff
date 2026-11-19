package splitview

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFilesHarness is a split whose palette searches files with files, the
// queries it was asked for recorded in asked.
func newFilesHarness(t *testing.T, asked *[]string, files func(query string) []PickerItem) *harness {
	t.Helper()
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Commands = func() []Command { return []Command{{Name: "Diff View…"}} }
		cfg.Files = func(_ tea.Model, query string) tea.Cmd {
			*asked = append(*asked, query)
			items := files(query)
			return func() tea.Msg {
				return FilesMsg{Query: query, Items: items, OnPick: func(it PickerItem) tea.Cmd { return ran("open " + it.Label) }}
			}
		}
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	return hs
}

// answer delivers what cmd, a Files search, found.
func (hs *harness) answer(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	require.NotNil(t, cmd, "a search was started")
	hs.send(cmd())
}

func TestSplit_PaletteOpensOnCommandsAndSearchesFilesWithoutPrefix(t *testing.T) {
	var asked []string
	hs := newFilesHarness(t, &asked, func(q string) []PickerItem {
		if q == "" {
			return []PickerItem{{Label: "a.go"}, {Label: "b.go", Detail: "src"}}
		}
		return []PickerItem{{Label: "b.go", Detail: "src"}}
	})
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	assert.Equal(t, ">", hs.m.palette.query, "the palette opens on the commands")
	assert.Equal(t, []string{"Open Terminal", "Diff View…", "Quit"}, names(hs.m.palette.rows))
	assert.Contains(t, ansi.Strip(hs.m.View()), " >█")
	hs.key("dif")
	assert.Equal(t, []string{"Diff View…"}, names(hs.m.palette.rows), "the commands filter on what follows '>'")
	assert.Empty(t, asked)

	for range 3 {
		hs.send(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	assert.Equal(t, ">", hs.m.palette.query)
	cmd := hs.send(tea.KeyMsg{Type: tea.KeyBackspace})
	assert.Empty(t, hs.m.palette.query)
	assert.Contains(t, ansi.Strip(hs.m.View()), "searching…")
	hs.answer(t, cmd)
	assert.Equal(t, []string{"a.go", "b.go"}, names(hs.m.palette.rows), "deleting '>' searches files")
	assert.Contains(t, ansi.Strip(hs.m.View()), "src", "a file's folder is its detail")

	stale := hs.key("b")
	cmd = hs.key("o")
	hs.answer(t, cmd)
	assert.Equal(t, []string{"b.go"}, names(hs.m.palette.rows))
	hs.answer(t, stale)
	assert.Equal(t, []string{"b.go"}, names(hs.m.palette.rows), "an answer for an older query is dropped")
	assert.Equal(t, []string{"", "b", "bo"}, asked)

	cmd = hs.send(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	assert.Equal(t, ranMsg{"open b.go"}, cmd(), "enter opens the file")
	assert.False(t, hs.m.palette.open)

	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	hs.send(tea.KeyMsg{Type: tea.KeyBackspace})
	assert.Nil(t, hs.key(">"), "typing '>' again asks for no files")
	assert.Equal(t, []string{"Open Terminal", "Diff View…", "Quit"}, names(hs.m.palette.rows), "and lists the commands")
}

func TestSplit_PaletteFileNoteAndEmptyText(t *testing.T) {
	var asked []string
	hs := newFilesHarness(t, &asked, func(string) []PickerItem { return nil })
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	hs.answer(t, hs.send(tea.KeyMsg{Type: tea.KeyBackspace}))
	assert.Contains(t, ansi.Strip(hs.m.View()), "type a file name")
	hs.answer(t, hs.key("x"))
	assert.Contains(t, ansi.Strip(hs.m.View()), "no matching files")

	hs.send(FilesMsg{Query: "x", Items: []PickerItem{{Label: "x.go"}}, Note: "the first 1 matches"})
	view := strings.Split(ansi.Strip(hs.m.View()), "\n")
	row := -1
	for i, l := range view {
		if strings.Contains(l, "x.go") {
			row = i
		}
	}
	require.Positive(t, row)
	assert.Contains(t, view[row+1], "the first 1 matches", "the note is said under the rows")
}

func TestSplit_PaletteWithoutFilesKeepsPlainQuery(t *testing.T) {
	hs := newCommandHarness(t, nil)
	hs.send(tea.KeyMsg{Type: tea.KeyCtrlP})
	assert.Empty(t, hs.m.palette.query, "no '>' when there are no files to search")
	assert.Contains(t, ansi.Strip(hs.m.View()), " > █")
}

func TestSplit_CarryOnReplace(t *testing.T) {
	type carried struct{ from, to string }
	hs := newSplit(func(hs *harness) Config {
		cfg := hs.config()
		cfg.Carry = func(old, next tea.Model) tea.Cmd {
			o, n := old.(innerModel), next.(innerModel)
			return func() tea.Msg { return carried{o.quitKey, n.quitKey} }
		}
		return cfg
	})
	hs.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	got := []tea.Msg{}
	cmd := hs.send(ReplaceInnerMsg{Model: innerModel{got: &got, quitKey: "x"}})
	require.NotNil(t, cmd)
	var msgs []tea.Msg
	var collect func(tea.Cmd)
	collect = func(c tea.Cmd) {
		if c == nil {
			return
		}
		msg := c()
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				collect(c)
			}
			return
		}
		msgs = append(msgs, msg)
	}
	collect(cmd)
	for _, msg := range msgs {
		hs.send(msg)
	}
	assert.Contains(t, got, tea.Msg(carried{"q", "x"}), "the carried work reaches the new model")
}
