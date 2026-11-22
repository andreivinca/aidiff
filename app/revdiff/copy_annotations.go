package main

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/clipboard"
	"github.com/umputun/revdiff/app/splitview"
)

// copyTimeout bounds one clipboard write.
const copyTimeout = 5 * time.Second

// clipboardWriter is where Copy annotations puts the annotations.
type clipboardWriter interface {
	Write(ctx context.Context, text string) error
}

// copyAnnotations puts text, n annotations, on cb and says so on the bottom
// row: the toolbar's ⋯ menu Copy annotations and the @ list's copy-all tool
// (ui.ModelConfig.CopyAnnotations).
func copyAnnotations(cb clipboardWriter) func(text string, n int) tea.Cmd {
	return func(text string, n int) tea.Cmd {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), copyTimeout)
			defer cancel()
			if err := cb.Write(ctx, text); err != nil {
				return splitview.FlashMsg{Text: "copy annotations: " + err.Error()}
			}
			return splitview.FlashMsg{Text: fmt.Sprintf("%d annotation(s) copied", n)}
		}
	}
}

// systemClipboard is this machine's clipboard.
var systemClipboard clipboardWriter = clipboard.System{}
