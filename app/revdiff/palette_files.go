package main

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/t3palette"
	"github.com/umputun/revdiff/app/ui"
)

// paletteFileLimit is the most files the palette lists for a query.
const paletteFileLimit = 200

// projectGroup titles the palette's project files, under the changed ones.
const projectGroup = "In the project"

// noChangedMatch is the row standing for the changed files when none
// matches, so the project's files still sit under their rule.
const noChangedMatch = "no changed file matches"

// carryWindow is how long a project file picked in the palette waits for
// the file tree it switched to; a switch that failed leaves it to expire.
const carryWindow = 30 * time.Second

// fileFinder is a review that finds its files by name (ui.Model.FindFiles).
type fileFinder interface {
	FindFiles(query string, limit int) func(context.Context) (diff.FoundPaths, error)
}

// changeLister is a review that tells whether it lists the changed files
// (ui.Model.ListsChanges), not every file of a folder.
type changeLister interface {
	ListsChanges() bool
}

// projectFiles is the whole project of a review of changes: search finds
// its files by name, show switches the review to its file tree. A zero
// value has no project to search.
type projectFiles struct {
	search ui.FileSearcher
	show   func() tea.Cmd
}

// projectPick is a palette row naming a file of the project, not of the
// changes: picking it switches to the file tree first.
type projectPick string

// paletteFinder is the palette's search by name and what carries a file
// over when the review is replaced.
type paletteFinder struct {
	project func(inner tea.Model) projectFiles // nil: no project search

	mu     sync.Mutex
	next   string    // the project file to show once the file tree is on screen
	nextAt time.Time // when it was picked
}

// files is the palette's search by name, its query without '>': the files
// of the review's changes first, then, under a rule, the other files of
// the project; or every file of the folder a browser shows. Picking one
// shows it in the review.
func (f *paletteFinder) files(inner tea.Model, query string) tea.Cmd {
	finder, ok := inner.(fileFinder)
	if !ok {
		return nil
	}
	find := finder.FindFiles(query, paletteFileLimit)
	var proj projectFiles
	if l, ok := inner.(changeLister); ok && l.ListsChanges() && f.project != nil {
		proj = f.project(inner)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localDiffTimeout)
		defer cancel()
		msg := splitview.FilesMsg{Query: query, OnPick: f.onPick(proj)}
		found, err := find(ctx)
		if err != nil {
			msg.Note = err.Error()
			return msg
		}
		var notes []string
		for _, p := range found.Paths {
			msg.Items = append(msg.Items, fileItem(p, p, ""))
		}
		if found.Truncated {
			notes = append(notes, fmt.Sprintf("the first %d matches", len(found.Paths)))
		}
		if proj.search == nil || strings.TrimSpace(query) == "" {
			msg.Note = noteOf(notes)
			return msg
		}
		if len(msg.Items) == 0 {
			msg.Items = append(msg.Items, splitview.PickerItem{Label: noChangedMatch})
		}
		more, err := proj.search.SearchFiles(ctx, query, paletteFileLimit)
		if err != nil {
			more = diff.FoundPaths{}
			notes = append(notes, "project: "+err.Error())
		}
		changed := make(map[string]bool, len(found.Paths))
		for _, p := range found.Paths {
			changed[p] = true
		}
		for _, p := range more.Paths {
			if !changed[p] {
				msg.Items = append(msg.Items, fileItem(p, projectPick(p), projectGroup))
			}
		}
		if more.Truncated {
			notes = append(notes, fmt.Sprintf("the first %d project matches", len(more.Paths)))
		}
		msg.Note = noteOf(notes)
		return msg
	}
}

// fileItem is the palette row of file p: its name, its folder beside it.
func fileItem(p string, value any, group string) splitview.PickerItem {
	dir := path.Dir(p)
	if dir == "." {
		dir = ""
	}
	return splitview.PickerItem{Label: path.Base(p), Detail: dir, Value: value, Group: group}
}

// noteOf is said under the rows: the lists cut short, or nothing.
func noteOf(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	note := strings.Join(notes, ", ")
	if strings.Contains(note, "matches") {
		note += ": type more to narrow them"
	}
	return note
}

// onPick shows the file a row names: a changed file in the review on
// screen, a project file in the project's file tree.
func (f *paletteFinder) onPick(proj projectFiles) func(splitview.PickerItem) tea.Cmd {
	return func(it splitview.PickerItem) tea.Cmd {
		switch v := it.Value.(type) {
		case string:
			return func() tea.Msg { return ui.OpenFileMsg{Path: v, Focus: true} }
		case projectPick:
			if proj.show == nil {
				return nil
			}
			f.mu.Lock()
			f.next, f.nextAt = string(v), time.Now()
			f.mu.Unlock()
			return proj.show()
		}
		return nil
	}
}

// carry keeps a file selected in the review replacing old: the project
// file just picked in the palette, else the file old showed, when the new
// review lists it.
func (f *paletteFinder) carry(old, _ tea.Model) tea.Cmd {
	f.mu.Lock()
	next, at := f.next, f.nextAt
	f.next = ""
	f.mu.Unlock()
	if next != "" && time.Since(at) < carryWindow {
		return func() tea.Msg { return ui.OpenFileMsg{Path: next, Focus: true} }
	}
	c, ok := old.(interface{ CurrentFile() string })
	if !ok || c.CurrentFile() == "" {
		return nil
	}
	msg := ui.OpenFileMsg{Path: c.CurrentFile()}
	return func() tea.Msg { return msg }
}

// reviewProject is the project of the review on screen: the folder of a T3
// review, else the repository of the local review (the run's own, or a
// folder's from the history). Showing a file of it switches to the file
// tree, as the toolbar does.
func reviewProject(t3p *t3palette.Palette, local *localDiff, diffs *localDiffs) func(inner tea.Model) projectFiles {
	view := switchView(t3p, local, diffs)
	return func(inner tea.Model) projectFiles {
		show := func() tea.Cmd { return view(inner, true) }
		if r := t3p.OnScreen(); r != nil {
			return projectFiles{search: t3client.NewFolderReader(r.Session.Conn(), r.Target.Cwd), show: show}
		}
		l := local
		if dir := t3p.LocalDir(); dir != "" {
			l = diffs.get(dir)
		}
		if l == nil {
			return projectFiles{}
		}
		return projectFiles{search: l.projectFolder(), show: show}
	}
}

// projectFolder is every file of the repository, made once so its file
// list serves the searches typed in a row.
func (l *localDiff) projectFolder() *diff.FolderReader {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.project == nil {
		l.project = diff.NewFolderReader(cmp.Or(l.src.gitRoot, l.src.workDir), l.deps.opts.Include, l.deps.opts.Exclude)
	}
	return l.project
}
