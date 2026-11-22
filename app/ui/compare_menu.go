package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The Compare dropdown lists what the review can compare. The choices come
// from git or from the T3 server, which takes a moment, so the dropdown
// opens at once saying it loads and fills in when they land
// (CompareMenuMsg).

// CompareItem is one choice of the Compare dropdown.
type CompareItem struct {
	Label   string
	Detail  string         // dim, on the right: what it compares against
	Current bool           // what the review on screen compares
	Run     func() tea.Cmd // switches the review to it
}

// CompareMenuMsg is what ModelConfig.Compare's command ends in: the choices
// of the Compare dropdown, or why there are none.
type CompareMenuMsg struct {
	Items []CompareItem
	Err   string
}

// compareChoices is the Compare dropdown's content.
type compareChoices struct {
	loading bool
	items   []CompareItem
	err     string
}

// compareDetailWidth caps a choice's detail, so the dropdown stays narrower
// than the window.
const compareDetailWidth = 40

// loadCompareMenu asks for the Compare dropdown's choices as it opens; until
// they land it says it loads.
func (m *Model) loadCompareMenu() tea.Cmd {
	m.toolbar.choices = compareChoices{loading: true}
	return m.toolbar.compare()
}

// handleCompareMenu takes the dropdown's choices, and highlights what the
// review compares now.
func (m Model) handleCompareMenu(msg CompareMenuMsg) (tea.Model, tea.Cmd) {
	m.toolbar.choices = compareChoices{items: msg.Items, err: msg.Err}
	if m.toolbar.open && m.compareOpen() {
		m.toolbar.item = -1
		for i, it := range msg.Items {
			if it.Current {
				m.toolbar.item = i
			}
		}
		if m.toolbar.item < 0 {
			m.moveMenuItem(1)
		}
	}
	return m, nil
}

// compareOpen reports whether the open dropdown is Compare's.
func (m Model) compareOpen() bool {
	buttons := m.toolbarButtons()
	return m.toolbar.button < len(buttons) && buttons[m.toolbar.button].load != nil
}

// compareMenu is the Compare dropdown: what the review can compare, the
// current choice ticked.
func (m Model) compareMenu() []tbItem {
	c := m.toolbar.choices
	switch {
	case c.loading:
		return []tbItem{{label: "loading…", disabled: true}}
	case c.err != "":
		return []tbItem{{label: ansi.Truncate(c.err, 60, "…"), disabled: true}}
	case len(c.items) == 0:
		return []tbItem{{label: "nothing to compare", disabled: true}}
	}
	items := make([]tbItem, 0, len(c.items))
	for _, it := range c.items {
		run := it.Run
		items = append(items, tbItem{label: it.Label, key: ansi.Truncate(it.Detail, compareDetailWidth, "…"), checked: it.Current,
			run: func(m Model) (tea.Model, tea.Cmd) {
				if run == nil {
					return m, nil
				}
				return m, run()
			}})
	}
	return items
}
