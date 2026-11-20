package t3palette

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/splitview"
)

func TestAddProject_BrowsesAndOpensAFolder(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "alpha"), filepath.Join(root, "beta")
	for _, d := range []string{a, filepath.Join(b, "sub"), filepath.Join(root, ".hidden")} {
		require.NoError(t, os.MkdirAll(d, 0o750))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o600))
	f := &localFake{}
	h := newHarness(t, Config{NoChat: true, Local: f.config(a, a)}, nil)

	browse := find[splitview.ShowPickerMsg](t, run(h.p.addProjectCmd().Run(nil)))
	assert.Equal(t, []string{"✓ Open " + filepath.Base(root), "..", "alpha/", "beta/", ".hidden/"}, pickerLabels(browse),
		"starts at the parent of the folder on screen; folders only, hidden ones last")

	into := find[splitview.ShowPickerMsg](t, run(browse.OnPick(browse.Items[3])))
	assert.Equal(t, []string{"✓ Open beta", "..", "sub/"}, pickerLabels(into))
	typed := find[splitview.ShowPickerMsg](t, run(into.OnText("sub")))
	assert.Equal(t, []string{"✓ Open sub", ".."}, pickerLabels(typed), "a typed path is relative to the folder listed")
	assert.Equal(t, []string{"Add Project: " + filepath.Join(b, "nope") + " is not a folder"}, flashes(run(into.OnText("nope"))))

	msgs := run(into.OnPick(into.Items[0]))
	sw := find[splitview.SwitchWorkspaceMsg](t, msgs)
	assert.Equal(t, localKey(b), sw.Key)
	assert.Equal(t, []string{b}, f.opened)
	assert.Equal(t, []string{a, b}, h.p.historyDirs(), "added at the end: the numbers stay put")
	h.settle()
}

func pickerLabels(m splitview.ShowPickerMsg) []string {
	out := make([]string, 0, len(m.Items))
	for _, it := range m.Items {
		out = append(out, it.Label)
	}
	return out
}
