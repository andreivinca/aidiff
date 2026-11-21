package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	m.identity = newMachineIdentity("workstation", "", false)
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
	m.identity = newMachineIdentity("workstation", "", true)
	for _, width := range []int{8, 30, 60} {
		m.layout.width = width
		bar := m.statusBarWithMachine()
		assert.LessOrEqual(t, ansi.StringWidth(bar), width-statusBarPadding, "width %d", width)
	}
	m.layout.width = 120
	assert.Equal(t, 120, m.layout.width, "fitting the bar leaves the model's width alone")
}

func TestMachineIdentity_Project(t *testing.T) {
	assert.Equal(t, "box | Sabra2 | T3", newMachineIdentity("box", "Sabra2", true).label(" | "), "the project follows the host")
	assert.Equal(t, "box | aidiff", newMachineIdentity("box", "aidiff", false).label(" | "))
	assert.Equal(t, "aidiff", newMachineIdentity("", "aidiff", false).label(" | "))
	assert.NotContains(t, newMachineIdentity("box", "pro\x1b[31mject", false).label(" | "), "\x1b")
}

func TestNewMachineIdentity_Sanitizes(t *testing.T) {
	id := newMachineIdentity("host\x1b[31m\nname", "", false)
	assert.NotContains(t, id.label(" | "), "\x1b")
	assert.NotContains(t, id.label(" | "), "\n")
	assert.Equal(t, "T3", newMachineIdentity("", "", true).label(" | "))
	assert.Empty(t, newMachineIdentity("", "", false).label(" | "))
}

func TestModel_StatusLineCarriesPaneHints(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.identity = newMachineIdentity("workstation", "", false)
	m.layout.width = 60 // the review's own column; the bar takes the width it is given
	hints := [][2]string{{"enter", "send"}, {"esc", "stop…"}, {"ctrl+p", "commands"}}

	line := ansi.Strip(m.StatusLine(140, hints))
	assert.Equal(t, 140, ansi.StringWidth(line), "the bar fills the width it is given")
	assert.True(t, strings.HasPrefix(line, " workstation"), "the review's status comes first: %q", line)
	assert.True(t, strings.HasSuffix(strings.TrimRight(line, " "), "enter send  esc stop…  ctrl+p commands"), "the hints sit on the right: %q", line)
	assert.Contains(t, line, "? help", "with room, the review's own segments stay")

	narrow := ansi.Strip(m.StatusLine(70, hints))
	assert.Equal(t, 70, ansi.StringWidth(narrow))
	assert.Contains(t, narrow, "enter send", "the main hint stays")
	assert.NotContains(t, narrow, "ctrl+p commands", "hints that do not fit are left out from the last")
	assert.True(t, strings.HasPrefix(narrow, " workstation"), "and the review keeps its own room")

	assert.Equal(t, ansi.Strip(m.StatusLine(140, nil)), ansi.Strip(m.StatusLine(140, [][2]string{})), "no hints: the review's bar alone")
	assert.Equal(t, 60, m.layout.width, "rendering leaves the model's width alone")
}

func TestModel_MachineIdentityKeepsProjectAndBranch(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.identity = newMachineIdentity("boxdev", "aidiff", true, "main")
	updated, _ := m.Update(MachineIdentityMsg{Name: "boxdev", T3: true})
	m = updated.(Model)
	assert.Equal(t, "boxdev | aidiff | main | T3", m.identity.label(" | "), "a connection names the machine, not the review's project")

	updated, _ = m.Update(MachineIdentityMsg{Name: "other", Project: "Sabra2", Branch: "dev", T3: true})
	m = updated.(Model)
	assert.Equal(t, "other | Sabra2 | dev | T3", m.identity.label(" | "), "named ones replace them")
}

func TestModel_BranchFollowsReloads(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.layout.width = 120
	m.identity = newMachineIdentity("workstation", "aidiff", false, "main")
	assert.Nil(t, m.loadBranchCmd(false), "no loader: the branch is fixed")
	assert.Nil(t, m.pollBranch())

	branch, fail := "feature", false
	m.loadBranch = func() (string, error) {
		if fail {
			return "", errors.New("connection lost")
		}
		return branch, nil
	}
	assert.Nil(t, m.pollBranch(), "a local review is not polled: the watcher's reloads re-read the branch")

	updated, cmd := m.Update(m.loadBranchCmd(false)())
	m = updated.(Model)
	assert.Nil(t, cmd, "a reload's read schedules nothing")
	assert.Contains(t, ansi.Strip(m.statusBarWithMachine()), "feature")
	assert.NotContains(t, ansi.Strip(m.statusBarWithMachine()), "main")

	fail = true
	updated, _ = m.Update(m.loadBranchCmd(false)())
	m = updated.(Model)
	assert.Equal(t, "feature", m.identity.branch, "a failed read keeps the branch shown")

	fail, branch = false, ""
	updated, _ = m.Update(m.loadBranchCmd(false)())
	m = updated.(Model)
	assert.Empty(t, m.identity.branch, "a detached HEAD shows no branch")
}

func TestModel_ReloadRereadsBranch(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.loadBranch = func() (string, error) { return "dev", nil }
	cmd := m.triggerReload()
	var got []tea.Msg
	for _, c := range cmd().(tea.BatchMsg) {
		if c != nil {
			if msg, ok := c().(branchLoadedMsg); ok {
				got = append(got, msg)
			}
		}
	}
	assert.Equal(t, []tea.Msg{branchLoadedMsg{branch: "dev"}}, got)
}

func TestModel_T3PollsBranch(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.identity = newMachineIdentity("box", "Sabra2", true, "main")
	m.loadBranch = func() (string, error) { return "hotfix", nil }
	require.NotNil(t, m.pollBranch(), "a T3 checkout reaches no watcher here: the branch is polled")

	updated, cmd := m.Update(branchLoadedMsg{branch: "hotfix", poll: true})
	m = updated.(Model)
	assert.Equal(t, "hotfix", m.identity.branch)
	assert.NotNil(t, cmd, "the poll's read schedules the next one")
	_, cmd = m.Update(branchLoadedMsg{err: errors.New("down"), poll: true})
	assert.NotNil(t, cmd, "a failed read keeps polling")
}

func TestModel_T3PollSurvivesSwitchingToLocal(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.layout.width = 120
	m.identity = newMachineIdentity("box", "Sabra2", true, "main")
	m.loadBranch = func() (string, error) { return "hotfix", nil }

	updated, _ := m.Update(MachineIdentityMsg{Name: "workstation"}) // the palette switched to local
	m = updated.(Model)
	label := ansi.Strip(m.statusBarWithMachine())
	assert.True(t, strings.HasPrefix(label, "workstation"), "the status bar names the live connection")
	assert.NotContains(t, label, "T3")

	updated, cmd := m.Update(branchLoadedMsg{branch: "hotfix", poll: true})
	m = updated.(Model)
	assert.NotNil(t, cmd, "the T3 review's branch is still polled")
	assert.Equal(t, "hotfix", m.identity.branch)
}
