package overlay

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/keymap"
	"github.com/umputun/revdiff/app/ui/style"
)

func searchSpec() HelpSpec {
	return HelpSpec{Sections: []HelpSection{
		{Title: "Navigation", Entries: []HelpEntry{{Keys: "j", Description: "down"}, {Keys: "k", Description: "up"}}},
		{Title: "Changes", Entries: []HelpEntry{{Keys: "x", Description: "revert the change under the cursor"},
			{Keys: "X", Description: "revert the whole file"}}},
		{Title: "Annotations", Entries: []HelpEntry{{Keys: "a", Description: "annotate line"}, {Keys: "d", Description: "delete annotation"}}},
	}}
}

func typeKeys(mgr *Manager, s string) {
	for _, r := range s {
		mgr.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, "")
	}
}

func TestHelpSearch_FiltersAsYouType(t *testing.T) {
	ctx := RenderCtx{Width: 120, Height: 40, Resolver: style.PlainResolver()}
	mgr := NewManager()
	mgr.OpenHelp(searchSpec())
	view := ansi.Strip(mgr.help.render(ctx, mgr))
	assert.Contains(t, view, "/ search", "the search row shows how to start")

	out := mgr.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}, keymap.ActionSearch)
	assert.Equal(t, OutcomeNone, out.Kind)
	require.True(t, mgr.help.typing)
	typeKeys(mgr, "revert")
	view = ansi.Strip(mgr.help.render(ctx, mgr))
	assert.Contains(t, view, "/ revert█")
	assert.Contains(t, view, "revert the change under the cursor")
	assert.Contains(t, view, "revert the whole file")
	assert.NotContains(t, view, "annotate line", "other bindings are hidden")
	assert.NotContains(t, view, "Navigation", "and sections with none left")

	typeKeys(mgr, "j") // a key that scrolls when not typing is text while typing
	assert.Equal(t, "revertj", mgr.help.query)
	assert.Contains(t, ansi.Strip(mgr.help.render(ctx, mgr)), "no key matches")
	mgr.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace}, "")
	assert.Equal(t, "revert", mgr.help.query)

	mgr.HandleKey(tea.KeyMsg{Type: tea.KeyEnter}, keymap.ActionConfirm)
	assert.False(t, mgr.help.typing, "enter keeps the filter, the keys scroll again")
	assert.Contains(t, ansi.Strip(mgr.help.render(ctx, mgr)), "esc clears")

	out = mgr.HandleKey(tea.KeyMsg{Type: tea.KeyEsc}, keymap.ActionDismiss)
	assert.Equal(t, OutcomeNone, out.Kind, "esc clears the search before it closes")
	assert.Empty(t, mgr.help.query)
	assert.Contains(t, ansi.Strip(mgr.help.render(ctx, mgr)), "annotate line")
	out = mgr.HandleKey(tea.KeyMsg{Type: tea.KeyEsc}, keymap.ActionDismiss)
	assert.Equal(t, OutcomeClosed, out.Kind)
}

func TestHelpSearch_MatchesKeysAndSectionTitles(t *testing.T) {
	mgr := NewManager()
	mgr.OpenHelp(searchSpec())
	mgr.help.query = "annotations"
	secs := mgr.help.sections()
	require.Len(t, secs, 1)
	assert.Len(t, secs[0].Entries, 2, "a section matched by its title keeps every binding")

	mgr.help.query = "X"
	secs = mgr.help.sections()
	require.Len(t, secs, 1)
	assert.Equal(t, "Changes", secs[0].Title, "keys match too, case aside")
}

func TestHelpSearch_EscWhileTypingClearsAndReopenResets(t *testing.T) {
	mgr := NewManager()
	mgr.OpenHelp(searchSpec())
	mgr.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}, "")
	typeKeys(mgr, "up")
	out := mgr.HandleKey(tea.KeyMsg{Type: tea.KeyEsc}, keymap.ActionDismiss)
	assert.Equal(t, OutcomeNone, out.Kind, "esc while typing stays open")
	assert.Empty(t, mgr.help.query)
	assert.False(t, mgr.help.typing)

	mgr.help.query = "left over"
	mgr.OpenHelp(searchSpec())
	assert.Empty(t, mgr.help.query, "a new help starts unfiltered")
}

func TestHelpSearch_HiddenOnATinyTerminal(t *testing.T) {
	mgr := NewManager()
	mgr.OpenHelp(searchSpec())
	assert.NotContains(t, ansi.Strip(mgr.help.render(RenderCtx{Width: 60, Height: 8, Resolver: style.PlainResolver()}, mgr)), "/ search")
}
