package overlay

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/ui/style"
)

// aidiff's annotation list ends with a row of tools acting on every
// annotation: c copies them, D deletes them (the review asks to confirm),
// each when the spec offers it. A click on a tool does the same.

// toolsRows is what the tools add under the list: a blank row, then theirs.
const toolsRows = 2

// annotTool is one tool of the row.
type annotTool struct {
	key, label string
	kind       OutcomeKind
}

// tools are the tools on offer, none while the list is empty.
func (a *annotListOverlay) tools() []annotTool {
	if len(a.items) == 0 {
		return nil
	}
	var out []annotTool
	if a.canCopy {
		out = append(out, annotTool{key: "c", label: "copy all", kind: OutcomeAnnotCopyAll})
	}
	if a.canDelete {
		out = append(out, annotTool{key: "D", label: "delete all", kind: OutcomeAnnotDeleteAll})
	}
	return out
}

// toolsHeight is the rows the tools take under the list.
func (a *annotListOverlay) toolsHeight() int {
	if len(a.tools()) == 0 {
		return 0
	}
	return toolsRows
}

// toolsGap separates two tools on the row.
const toolsGap = "   "

// toolText is how tool t reads on the row.
func (t annotTool) text() string {
	return t.key + " " + t.label
}

// toolsFooter is the tools row with the blank row above it, "" for none.
func (a *annotListOverlay) toolsFooter(width int, resolver Resolver) string {
	tools := a.tools()
	if len(tools) == 0 {
		return ""
	}
	accent := string(resolver.Color(style.ColorKeyAccentFg))
	muted := string(resolver.Color(style.ColorKeyMutedFg))
	parts := make([]string, 0, len(tools))
	for _, t := range tools {
		parts = append(parts, accent+t.key+string(style.ResetFg)+" "+muted+t.label+string(style.ResetFg))
	}
	return "\n\n" + ansi.Truncate("  "+strings.Join(parts, toolsGap), width, "…")
}

// toolKey is the outcome of a tool's key, ok false for any other key.
func (a *annotListOverlay) toolKey(msg tea.KeyMsg) (Outcome, bool) {
	for _, t := range a.tools() {
		if msg.String() == t.key {
			return Outcome{Kind: t.kind}, true
		}
	}
	return Outcome{}, false
}

// toolClick is the outcome of a click at column x of the content, row y
// counted from the row under the list, ok false off the tools.
func (a *annotListOverlay) toolClick(x, y int) (Outcome, bool) {
	if y != toolsRows-1 {
		return Outcome{}, false
	}
	at := 2 // the row's indent
	for _, t := range a.tools() {
		w := len(t.text())
		if x >= at && x < at+w {
			return Outcome{Kind: t.kind}, true
		}
		at += w + len(toolsGap)
	}
	return Outcome{}, false
}
