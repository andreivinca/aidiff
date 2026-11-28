package t3palette

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// The history panel is the sidebar while this machine is the selected
// connection: the local folders aidiff reviewed, most recently opened first
// as of the run's start, so the numbers stay put while it runs. Each folder
// is a split workspace of its own, as each project of a T3 server is: its
// review, terminals and pane sizes, kept while the user works elsewhere.

// LocalConfig is how the palette reaches this machine's folders.
type LocalConfig struct {
	// History lists the folders reviewed before, most recently opened
	// first. It is read once, when the palette is built.
	History func() []string
	// Remember records dir as opened now. It runs in the background.
	Remember func(dir string)
	// Open builds the review of dir.
	Open func(dir string) (LocalReview, error)
	// SaveHistory files the annotations of dir's review leaving the screen.
	// It runs in the background.
	SaveHistory func(dir, annotations string, files []string)
	// Start is the folder the run's local review shows, nil when it shows
	// none (stdin, a compare pair, a T3 thread).
	Start *LocalWorkspace
}

// LocalReview is the review of a local folder.
type LocalReview struct {
	Model tea.Model
	// Watch starts the live refresh of the folder, nil for none.
	Watch func(p Program) (stop func())
}

// LocalWorkspace is a local folder's workspace.
type LocalWorkspace struct {
	Dir string
	// Watch starts the live refresh of the folder, nil for none.
	Watch func(p Program) (stop func())
}

// historyHeader is how many rows the history sidebar draws above its list:
// the title, the machine, a blank line.
const historyHeader = 3

// historyHelp is the key help drawn under the folders, after a blank row.
var historyHelp = []string{
	"",
	" enter, 1…9   open folder",
	" a            add project",
	" ctrl/alt+b   show / hide",
	" alt+1…9      switch tab",
	" alt+s        switch session",
	" alt+← alt+→  move focus",
	" alt+h alt+l  move focus",
}

// localKey is the split workspace of local folder dir. A T3 key starts with
// its server's label, never empty, so the two never meet.
func localKey(dir string) string {
	return "\x00local\x00" + dir
}

// LocalDir is the local folder whose review is on screen, "" while a T3
// review is, or a local review of no folder.
func (t *Palette) LocalDir() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.local == nil {
		return ""
	}
	return t.local.Dir
}

// initLocalLocked loads the history and puts the run's folder, when it
// reviews one, on top; the folders gone from disk are left out in the
// background (pruneHistoryLocked). mu must be held or the palette not
// shared yet.
func (t *Palette) initLocalLocked() {
	lc := t.cfg.Local
	if lc.History != nil {
		for _, d := range lc.History() {
			t.history = appendDir(t.history, d)
		}
		t.pruneHistoryLocked(t.history)
	}
	if lc.Start == nil || t.onScreen != nil {
		return
	}
	start := *lc.Start
	t.local = &start
	t.ws = localKey(start.Dir)
	t.history = append([]string{start.Dir}, removeDir(t.history, start.Dir)...)
	if lc.Remember != nil {
		t.queueLocked(func() {
			lc.Remember(start.Dir)
		})
	}
}

// isFolder reports whether dir is a folder on disk; a variable for the
// tests.
var isFolder = func(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// pruneHistoryLocked leaves the folders of dirs gone from disk out of the
// history, in the background: a stat of a stale network mount can hang, and
// it must not hold up the start, nor Close past loopsWait. The folder on
// screen and the parked ones stay. mu must be held, or the palette not
// shared yet.
func (t *Palette) pruneHistoryLocked(dirs []string) {
	dirs = slices.Clone(dirs)
	t.loops.Go(func() {
		gone := map[string]bool{}
		for _, d := range dirs {
			if t.ctx.Err() != nil {
				return
			}
			if !t3client.IsRemoteDir(d) && !isFolder(d) { // a machine's folders are not checked here
				gone[d] = true
			}
		}
		if len(gone) == 0 {
			return
		}
		t.mu.Lock()
		keep := t.localDirLocked()
		// a new slice: the sidebar reads the old one without the lock
		t.history = slices.DeleteFunc(slices.Clone(t.history), func(d string) bool {
			return gone[d] && d != keep && t.parkedLocal[localKey(d)] == nil
		})
		p := t.program
		t.mu.Unlock()
		if p != nil {
			p.Send(shellRefreshedMsg{}) // redraws the panel
		}
	})
}

// appendDir appends dir to dirs unless it is there already.
func appendDir(dirs []string, dir string) []string {
	if dir == "" || containsDir(dirs, dir) {
		return dirs
	}
	return append(dirs, dir)
}

// removeDir is dirs without dir.
func removeDir(dirs []string, dir string) []string {
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d != dir {
			out = append(out, d)
		}
	}
	return out
}

