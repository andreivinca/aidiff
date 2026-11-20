package overlay

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/ui/style"
)

// The help popup's search row: / starts typing, the sections narrow to the
// bindings whose keys or description hold the query (a section whose title
// holds it keeps every binding), Enter keeps the filter and gives the keys
// back to scrolling, Esc clears it. While nothing is typed, j / k / g / ?
// work as before.

// helpSearchRows are the rows the search takes above the bindings: the
// search line and a blank one.
const helpSearchRows = 2

// searchFits reports whether a terminal termHeight rows tall leaves the
// search row room above at least one binding; a shorter one shows the
// bindings alone, as before.
func searchFits(termHeight int) bool {
	return termHeight-helpHeightMargin-helpPopupChromeLines > helpSearchRows
}

// sections are the spec's sections the query keeps.
func (h *helpOverlay) sections() []HelpSection {
	q := strings.ToLower(strings.TrimSpace(h.query))
	if q == "" {
		return h.spec.Sections
	}
	var out []HelpSection
	for _, sec := range h.spec.Sections {
		if strings.Contains(strings.ToLower(sec.Title), q) {
			out = append(out, sec)
			continue
		}
		var kept []HelpEntry
		for _, e := range sec.Entries {
			if strings.Contains(strings.ToLower(e.Keys), q) || strings.Contains(strings.ToLower(e.Description), q) {
				kept = append(kept, e)
			}
		}
		if len(kept) > 0 {
			out = append(out, HelpSection{Title: sec.Title, Entries: kept})
		}
	}
	return out
}

// searchLine is the search row, width cells wide.
func (h *helpOverlay) searchLine(width int, resolver Resolver) string {
	muted := string(resolver.Color(style.ColorKeyMutedFg))
	var line string
	switch {
	case h.typing:
		line = "/ " + h.query + "█"
	case h.query != "":
		line = "/ " + h.query + muted + "  esc clears" + string(style.ResetFg)
	default:
		line = muted + "/ search" + string(style.ResetFg)
	}
	if lipgloss.Width(line) > width {
		line = ansi.Truncate(line, width, helpTruncTail)
	}
	return h.padLine(line, width)
}

// handleSearchKey takes a key while the query is being typed.
func (h *helpOverlay) handleSearchKey(msg tea.KeyMsg) Outcome {
	switch msg.Type {
	case tea.KeyEsc:
		h.query, h.typing = "", false
	case tea.KeyEnter:
		h.typing = false
	case tea.KeyBackspace:
		if r := []rune(h.query); len(r) > 0 {
			h.query = string(r[:len(r)-1])
		}
	case tea.KeySpace:
		h.query += " "
	case tea.KeyUp:
		h.offset = max(h.offset-1, 0)
	case tea.KeyDown:
		h.offset++
	case tea.KeyRunes:
		if msg.Alt {
			break
		}
		for _, r := range msg.Runes {
			if !unicode.IsPrint(r) {
				return Outcome{Kind: OutcomeNone}
			}
		}
		h.query += string(msg.Runes)
	default:
	}
	if msg.Type != tea.KeyUp && msg.Type != tea.KeyDown {
		h.offset = 0 // a new query shows its first match
	}
	return Outcome{Kind: OutcomeNone}
}
