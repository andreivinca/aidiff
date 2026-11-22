package splitview

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
)

// The tab strip is a row across the top of the window, above the sidebars,
// the wrapped model and the panes: one tab per open named workspace, in the
// order they were opened. Opening a workspace by any route appends its tab,
// or goes to its tab when it is open already, and switching never reorders
// them, so alt+N (ctrl+N where the terminal reports it) always means the
// same workspace. Each tab names its workspace and machine (Sessions) and
// has a close button; the strip shows only while two or more tabs are
// open, so a single-workspace run keeps its row. The session switcher keeps
// its own order, the most recently used first, over the same workspaces.
// The program may start the split with tabs of workspaces it opens only
// once they are shown (Config.Tabs, unopened.go), such as the ones an
// earlier run left open.
//
// The unnamed workspace has no tab: it closes once left, and in practice it
// is on screen only while it is the run's only workspace.

// minTabsHeight is the window height below which the strip gives its row
// back: the wrapped model then keeps at least minTabsHeight-1 rows, room for
// a review's own toolbar (which needs 8).
const minTabsHeight = 10

// lastTab is flashed when the user asks to close the only tab: something
// must stay on screen.
const lastTab = "the last tab stays open: there would be nothing to show"

// closeTabMsg closes tab key once the user confirmed it.
type closeTabMsg struct {
	key string
}

// opened is tabs with key appended when it is new, as a new slice; the
// unnamed workspace gets no tab.
func opened(tabs []string, key string) []string {
	if key == "" || slices.Contains(tabs, key) {
		return tabs
	}
	return append(slices.Clip(tabs), key)
}

// without is keys without key, as a new slice.
func without(keys []string, key string) []string {
	return slices.DeleteFunc(slices.Clone(keys), func(k string) bool { return k == key })
}