// containsDir reports whether dir is one of dirs.
func containsDir(dirs []string, dir string) bool {
	for _, d := range dirs {
		if d == dir {
			return true
		}
	}
	return false
}

// historyDirs are the history panel's folders.
func (t *Palette) historyDirs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.history
}

// openLocal puts local folder dir on screen: its workspace as it was left,
// when it has one, else a new one with its review. The folder on screen
// stays as it is.
func (t *Palette) openLocal(dir string) tea.Cmd {
	key := localKey(dir)
	t.mu.Lock()
	onScreen := t.ws == key
	ws, parked := t.parkedLocal[key]
	if !onScreen {
		t.rememberDirLocked(dir)
	}
	t.mu.Unlock()
	switch {
	case onScreen:
		return nil
	case parked:
		return func() tea.Msg {
			return splitview.SwitchWorkspaceMsg{Key: key, Done: func(sw splitview.WorkspaceSwitch) {
				t.switchedLocal(key, ws, sw)
			}}
		}
	case t.cfg.Local.Open == nil:
		return nil
	}
	return tea.Batch(flash("opening "+filepath.Base(dir)+"…"), func() tea.Msg {
		return t.folderMsg(key, dir, true)
	})
}

// rememberDirLocked records local folder dir as opened now, in the
// background. mu must be held.
func (t *Palette) rememberDirLocked(dir string) {
	if remember := t.cfg.Local.Remember; remember != nil {
		t.queueLocked(func() {
			remember(dir)
		})
	}
}

// folderMsg builds the review of local folder dir and the message that
// puts it on screen in a new workspace, key, with a terminal when
// withTerminal and not NoTerminal. A restored tab passes false: terminals
// are not part of what a tab saves, and the background build may finish
// after another tab took the screen, where the terminal would land.
func (t *Palette) folderMsg(key, dir string, withTerminal bool) tea.Msg {
	return t.folderMsgLogin(key, dir, withTerminal, true)
}

// folderMsgLogin is folderMsg; with login, a folder on an SSH machine that
// cannot be reached for want of a password (its login ended) gets the
// user's answer and is opened once more.
func (t *Palette) folderMsgLogin(key, dir string, withTerminal, login bool) tea.Msg {
	if t.cfg.Local.Open == nil {
		return nil
	}
	r, err := t.cfg.Local.Open(dir)
	if d, remote := t3client.ParseRemoteDir(dir); err != nil && login && remote && t3client.NeedsLogin(err) {
		failed := func(runErr error) tea.Msg {
			return splitview.FlashMsg{Text: "open " + filepath.Base(dir) + ": ssh login to " + d.Host + " did not complete: " + runErr.Error()}
		}
		if cmd := t.loginAnd("ssh://"+d.Host, failed, func() tea.Msg { return t.folderMsgLogin(key, dir, withTerminal, false) }); cmd != nil {
			return cmd()
		}
	}
	if err != nil {
		return splitview.FlashMsg{Text: "open " + filepath.Base(dir) + ": " + err.Error()}
	}
	ws := &LocalWorkspace{Dir: dir, Watch: r.Watch}
	show := splitview.SwitchWorkspaceMsg{Key: key, Model: r.Model, Done: func(sw splitview.WorkspaceSwitch) {
		t.switchedLocal(key, ws, sw)
	}}
	if t.cfg.NoTerminal || !withTerminal {
		return show
	}
	// in order: the terminal opens in the folder's new workspace, there,
	// as a thread's chat pane comes along with it
	return tea.Sequence(func() tea.Msg { return show }, func() tea.Msg { return splitview.OpenTerminalMsg{} })()
}

