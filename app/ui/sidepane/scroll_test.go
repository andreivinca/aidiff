package sidepane

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/ui/style"
)

func TestFileTree_ScrollByKeepsSelectionUntilEnsureVisible(t *testing.T) {
	entries := make([]diff.FileEntry, 30)
	for i := range entries {
		entries[i] = diff.FileEntry{Path: fmt.Sprintf("f%02d.go", i)}
	}
	ft := NewFileTree(entries)
	selected := ft.SelectedFile()
	res := style.PlainResolver()
	render := FileTreeRender{Width: 30, Height: 10, Resolver: res, Renderer: style.NewRenderer(res)}

	ft.ScrollBy(5, 10)
	ft.Render(render)
	assert.Equal(t, 5, ft.ScrollState().Offset, "rendering keeps the scrolled window")
	assert.Equal(t, selected, ft.SelectedFile())

	ft.ScrollBy(100, 10)
	assert.Equal(t, 21, ft.ScrollState().Offset, "clamped to the last full window (31 rows with the ./ header)")
	ft.ScrollBy(-100, 10)
	assert.Equal(t, 0, ft.ScrollState().Offset)

	ft.ScrollBy(5, 10)
	ft.EnsureVisible(10)
	ft.Render(render)
	assert.Equal(t, 1, ft.ScrollState().Offset, "EnsureVisible brings the cursor (below the ./ header) back")
}
