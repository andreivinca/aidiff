package main

import (
	"context"
	"fmt"
	"path"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/ui"
)

// paletteFileLimit is the most files the palette lists for a query.
const paletteFileLimit = 200

// fileFinder is a review that finds its files by name (ui.Model.FindFiles).
type fileFinder interface {
	FindFiles(query string, limit int) func(context.Context) (diff.FoundPaths, error)
}

// paletteFiles is the palette's search by name, its query without '>': the
// files of the review's changes, or every file of the folder it browses.
// Picking one shows it in the review.
func paletteFiles(inner tea.Model, query string) tea.Cmd {
	f, ok := inner.(fileFinder)
	if !ok {
		return nil
	}
	find := f.FindFiles(query, paletteFileLimit)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localDiffTimeout)
		defer cancel()
		found, err := find(ctx)
		msg := splitview.FilesMsg{Query: query, OnPick: openPickedFile}
		if err != nil {
			msg.Note = err.Error()
			return msg
		}
		for _, p := range found.Paths {
			dir := path.Dir(p)
			if dir == "." {
				dir = ""
			}
			msg.Items = append(msg.Items, splitview.PickerItem{Label: path.Base(p), Detail: dir, Value: p})
		}
		if found.Truncated {
			msg.Note = fmt.Sprintf("the first %d matches: type more to narrow them", len(found.Paths))
		}
		return msg
	}
}

// carryFile keeps the file the old review showed selected in the review
// replacing it, such as the same review in another view or comparison, when
// the new one lists that file.
func carryFile(old, _ tea.Model) tea.Cmd {
	c, ok := old.(interface{ CurrentFile() string })
	if !ok || c.CurrentFile() == "" {
		return nil
	}
	msg := ui.OpenFileMsg{Path: c.CurrentFile()}
	return func() tea.Msg { return msg }
}

// openPickedFile shows the file a palette row names.
func openPickedFile(it splitview.PickerItem) tea.Cmd {
	p, _ := it.Value.(string)
	return func() tea.Msg { return ui.OpenFileMsg{Path: p, Focus: true} }
}
