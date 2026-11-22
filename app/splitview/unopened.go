package splitview

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A tab can stand for a workspace that is not open yet (Config.Tabs), such
// as one an earlier run left open, which the program opens only once the
// user shows it, so a start with many tabs costs no more than one with a
// single tab. Such a workspace is parked with a stand-in for its wrapped
// model, so the tab strip, the switcher, alt+N and closing treat it as any
// other workspace. It is never shown as it was parked: Show reports it not
// ready, so the switcher passes it by and a tab key asks Sessions.Open,
// and the first SwitchWorkspaceMsg with a Model for it opens it in place,
// its tab kept where it was (Created, as for a workspace not seen before).
// Closing its tab asks nothing, as there is nothing in it, and DropTabMsg
// closes it when what it stood for is gone. The stand-in comes on screen
// when the run starts on such a workspace (Config.Opening), or when the tab
// on screen closes and no opened tab is left to take the screen; that
// workspace then opens from there, the stand-in saying how it goes (the
// tab's note, such as connecting…) until it is in its place.

// DropTabMsg closes the tab of workspace Key, asking nothing, when that
// workspace was never opened (Config.Tabs): what it stood for cannot come
// back, such as a folder deleted since. Any other tab is left alone.
type DropTabMsg struct {
	Key string
}

func (DropTabMsg) splitMsg() {}

// unopened stands in for the wrapped model of a workspace not opened yet.
type unopened struct {
	describe func() SessionInfo // the workspace as its tab shows it, fetching nothing
	open     func() tea.Cmd     // opens it again (Sessions.Open): enter, after it failed
	gone     bool               // it cannot be opened, though it is the last tab: it closes once left
	w, h     int
}

func (u unopened) Init() tea.Cmd {
	return nil
}

func (u unopened) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		u.w, u.h = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.Type == tea.KeyEnter && !u.gone && u.open != nil {
			return u, u.open()
		}
	}
	return u, nil
}

// View says, in the middle of its column, which workspace it stands for and
// how its opening goes: its tab's note (connecting…, not connected), else
// that it opens.
func (u unopened) View() string {
	info := SessionInfo{Name: "this tab"}
	if u.describe != nil {
		info = u.describe()
	}
	title := info.Name
	if info.Machine != "" {
		title += " · " + info.Machine
	}
	lines := []string{title, ""}
	switch {
	case u.gone:
		lines = append(lines, "it cannot be opened: open a folder or a project to go on")
	case info.Note != "":
		lines = append(lines, info.Note, "", "enter, or its tab, tries again")
	default:
		lines = append(lines, "opening…", "", "enter, or its tab, tries again")
	}
	return lipgloss.Place(u.w, u.h, lipgloss.Center, lipgloss.Center, strings.Join(lines, "\n"))
}

// isGone reports whether model stands in for a workspace that cannot be
// opened (dropTab).
func isGone(model tea.Model) bool {
	u, ok := model.(unopened)
	return ok && u.gone
}

// isUnopened reports whether model stands in for a workspace not opened yet.
func isUnopened(model tea.Model) bool {
	_, ok := model.(unopened)
	return ok
}

// withTabs gives the split the tabs of cfg.Tabs, in order, its workspace
// among them, the others parked not opened yet, and the order of use
// cfg.Recent starts the switcher with; with cfg.Opening, its workspace is
// not opened yet either. They need Sessions, which alone can open them.
func (m Model) withTabs(cfg Config) Model {
	if cfg.Sessions == nil {
		return m
	}
	if cfg.Opening && cfg.Workspace != "" {
		m.inner = m.standIn(cfg.Workspace)
	}
	if len(cfg.Tabs) == 0 {
		return m
	}
	var tabs []string
	for _, k := range cfg.Tabs {
		tabs = opened(tabs, k)
	}
	m.tabs = opened(tabs, cfg.Workspace)
	m.parked = map[string]workspace{}
	for _, k := range m.tabs {
		if k == cfg.Workspace {
			continue
		}
		m.parked[k] = workspace{id: nextWorkspaceID(), inner: m.standIn(k), focus: focusDiff, termRatio: m.termRatio}
	}
	for _, k := range cfg.Recent {
		if _, ok := m.parked[k]; ok && !slices.Contains(m.mru, k) {
			m.mru = append(m.mru, k)
		}
	}
	return m
}

// standIn is the stand-in of workspace key, not opened yet.
func (m Model) standIn(key string) unopened {
	sessions := m.cfg.Sessions
	return unopened{
		describe: func() SessionInfo { return m.sessionInfo(key, false) },
		open:     func() tea.Cmd { return sessions.Open(key) },
	}
}

// Tabs are the keys of the tabs in their order, the workspaces not opened
// yet included: what the program keeps for its next run.
func (m Model) Tabs() []string {
	return m.tabKeys()
}

// openInPlace opens the workspace on screen, not opened yet, with msg's
// Model, as a new workspace (Created): its stand-in held nothing to retire
// or carry over.
func (m Model) openInPlace(msg SwitchWorkspaceMsg) (tea.Model, tea.Cmd) {
	m.inner = msg.Model
	m.wsID = nextWorkspaceID()
	m.dropLeftCache()
	m.innerSized = false
	m.refreshPending = false
	m.flash = ""
	if msg.Done != nil {
		msg.Done(WorkspaceSwitch{Created: true})
	}
	initCmd := m.tag(m.inner.Init())
	model, sizeCmd := m.relayout()
	return model, tea.Batch(initCmd, sizeCmd)
}

// dropTab closes workspace key's tab when it was never opened (DropTabMsg).
// The last tab stays, as ever: on screen, its stand-in says it cannot be
// opened, and it closes once another workspace comes (switchAway).
func (m Model) dropTab(key string) (tea.Model, tea.Cmd) {
	if !m.hasWorkspace(key) || !isUnopened(m.workspaceModel(key)) {
		return m, nil
	}
	if key == m.wsKey && len(m.tabKeys()) < 2 {
		u, _ := m.inner.(unopened)
		u.gone = true
		m.inner = u
		m.dropLeftCache()
		return m, nil
	}
	return m.closeTab(key)
}