// switchedLocal brings the palette in line with a switch to local folder
// workspace key, ws: switched's counterpart for a folder of this machine.
// The workspace left is parked, a T3 review keeping its session; the old
// refresh stops and the folder's starts. A restored folder may open in
// place of its stand-in on screen (restore.go), leaving nothing.
func (t *Palette) switchedLocal(key string, ws *LocalWorkspace, sw splitview.WorkspaceSwitch) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || (sw.Left == nil && !sw.Created) {
		return // on screen already
	}
	prevStop := t.stop
	var notes []note
	if sw.Left != nil {
		notes = t.leaveLocked(sw)
	}
	delete(t.parkedLocal, key)
	delete(t.restored, key)
	t.ws, t.onScreen, t.local = key, nil, ws
	t.stop = t.localWatchLocked(ws)
	if !sw.Created && t.program != nil {
		p := t.program
		t.laterLocked(func() {
			p.Send(splitview.FilesChangedMsg{}) // back from the background: catch up
		})
	}
	unused := t.takeUnusedLocked()
	t.laterLocked(func() {
		prevStop()
		closeAll(unused)
		t.saveNotes(notes)
	})
}

// leaveLocked records the workspace on screen as left by sw: its review's
// store, and the workspace parked when the split kept it. It returns the
// annotations to file in history. mu must be held.
func (t *Palette) leaveLocked(sw splitview.WorkspaceSwitch) []note {
	prev := t.onScreen
	if prev != nil {
		t.stores[reviewKey(prev)] = storeOf(sw.Left) // kept for coming back to it
	}
	notes := t.unfiledLocked(sw.Left, noteOf(prev, t.localDirLocked(), sw.Left, nil)) // a safety net, parked or not
	switch {
	case sw.LeftKept && prev != nil:
		t.parked[t.ws] = prev
	case sw.LeftKept && t.local != nil:
		t.parkedLocal[t.ws] = t.local
	default:
		t.dropChatsLocked(t.ws) // the split closed them with their workspace
	}
	return notes
}

// unfiledLocked is notes, what model leaves to history, unless its store
// filed the same annotations when it last left the screen: going back and
// forth, as the session switcher does in passing, files them once, not on
// every pass. mu must be held.
func (t *Palette) unfiledLocked(model tea.Model, notes []note) []note {
	m, ok := model.(annotated)
	if !ok || len(notes) == 0 {
		return notes
	}
	if t.filed[m.Store()] == notes[0].annotations {
		return nil
	}
	t.filed[m.Store()] = notes[0].annotations
	return notes
}

// localDirLocked is the local folder on screen, "" when none. mu must be
// held.
func (t *Palette) localDirLocked() string {
	if t.local == nil {
		return ""
	}
	return t.local.Dir
}

// localWatchLocked starts the live refresh of local folder ws. There is
// none with NoWatch or before Start. mu must be held.
func (t *Palette) localWatchLocked(ws *LocalWorkspace) (stop func()) {
	if t.cfg.NoWatch || t.program == nil || ws == nil || ws.Watch == nil {
		return noop
	}
	return ws.Watch(t.program)
}

// saveNotes files notes in history: a T3 review's by its server, a local
// folder's by its folder, the run's own as the run's source.
func (t *Palette) saveNotes(notes []note) {
	for _, n := range notes {
		if n.review == nil && n.dir != "" && t.cfg.Local.SaveHistory != nil {
			t.cfg.Local.SaveHistory(n.dir, n.annotations, n.files)
			continue
		}
		if t.cfg.SaveHistory != nil {
			t.cfg.SaveHistory(n.review, n.annotations, n.files)
		}
	}
}

// historySidebar lists this machine's folders, numbered; picking one puts
// it on screen (openLocal). It lives on the UI goroutine.
type historySidebar struct {
	t      *Palette
	sel    int
	offset int
}

// Available reports whether there is a folder to list.
func (b *historySidebar) Available() bool {
	return len(b.t.historyDirs()) > 0
}

// Shown selects the folder on screen.
func (b *historySidebar) Shown() {
	current := b.t.LocalDir()
	b.sel = 0
	for i, d := range b.t.historyDirs() {
		if d == current {
			b.sel = i
			return
		}
	}
}

