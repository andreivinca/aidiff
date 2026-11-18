package t3palette

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/ui"
)

// diffChoice is what T3: Diff View… chose. It applies to the reviews of the
// folder it was chosen in, since a branch named there may not exist in
// another; reviews elsewhere compare what the run's --t3-source says.
type diffChoice struct {
	cwd    string
	source t3client.DiffSource
	label  string
}

// The rows of the diff picker that are not a branch.
type (
	againstBranchRow struct{} // leads to the branch picker
	fileTreeRow      struct{} // every file of the folder instead of its changes
	// wholeFilesRow switches the review on screen between whole files and
	// the changed blocks only, as C does
	wholeFilesRow struct {
		compact bool // what picking it switches to
	}
)

// compacter is a review that can show the changed blocks only.
type compacter interface {
	Compact() bool
	CompactApplicable() bool
	CompactContext() int
}

// viewRow is the diff picker's row that switches the review on screen to
// whole files, or back to the changed blocks; none for a review that
// cannot. It keeps how inner shows its files for the reviews built from
// here, since C and alt+= / alt+- may have changed it.
func (t *Palette) viewRow(inner tea.Model) []splitview.PickerItem {
	c, ok := inner.(compacter)
	if !ok || !c.CompactApplicable() {
		return nil
	}
	t.mu.Lock()
	t.view = View{WholeFiles: !c.Compact(), Context: c.CompactContext()}
	t.mu.Unlock()
	if c.Compact() {
		return []splitview.PickerItem{{Label: "Whole files", Detail: "each file in full, changes marked", Value: wholeFilesRow{}}}
	}
	return []splitview.PickerItem{{Label: "Changed blocks only",
		Detail: fmt.Sprintf("%d lines around each change", c.CompactContext()), Value: wholeFilesRow{compact: true}}}
}

// newReview is the review of target over s, comparing what was chosen for
// its folder.
func (t *Palette) newReview(s Session, target t3client.Target) *Review {
	t.mu.Lock()
	d, view := t.diff, t.view
	t.mu.Unlock()
	r := &Review{Session: s, Target: target, View: view}
	if d.cwd != "" && d.cwd == target.Cwd {
		r.Source, r.SourceLabel = d.source, d.label
	}
	return r
}

// sourceLabel names what the review compares, "" for the run's default.
func (r *Review) sourceLabel() string {
	if r == nil {
		return ""
	}
	return r.SourceLabel
}

// diffCmd offers what the review on screen can compare: the uncommitted
// changes, the unpushed ones, the branch against the default branch, or
// against a branch the user picks. The review's annotations come along.
func (t *Palette) diffCmd(inner tea.Model) tea.Cmd {
	r := t.OnScreen()
	if r == nil {
		return flash("T3: open a thread first")
	}
	store := storeOf(inner)
	view := t.viewRow(inner)
	return asking("listing branches", func(ctx context.Context) tea.Msg {
		refs, err := r.Session.Server().ListRefs(ctx, r.Target.Cwd)
		if errors.Is(err, t3client.ErrNotRepository) {
			return splitview.FlashMsg{Text: "T3: " + r.Target.Label() + " is not a git repository: there is no diff to choose"}
		}
		if err != nil {
			return t3Error("list branches", err)
		}
		return t.diffPicker(r, refs, store, view...)
	})
}

// diffPicker is the list of what to compare, given the repository's refs,
// then the view rows.
func (t *Palette) diffPicker(r *Review, refs []t3client.Ref, store *annotation.Store, view ...splitview.PickerItem) tea.Msg {
	items := compareItems(refs)
	items = append(items, splitview.PickerItem{Label: "File tree", Detail: "every file, each folder listed when opened", Value: fileTreeRow{}})
	items = append(items, view...)
	return splitview.ShowPickerMsg{
		Title:  "T3 Code: diff of " + r.Target.Label(),
		Items:  items,
		OnPick: t.onPick(r, refs, store),
	}
}

// onPick does what a T3: Diff View… row says, with the annotations in
// store.
func (t *Palette) onPick(r *Review, refs []t3client.Ref, store *annotation.Store) func(it splitview.PickerItem) tea.Cmd {
	return func(it splitview.PickerItem) tea.Cmd {
		switch v := it.Value.(type) {
		case againstBranchRow:
			return func() tea.Msg {
				return t.branchPicker(r, refs, store)
			}
		case fileTreeRow:
			return t.showFiles(r, store)
		case wholeFilesRow:
			t.mu.Lock()
			t.view.WholeFiles = !v.compact
			t.mu.Unlock()
			return func() tea.Msg { return ui.SetCompactMsg{On: v.compact} }
		}
		d, _ := it.Value.(diffChoice)
		return t.showDiff(r, d, store)
	}
}

