package t3palette

import (
	"context"
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// diffChoice is what T3: Diff… chose. It applies to the reviews of the
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
)

// newReview is the review of target over s, comparing what was chosen for
// its folder.
func (t *Palette) newReview(s Session, target t3client.Target) *Review {
	t.mu.Lock()
	d := t.diff
	t.mu.Unlock()
	r := &Review{Session: s, Target: target}
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
	return asking("listing branches", func(ctx context.Context) tea.Msg {
		refs, err := r.Session.Server().ListRefs(ctx, r.Target.Cwd)
		if errors.Is(err, t3client.ErrNotRepository) {
			return splitview.FlashMsg{Text: "T3: " + r.Target.Label() + " is not a git repository: there is no diff to choose"}
		}
		if err != nil {
			return t3Error("list branches", err)
		}
		return t.diffPicker(r, refs, store)
	})
}

// diffPicker is the list of what to compare, given the repository's refs.
func (t *Palette) diffPicker(r *Review, refs []t3client.Ref, store *annotation.Store) tea.Msg {
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
	return splitview.ShowPickerMsg{
		Title: "T3 Code: diff of " + r.Target.Label(),
		Items: items,
		OnPick: func(it splitview.PickerItem) tea.Cmd {
			if _, pick := it.Value.(againstBranchRow); pick {
				return func() tea.Msg {
					return t.branchPicker(r, refs, store)
				}
			}
			d, _ := it.Value.(diffChoice)
			return t.showDiff(r, d, store)
		},
	}
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

// showDiff makes d the choice for r's folder and rebuilds r comparing it,
// with the annotations in store.
func (t *Palette) showDiff(r *Review, d diffChoice, store *annotation.Store) tea.Cmd {
	d.cwd = r.Target.Cwd
	t.mu.Lock()
	t.diff = d
	t.mu.Unlock()
	next := &Review{Session: r.Session, Target: r.Target, Source: d.source, SourceLabel: d.label}
	return tea.Batch(flash("T3: diff: "+d.label), func() tea.Msg {
		return t.showMsg(next, store, "show diff")
	})
}
