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

// copyAnnotationsCommand is the palette row that copies the review's
// annotations, in the output format, to the clipboard, to paste into a
// terminal pane's agent or anywhere else.
func copyAnnotationsCommand(cb clipboardWriter) splitview.Command {
	return splitview.Command{Name: "Copy annotations", Detail: "to the clipboard", Run: func(inner tea.Model) tea.Cmd {
		store := reviewStore(inner)
		n := store.Count()
		if n == 0 {
			return flashCmd("no annotations to copy")
		}
		text := store.FormatOutput()
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), copyTimeout)
			defer cancel()
			if err := cb.Write(ctx, text); err != nil {
				return splitview.FlashMsg{Text: "copy annotations: " + err.Error()}
			}
			return splitview.FlashMsg{Text: fmt.Sprintf("%d annotation(s) copied", n)}
		}
	}}
}

// flashCmd shows text on the bottom row.
func flashCmd(text string) tea.Cmd {
	return func() tea.Msg { return splitview.FlashMsg{Text: text} }
}

// systemClipboard is this machine's clipboard.
var systemClipboard clipboardWriter = clipboard.System{}