// Comparisons are the comparison rows for the toolbar's View menu, after
// asking the server for the branches: the rows, a report of the one the
// review on screen shows, and how one is picked with the annotations in a
// store.
func (t *Palette) Comparisons(ctx context.Context) ([]splitview.PickerItem, func(splitview.PickerItem) bool,
	func(splitview.PickerItem, *annotation.Store) tea.Cmd, error) {
	r := t.OnScreen()
	if r == nil {
		return nil, nil, nil, errors.New("open a thread first")
	}
	refs, err := r.Session.Server().ListRefs(ctx, r.Target.Cwd)
	if errors.Is(err, t3client.ErrNotRepository) {
		return nil, nil, nil, errors.New(r.Target.Label() + " is not a git repository")
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list branches: %w", err)
	}
	current := cmp.Or(r.SourceLabel, t.cfg.DiffLabel)
	isCurrent := func(it splitview.PickerItem) bool {
		d, ok := it.Value.(diffChoice)
		return ok && d.label == current
	}
	pick := func(it splitview.PickerItem, store *annotation.Store) tea.Cmd {
		return t.onPick(r, refs, store)(it)
	}
	return compareItems(refs), isCurrent, pick, nil
}

// compareItems are what a T3 review can compare: the uncommitted changes,
// the unpushed ones when the branch has an upstream, the branch's, and a
// branch to pick.
func compareItems(refs []t3client.Ref) []splitview.PickerItem {
	items := []splitview.PickerItem{
		{Label: "Uncommitted changes", Detail: "the working tree",
			Value: diffChoice{source: t3client.DiffSource{Kind: t3client.SourceWorkingTree}, label: "uncommitted"}},
	}
	for _, ref := range refs {
		if up := ref.Upstream(); ref.Current && up != "" {
			items = append(items, splitview.PickerItem{Label: "Unpushed changes", Detail: "vs " + up + ", uncommitted included",
				Value: diffChoice{source: t3client.DiffSource{Kind: t3client.SourceBranchRange, BaseRef: up}, label: "unpushed (vs " + up + ")"}})
		}
	}
	items = append(items,
		splitview.PickerItem{Label: "Branch changes", Detail: "vs the default branch, uncommitted included",
			Value: diffChoice{source: t3client.DiffSource{Kind: t3client.SourceBranchRange}, label: "branch"}},
		splitview.PickerItem{Label: "Against a branch…", Detail: "pick the base", Value: againstBranchRow{}})
	return items
}

// branchPicker lists the refs to compare the branch against, the current
// one left out; a typed ref, such as a tag or a commit, is taken too.
func (t *Palette) branchPicker(r *Review, refs []t3client.Ref, store *annotation.Store) tea.Msg {
	against := func(ref string) diffChoice {
		return diffChoice{source: t3client.DiffSource{Kind: t3client.SourceBranchRange, BaseRef: ref}, label: "vs " + ref}
	}
	items := make([]splitview.PickerItem, 0, len(refs))
	for _, ref := range refs {
		if ref.Current {
			continue
		}
		detail := "local"
		switch {
		case ref.IsDefault:
			detail = "default branch"
		case ref.IsRemote:
			detail = "remote"
		}
		items = append(items, splitview.PickerItem{Label: ref.Name, Detail: detail, Value: against(ref.Name)})
	}
	return splitview.ShowPickerMsg{
		Title: "T3 Code: compare " + r.Target.Label() + " against",
		Items: items,
		OnPick: func(it splitview.PickerItem) tea.Cmd {
			d, _ := it.Value.(diffChoice)
			return t.showDiff(r, d, store)
		},
		OnText: func(typed string) tea.Cmd {
			return t.showDiff(r, against(typed), store)
		},
		TextLabel: "Compare against %s",
	}
}

// showFiles rebuilds r as a browser of every file in its folder, with the
// annotations in store. What the folder's diffs compare stays chosen, for
// the way back.
func (t *Palette) showFiles(r *Review, store *annotation.Store) tea.Cmd {
	t.mu.Lock()
	view := t.view
	t.mu.Unlock()
	next := &Review{Session: r.Session, Target: r.Target, Source: t3client.DiffSource{Kind: t3client.SourceFiles},
		SourceLabel: "file tree", View: view}
	return tea.Batch(flash("T3: file tree…"), func() tea.Msg {
		return t.showMsg(next, store, "show the file tree")
	})
}

// ShowView is the toolbar's switch for the T3 review on screen: every file
// of its folder, or its changes again, compared as chosen last for that
// folder.
func (t *Palette) ShowView(inner tea.Model, files bool) tea.Cmd {
	r := t.OnScreen()
	if r == nil {
		return flash("T3: open a thread first")
	}
	store := storeOf(inner)
	if files {
		return t.showFiles(r, store)
	}
	t.mu.Lock()
	d := t.diff
	t.mu.Unlock()
	if d.cwd != r.Target.Cwd {
		d = diffChoice{} // the run's --t3-source
	}
	if d.label == "" {
		d.label = t.cfg.DiffLabel
	}
	return t.showDiff(r, d, store)
}

// showDiff makes d the choice for r's folder and rebuilds r comparing it,
// with the annotations in store.
func (t *Palette) showDiff(r *Review, d diffChoice, store *annotation.Store) tea.Cmd {
	d.cwd = r.Target.Cwd
	t.mu.Lock()
	t.diff = d
	view := t.view
	t.mu.Unlock()
	next := &Review{Session: r.Session, Target: r.Target, Source: d.source, SourceLabel: d.label, View: view}
	return tea.Batch(flash("T3: diff: "+d.label), func() tea.Msg {
		return t.showMsg(next, store, "show diff")
	})
}