// View draws the title, the machine and the numbered folders: the one on
// screen marked, the selected one highlighted.
func (b *historySidebar) View(width, height int, focused bool) string {
	if height < historyHeader+2 {
		return ""
	}
	dirs := b.t.historyDirs()
	total := len(dirs) + 1 // the Add project… row under the folders
	b.sel = min(b.sel, total-1)
	faint := lipgloss.NewStyle().Faint(true)
	lines := []string{paneTitle("History", focused), faint.Render(" " + b.t.cfg.LocalName), ""}
	if len(dirs) == 0 {
		lines = append(lines, faint.Render(" no folders yet"))
	}
	help := historyHelp
	if height < historyHeader+len(help)+3 {
		help = nil // too short: the folders come first
	}
	rows := max(1, height-historyHeader-len(help))
	b.offset = min(max(b.offset, b.sel-rows+1), b.sel)
	b.offset = max(0, min(b.offset, total-rows))
	current := b.t.LocalDir()
	for i := b.offset; i < min(total, b.offset+rows); i++ {
		if i == len(dirs) {
			lines = append(lines, addProjectRow(width, i == b.sel, focused))
			continue
		}
		lines = append(lines, historyRow(i, dirs[i], width, i == b.sel, focused, dirs[i] == current))
	}
	for _, h := range help {
		lines = append(lines, hintRow(h, width))
	}
	return strings.Join(lines[:min(len(lines), height)], "\n")
}

// historyRow draws folder dir, the list's row i: its name, then its parent
// folder dim.
func historyRow(i int, dir string, width int, selected, focused, current bool) string {
	num := " "
	if i < 9 {
		num = fmt.Sprint(i + 1)
	}
	mark := "  "
	if current {
		mark = "▸ "
	}
	style := rowStyle(selected, focused)
	if current {
		style = style.Bold(true)
	}
	label := ansi.Truncate(" "+num+" "+mark+filepath.Base(dir), width, "…")
	rest := width - ansi.StringWidth(label)
	detail := ""
	if parent := homeRelative(filepath.Dir(dir)); rest > 2 {
		detail = " " + parent
		if over := ansi.StringWidth(detail) - rest; over > 0 { // keep the nearest folders
			detail = " …" + ansi.TruncateLeft(parent, over+1, "")
		}
	}
	detail += strings.Repeat(" ", max(0, rest-ansi.StringWidth(detail)))
	// each part styled whole: a style nested in another would end it early
	return edged(style, label, selected, focused) + detailStyle(style).Render(detail)
}

// homeRelative writes dir under the home folder as ~/…, and a folder on a
// machine as host:path.
func homeRelative(dir string) string {
	if rest, ok := strings.CutPrefix(dir, "ssh:"); ok {
		return rest
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return dir
	}
	if dir == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(dir, home+string(filepath.Separator)); ok {
		return "~/" + rel
	}
	return dir
}

// HandleKey moves the selection and opens the selected folder; a digit
// opens its folder, a adds a project (addproject.go), esc hides the sidebar.
func (b *historySidebar) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	dirs := b.t.historyDirs()
	switch msg.String() {
	case "up", "k":
		b.sel = max(0, b.sel-1)
	case "down", "j":
		b.sel = min(len(dirs), b.sel+1) // past the folders, the Add project… row
	case "home", "g":
		b.sel = 0
	case "end", "G":
		b.sel = len(dirs)
	case "enter", " ":
		if b.sel == len(dirs) {
			return b.t.browseFolders(b.t.browseStart()), true
		}
		return b.open(dirs, b.sel)
	case "a":
		return b.t.browseFolders(b.t.browseStart()), true
	case "esc":
		return nil, true
	default:
		if r := msg.Runes; msg.Type == tea.KeyRunes && len(r) == 1 && r[0] >= '1' && r[0] <= '9' {
			return b.Select(int(r[0] - '0'))
		}
	}
	return nil, false
}

// Click opens the folder on row y.
func (b *historySidebar) Click(y int) (tea.Cmd, bool) {
	i := b.offset + y - historyHeader
	dirs := b.t.historyDirs()
	if y >= historyHeader && i == len(dirs) {
		b.sel = i
		return b.t.browseFolders(b.t.browseStart()), true
	}
	if y < historyHeader || i >= len(dirs) {
		return nil, false
	}
	b.sel = i
	return b.open(dirs, i)
}

// Select opens folder n, counted from 1: its digit, pressed in the panel.
func (b *historySidebar) Select(n int) (tea.Cmd, bool) {
	dirs := b.t.historyDirs()
	if n < 1 || n > len(dirs) {
		return flash(fmt.Sprintf("there is no folder %d in the history", n)), false
	}
	b.sel = n - 1
	return b.open(dirs, n-1)
}

// open puts dirs[i] on screen and hides the sidebar.
func (b *historySidebar) open(dirs []string, i int) (tea.Cmd, bool) {
	if i < 0 || i >= len(dirs) {
		return nil, false
	}
	return b.t.openLocal(dirs[i]), true
}
