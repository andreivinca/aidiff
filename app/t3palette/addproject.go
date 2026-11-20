package t3palette

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/splitview"
)

// Add Project… opens a folder of this machine that is not in the history
// yet, without leaving aidiff: the palette browses the folders, starting at
// the parent of the folder on screen (else the home folder), and opening
// one puts it in its own workspace, at the end of the history so the
// numbers of the folders listed already stay put. A typed path opens too.

// addProjectName is the palette command's name.
const addProjectName = "Add Project…"

// browseDir is a folder browser row: the folder to list next.
type browseDir string

// openDir is the browser row that opens the folder it lists.
type openDir string

// addProjectCmd is the palette command, for this machine's connection.
func (t *Palette) addProjectCmd() splitview.Command {
	return splitview.Command{Name: addProjectName, Detail: "open another folder", Run: func(tea.Model) tea.Cmd {
		return t.browseFolders(t.browseStart())
	}}
}

// browseStart is where the browser starts: the parent of the folder on
// screen, else the home folder, else the working directory.
func (t *Palette) browseStart() string {
	if dir := t.LocalDir(); dir != "" {
		return filepath.Dir(dir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return string(filepath.Separator)
}

// browseFolders lists dir in the palette: a row opening it, its parent,
// then its folders, hidden ones last.
func (t *Palette) browseFolders(dir string) tea.Cmd {
	return func() tea.Msg {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return splitview.FlashMsg{Text: "Add Project: " + err.Error()}
		}
		items := []splitview.PickerItem{{Label: "✓ Open " + filepath.Base(dir), Detail: homeRelative(dir), Value: openDir(dir)}}
		if parent := filepath.Dir(dir); parent != dir {
			items = append(items, splitview.PickerItem{Label: "..", Detail: homeRelative(parent), Value: browseDir(parent)})
		}
		var shown, hidden []splitview.PickerItem
		for _, e := range entries {
			if !isDir(dir, e) {
				continue
			}
			it := splitview.PickerItem{Label: e.Name() + "/", Value: browseDir(filepath.Join(dir, e.Name()))}
			if strings.HasPrefix(e.Name(), ".") {
				hidden = append(hidden, it)
				continue
			}
			shown = append(shown, it)
		}
		byLabel := func(a, b splitview.PickerItem) int {
			return strings.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label))
		}
		slices.SortFunc(shown, byLabel)
		slices.SortFunc(hidden, byLabel)
		items = append(append(items, shown...), hidden...)
		return splitview.ShowPickerMsg{
			Title:     "Add Project: " + homeRelative(dir),
			Items:     items,
			OnPick:    t.onBrowse,
			OnText:    func(typed string) tea.Cmd { return t.openTyped(dir, typed) },
			TextLabel: "Go to %s",
		}
	}
}

// isDir reports whether entry e of dir is a folder, a link to one included.
func isDir(dir string, e os.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink == 0 {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, e.Name()))
	return err == nil && fi.IsDir()
}

// onBrowse lists the folder a row names, or opens it.
func (t *Palette) onBrowse(it splitview.PickerItem) tea.Cmd {
	switch v := it.Value.(type) {
	case browseDir:
		return t.browseFolders(string(v))
	case openDir:
		return t.addProject(string(v))
	}
	return nil
}

// openTyped lists the folder a typed path names: absolute, under the home
// folder with ~, else relative to dir, the folder listed.
func (t *Palette) openTyped(dir, typed string) tea.Cmd {
	p := strings.TrimSpace(typed)
	if rest, ok := strings.CutPrefix(p, "~"); ok && (rest == "" || strings.HasPrefix(rest, string(filepath.Separator))) {
		if home, err := os.UserHomeDir(); err == nil {
			p = home + rest
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	p = filepath.Clean(p)
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		return flash("Add Project: " + homeRelative(p) + " is not a folder")
	}
	return t.browseFolders(p)
}

// addProject lists dir in the history, when it is not there yet, and puts
// it on screen.
func (t *Palette) addProject(dir string) tea.Cmd {
	dir = filepath.Clean(dir)
	t.mu.Lock()
	t.history = appendDir(t.history, dir)
	t.mu.Unlock()
	return t.openLocal(dir)
}
