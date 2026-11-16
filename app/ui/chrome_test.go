package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func TestModel_ViewSeparatorOnly(t *testing.T) {
	for _, single := range []bool{false, true} {
		for _, focus := range []pane{paneTree, paneDiff} {
			m := testModel([]string{"a.go", "b.go"}, nil)
			m.tree = testNewFileTree([]string{"a.go", "b.go"})
			m.file.singleFile = single
			m.layout.focus = focus
			m.file.name = "a.go"
			m.layout.viewport.Width = 60
			m.layout.viewport.Height = m.paneHeight() - 1
			m.layout.viewport.SetContent(strings.Repeat("content │ stays intact\n", 200))
			view := ansi.Strip(m.View())
			assert.NotContains(t, view, "┌")
			assert.NotContains(t, view, "└")
			assert.NotContains(t, view, "││")
			assert.NotContains(t, view, scrollbarThumbGlyph, "diff content must not become a scrollbar")
			assert.Contains(t, view, "content │ stays intact")
			first, _, _ := strings.Cut(view, "\n")
			if single {
				assert.NotContains(t, first, "│")
				assert.NotContains(t, view, dividerHandle)
			} else {
				assert.Equal(t, 1, strings.Count(first, "│"))
				rows := strings.Split(view, "\n")
				assert.Contains(t, rows[(m.paneHeight()+2)/2], dividerHandle)
			}
		}
	}
}

func TestReplaceDivider(t *testing.T) {
	tests := []struct {
		name, row, want string
	}{
		{name: "border track", row: " a.go │", want: " a.go " + dividerHandle},
		{name: "scrollbar thumb keeps its styling", row: " a.go " + scrollbarThumbRune, want: " a.go \x1b[1m" + dividerHandle + "\x1b[22m"},
		{name: "styled border", row: " a.go \x1b[38;5;1m│\x1b[0m", want: " a.go \x1b[38;5;1m" + dividerHandle + "\x1b[0m"},
		{name: "content glyph before the border", row: " a │ b │", want: " a │ b " + dividerHandle},
		{name: "no border: content untouched", row: " a │ b", want: " a │ b"},
		{name: "empty", row: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, replaceDivider(tt.row))
		})
	}
}

func TestModel_StatusBarMachineIdentity(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.identity = newMachineIdentity("workstation", false)
	m.layout.width = 120
	local := ansi.Strip(m.statusBarWithMachine())
	assert.True(t, strings.HasPrefix(local, "workstation"))
	assert.NotContains(t, local, "T3")

	updated, _ := m.Update(MachineIdentityMsg{Name: "remote-box", T3: true})
	m = updated.(Model)
	remote := ansi.Strip(m.statusBarWithMachine())
	assert.True(t, strings.HasPrefix(remote, "remote-box"))
	assert.Contains(t, remote, "T3")
	assert.NotContains(t, remote, "workstation")
	assert.Less(t, strings.Index(remote, "remote-box"), strings.Index(remote, "T3"))

	m.identity = machineIdentity{}
	assert.Equal(t, m.statusBarText(), m.statusBarWithMachine(), "no identity: the upstream status bar")
}

func TestModel_StatusBarMachineIdentityFitsTheWidth(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.file.name = strings.Repeat("deep/", 20) + "file.go"
	m.identity = newMachineIdentity("workstation", true)
	for _, width := range []int{8, 30, 60} {
		m.layout.width = width
		bar := m.statusBarWithMachine()
		assert.LessOrEqual(t, ansi.StringWidth(bar), width-statusBarPadding, "width %d", width)
	}
	m.layout.width = 120
	assert.Equal(t, 120, m.layout.width, "fitting the bar leaves the model's width alone")
}

func TestNewMachineIdentity_Sanitizes(t *testing.T) {
	id := newMachineIdentity("host\x1b[31m\nname", false)
	assert.NotContains(t, id.label(" | "), "\x1b")
	assert.NotContains(t, id.label(" | "), "\n")
	assert.Equal(t, "T3", newMachineIdentity("", true).label(" | "))
	assert.Empty(t, newMachineIdentity("", false).label(" | "))
}
