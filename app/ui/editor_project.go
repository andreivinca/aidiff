package ui

import (
	"errors"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/editor"
)

// aidiff's e / f12: the editor starts in the project's root folder and opens
// the file on screen, at the cursor's line when the row has one, or no file
// at all when there is none to open. Upstream's openSourceEditor refuses
// instead in those cases.

// projectEditor is an editor that can also start in a folder with no file
// (editor.Editor.ProjectCommand). It is optional so that ExternalEditor, and
// its generated mock, stay upstream's.
type projectEditor interface {
	ProjectCommand(dir string) (*exec.Cmd, error)
}

// openProjectEditor opens the editor in the project root, with the file on
// screen when there is one. A review with no local folder (stdin, a T3
// host) and a file whose line annotations upstream protects from edits
// keep upstream's refusal and hint.
func (m *Model) openProjectEditor() tea.Cmd {
	policy := m.cfg.sourceEditorPolicy
	pe, ok := m.editor.(projectEditor)
	if !policy.Available || policy.Root == "" || !ok || m.annotatedFileProtected() {
		return m.openSourceEditor()
	}
	result, err := m.sourceEditorTarget()
	if err != nil {
		noRow := *m // a row with no source line (a divider, a placeholder) still opens the file
		noRow.nav.diffCursor = -1
		result, err = noRow.sourceEditorTarget()
	}
	var cmd *exec.Cmd
	if err == nil {
		cmd, err = m.editor.SourceCommand(result.sourcePath, result.sourceLine)
	}
	if err != nil { // no file on screen, or it is deleted or gone: the project alone
		result = sourceEditorTargetResult{}
		if cmd, err = pe.ProjectCommand(policy.Root); err != nil {
			if errors.Is(err, editor.ErrProjectNotDir) {
				m.editorState.hint = "Editor unavailable: project is not a folder"
				return nil
			}
			return func() tea.Msg { return sourceEditorFinishedMsg{err: err} }
		}
	}
	cmd.Dir = policy.Root
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		return sourceEditorFinishedMsg{err: runErr, fileName: result.fileName, reloadAfterCleanExit: result.reloadAfterCleanExit}
	})
}

// annotatedFileProtected reports whether upstream refuses to open the file on
// screen because edits could orphan its line annotations.
func (m Model) annotatedFileProtected() bool {
	return m.cfg.sourceEditorPolicy.DisallowAnnotatedFileEditing && m.file.name != "" && m.hasCurrentFileLineAnnotations()
}