// tabKeys are the tabs: the open named workspaces in the order they were
// opened.
func (m Model) tabKeys() []string {
	keys := make([]string, 0, len(m.tabs)+1)
	for _, k := range m.tabs {
		if k != "" && m.hasWorkspace(k) && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	var rest []string // open ones the order does not know: never expected, listed last
	if m.wsKey != "" && !slices.Contains(keys, m.wsKey) {
		rest = append(rest, m.wsKey)
	}
	for k := range m.parked {
		if k != "" && !slices.Contains(keys, k) && !slices.Contains(rest, k) {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	return append(keys, rest...)
}

// tabRowsFor is how many rows the strip takes in the window as it is: one
// while two or more tabs are open and the window is tall enough.
func (m Model) tabRowsFor() int {
	if m.height >= minTabsHeight && len(m.tabKeys()) >= 2 {
		return 1
	}
	return 0
}

// paneH is the height the wrapped model gets: the window's, less the strip.
// m.top follows the strip at every relayout, the only place the open
// workspaces or the window height change.
func (m Model) paneH() int {
	return m.height - m.top
}

// selectTab is alt+N or ctrl+N: tab n, counted from 1, comes on screen.
func (m Model) selectTab(n int) (tea.Model, tea.Cmd) {
	keys := m.tabKeys()
	if n < 1 || n > len(keys) {
		m.flash = "there is no tab " + strconv.Itoa(n)
		return m, nil
	}
	return m.showTab(keys[n-1])
}

// showTab puts tab key on screen as the switcher's commit does: as it was
// parked when it can come so, else through Sessions.Open (a dropped or
// replaced connection, a workspace not opened yet). The tab on screen while
// it is not opened yet tries to open again. A sidebar, left for the tab,
// hides.
func (m Model) showTab(key string) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if key != m.wsKey {
		done, ready := m.showSession(key)
		if ready {
			model, cmd := m.switchWorkspace(SwitchWorkspaceMsg{Key: key, Done: done})
			m, cmds = model.(Model), append(cmds, cmd)
		} else {
			cmds = append(cmds, m.scoped(m.cfg.Sessions.Open(key)))
		}
	} else if isUnopened(m.inner) && !isGone(m.inner) {
		cmds = append(cmds, m.scoped(m.cfg.Sessions.Open(key)))
	}
	model, cmd := m.hideSidebar()
	return model, tea.Batch(append(cmds, cmd)...)
}

// requestCloseTab asks to close tab key, every time, saying what closing
// it would lose (closeDetails); the last tab stays. A workspace not opened
// yet has nothing to lose and closes at once.
func (m Model) requestCloseTab(key string) (tea.Model, tea.Cmd) {
	keys := m.tabKeys()
	switch {
	case !slices.Contains(keys, key):
		m.flash = "this review has no tab to close"
		return m, nil
	case len(keys) < 2:
		m.flash = lastTab
		return m, nil
	case isUnopened(m.workspaceModel(key)):
		return m.closeTab(key)
	}
	details := m.closeDetails(key)
	if len(details) == 0 {
		details = []string{"Opening it again starts a new tab."} // asked anyway: alt+w is one key away from others
	}
	yes := func() tea.Msg { return closeTabMsg{key: key} }
	return m.showConfirm(ConfirmMsg{
		Title:   "Close " + m.describeSession(key).Name + "?",
		Detail:  strings.Join(details, " "),
		Confirm: "Close",
		Yes:     yes,
	})
}

// closeDetails says what closing workspace key would lose, nothing when
// it would lose nothing: its terminals, the chat of an agent at work, and
// what the program says of its model (Sessions.CloseNote).
func (m Model) closeDetails(key string) []string {
	var details []string
	switch n := m.shells(key); {
	case n == 1:
		details = append(details, "Closes its terminal and everything running in it.")
	case n > 1:
		details = append(details, "Closes its "+strconv.Itoa(n)+" terminals and everything running in them.")
	}
	if m.chats(key) > 0 && m.describeSession(key).Busy {
		details = append(details, "Its agent is working; closing its chat does not stop it.")
	}
	if m.cfg.Sessions != nil {
		if note := cleanLine(m.cfg.Sessions.CloseNote(key, m.workspaceModel(key))); strings.TrimSpace(note) != "" {
			details = append(details, note)
		}
	}
	return details
}

// chats counts workspace key's open panes that are not shells, such as a
// chat: the panes with a quit note of their own.
func (m Model) chats(key string) int {
	n := 0
	for _, t := range m.workspaceTerms(key) {
		if _, chat := t.(quitNoter); chat && !t.Exited() {
			n++
		}
	}
	return n
}

// workspaceTerms are workspace key's panes, on screen or parked.
func (m Model) workspaceTerms(key string) []Terminal {
	if key == m.wsKey {
		return m.terms
	}
	return m.parked[key].terms
}

// workspaceModel is workspace key's wrapped model, nil when it is not open.
func (m Model) workspaceModel(key string) tea.Model {
	if key == m.wsKey {
		return m.inner
	}
	return m.parked[key].inner
}

// closeTab closes tab key with its workspace and panes. The tab on screen
// hands the screen to its neighbor first (tabNeighbor); one not opened yet
// opens from there (unopened.go). The program hears of the close
// (Sessions.Closed) once the tab is off screen.
func (m Model) closeTab(key string) (tea.Model, tea.Cmd) {
	keys := m.tabKeys()
	i := slices.Index(keys, key)
	if i < 0 {
		return m, nil
	}
	if len(keys) < 2 {
		m.flash = lastTab
		return m, nil
	}
	var model tea.Model // the closed workspace's wrapped model
	var cmd tea.Cmd
	if key == m.wsKey {
		next := m.tabNeighbor(keys, i)
		done, _ := m.showSession(next)
		model = m.inner
		var switched tea.Model
		switched, cmd = m.switchAway(SwitchWorkspaceMsg{Key: next, Done: done}, true)
		m = switched.(Model)
		if isUnopened(m.inner) && m.cfg.Sessions != nil { // no opened tab was left to go to
			cmd = tea.Batch(cmd, m.scoped(m.cfg.Sessions.Open(next)))
		}
		m.switcher = switcherState{}
		m.mru = used(m.mru, m.wsKey)
		hidden, hideCmd := m.hideSidebar()
		m, cmd = hidden.(Model), tea.Batch(cmd, hideCmd)
	} else {
		w := m.parked[key]
		for _, t := range w.terms {
			m.closing.Go(t.Close)
		}
		m.parked = maps.Clone(m.parked) // never write into a map older copies share
		delete(m.parked, key)
		model = w.inner
		relaid, layoutCmd := m.relayout() // the strip may go
		m, cmd = relaid.(Model), layoutCmd
	}
	m.tabs, m.mru = without(m.tabs, key), without(m.mru, key)
	if m.cfg.Sessions != nil {
		m.cfg.Sessions.Closed(key, model)
	}
	return m, cmd
}

// tabNeighbor is the tab that takes the screen when tab i, on screen,
// closes: the one to its right, else its left, preferring the nearest that
// can come back as it was parked (Sessions.Show); with none, the nearest
// anyway, which the program then rebuilds or reconnects as a switch to it
// does.
func (m Model) tabNeighbor(keys []string, i int) string {
	var order []string
	for d := 1; d < len(keys); d++ {
		for _, j := range []int{i + d, i - d} {
			if j >= 0 && j < len(keys) {
				order = append(order, keys[j])
			}
		}
	}
	for _, k := range order {
		if _, ready := m.showSession(k); ready {
			return k
		}
	}
	return order[0]
}

// closeTabCommand is the palette's row for closing the tab on screen, while
// there is another tab to go to.
func (m Model) closeTabCommand() (row, bool) {
	if m.wsKey == "" || len(m.tabKeys()) < 2 {
		return row{}, false
	}
	run := func(m Model) (tea.Model, tea.Cmd) {
		return m.requestCloseTab(m.wsKey)
	}
	return row{label: "Close tab", detail: m.sessionInfo(m.wsKey, false).Name + " · " + m.cfg.Keys.CloseTab, run: run}, true
}

// shifted is an alt+letter key with its letter in upper case, as Bubble
// Tea names it with shift held or caps lock on ("alt+w" → "alt+W"), so the
// key works either way; key itself for any other key (a ctrl+letter reads
// the same shifted or not).
func shifted(key string) string {
	letter, ok := strings.CutPrefix(key, "alt+")
	if !ok || len(letter) != 1 || letter[0] < 'a' || letter[0] > 'z' {
		return key
	}
	return "alt+" + strings.ToUpper(letter)
}

// tabsMouse handles a press on the strip: a tab's close button closes it,
// a tab comes on screen, a scroll mark brings the first tab hidden beyond
// it. Other buttons and the gaps do nothing.
func (m Model) tabsMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	z, ok := m.tabStrip().hit(msg.X)
	switch {
	case !ok:
		return m, nil
	case z.close:
		return m.requestCloseTab(z.key)
	}
	return m.showTab(z.key)
}

// numberKey reports whether k is ctrl+1…ctrl+9 (alt+1…alt+9 too), the keys
// that bring a tab by its number, and which.
func numberKey(k uv.KeyPressEvent) (int, bool) {
	if k.Code < '1' || k.Code > '9' || k.Mod&(uv.ModCtrl|uv.ModAlt) == 0 {
		return 0, false
	}
	return int(k.Code - '0'), true
}

// altNumber reports whether msg is alt+1…alt+9, the number keys every
// terminal sends, the Linux console included, and which.
func altNumber(msg tea.KeyMsg) (int, bool) {
	if !msg.Alt || msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return 0, false
	}
	r := msg.Runes[0]
	if r < '1' || r > '9' {
		return 0, false
	}
	return int(r - '0'), true
}
