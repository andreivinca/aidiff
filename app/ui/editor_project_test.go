package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/editor"
)

// fakeProjectEditor records what openProjectEditor asked it to open.
type fakeProjectEditor struct {
	project string    // the folder ProjectCommand started in
	last    *exec.Cmd // the command handed out last
}

func (f *fakeProjectEditor) Command(string) (*exec.Cmd, func(error) (string, error), error) {
	return exec.Command("/bin/true"), func(error) (string, error) { return "", nil }, nil
}

func (f *fakeProjectEditor) SourceCommand(path string, line int) (*exec.Cmd, error) {
	cmd, err := editor.Editor{}.SourceCommand(path, line) // the real checks: missing, not regular
	f.last = cmd
	return cmd, err //nolint:wrapcheck // the real editor's error, as it is
}

func (f *fakeProjectEditor) ProjectCommand(dir string) (*exec.Cmd, error) {
	f.project = dir
	f.last = exec.Command("/bin/true")
	return f.last, nil
}

// projectEditorModel is a worktree review of a.go in root, whose editor is fake.
func projectEditorModel(t *testing.T, root string, lines []diff.DiffLine) (Model, *fakeProjectEditor) {
	t.Helper()
	t.Setenv("EDITOR", "true")
	m := testModel([]string{"a.go"}, map[string][]diff.DiffLine{"a.go": lines})
	enableSourceEditor(&m, root, true)
	m.tree = testNewFileTree([]string{"a.go"})
	fe := &fakeProjectEditor{}
	m.editor = fe
	return m, fe
}

// launched opens the editor and reports the folder it starts in and its arguments.
func launched(t *testing.T, m *Model, fe *fakeProjectEditor) (dir string, args []string) {
	t.Helper()
	fe.last = nil
	cmd := m.openProjectEditor()
	require.NotNil(t, cmd, "the editor is launched; hint: %q", m.editorState.hint)
	require.NotNil(t, fe.last)
	return fe.last.Dir, fe.last.Args
}

func TestOpenProjectEditor_NoFileOpensTheProject(t *testing.T) {
	root := t.TempDir()
	m, fe := projectEditorModel(t, root, nil)
	m.file.name = ""
	dir, args := launched(t, &m, fe)
	assert.Equal(t, root, fe.project, "no file on screen: the editor opens the project alone")
	assert.Equal(t, root, dir)
	assert.Equal(t, []string{"/bin/true"}, args)
}

func TestOpenProjectEditor_OpensTheFileFromTheRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("one\ntwo\n"), 0o600))
	lines := []diff.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "one", ChangeType: diff.ChangeContext},
		{Content: "⋯ 1 line ⋯", ChangeType: diff.ChangeDivider},
	}
	m, fe := projectEditorModel(t, root, lines)
	m.file.name, m.file.lines = "a.go", lines
	dir, args := launched(t, &m, fe)
	assert.Empty(t, fe.project)
	assert.Equal(t, root, dir, "the editor starts in the project root")
	assert.Equal(t, []string{"true", filepath.Join(root, "a.go")}, args)

	m.nav.diffCursor = 1 // a divider has no source line: the file opens anyway
	_, args = launched(t, &m, fe)
	assert.Equal(t, []string{"true", filepath.Join(root, "a.go")}, args)

	require.NoError(t, os.Remove(filepath.Join(root, "a.go")))
	_, _ = launched(t, &m, fe)
	assert.Equal(t, root, fe.project, "a file gone from disk: the project alone")
}

func TestOpenProjectEditor_KeepsUpstreamRefusals(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("one\n"), 0o600))
	lines := []diff.DiffLine{{OldNum: 1, NewNum: 1, Content: "one", ChangeType: diff.ChangeContext}}
	m, fe := projectEditorModel(t, root, lines)
	m.file.name, m.file.lines = "a.go", lines
	m.store.Add(annotation.Annotation{File: "a.go", Line: 1, Type: " ", Comment: "keep"})
	assert.Nil(t, m.openProjectEditor())
	assert.Equal(t, "Editor unavailable: file has line annotations", m.editorState.hint)
	assert.Empty(t, fe.project, "edits could orphan the annotations")

	m, _ = projectEditorModel(t, root, lines)
	m.cfg.sourceEditorPolicy = SourceEditorPolicy{}
	assert.Nil(t, m.openProjectEditor(), "no local folder (stdin, a remote T3 review)")
	assert.Equal(t, "Editor unavailable: source editor disabled", m.editorState.hint)
}

func TestOpenProjectEditor_KeyWithNoFile(t *testing.T) {
	root := t.TempDir()
	m, fe := projectEditorModel(t, root, nil)
	m.file.name = ""
	for _, focus := range []pane{paneDiff, paneTree} {
		m.layout.focus = focus
		fe.project = ""
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
		require.NotNil(t, cmd)
		assert.Equal(t, root, fe.project, "e opens the project with focus %v", focus)
	}
}
