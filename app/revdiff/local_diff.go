package main

import (
	"context"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/splitview"
)

// localDiffTimeout bounds the git queries of one Diff View… step.
const localDiffTimeout = 15 * time.Second

// localDiff is the palette's Diff View… for a review of a local git repository:
// it rebuilds the review comparing something else, with its annotations.
type localDiff struct {
	deps        reviewDeps
	src         sourceSetup
	description string
	git         localGit

	mu    sync.Mutex
	label string // what the review on screen compares
}

// localChoice is what a review compares: a ref (the working tree against it,
// or "" for the unstaged changes) or the staged changes.
type localChoice struct {
	ref    string
	staged bool
	label  string
}

// againstBranch is the Diff View… row that leads to the branch picker.
type againstBranch struct{}

// newLocalDiff is the Diff View… of the run's review, nil unless that is a
// review of a git repository's changes: not stdin, a compare pair, a plain
// folder, every file, or a T3 thread.
func newLocalDiff(deps reviewDeps, src sourceSetup, description string) *localDiff {
	opts := deps.opts
	if src.vcsType != diff.VCSGit || src.folder != nil || src.t3 != nil || opts.Stdin || opts.AllFiles || opts.CompareOld != "" {
		return nil
	}
	return &localDiff{deps: deps, src: src, description: description, git: localGit{dir: src.workDir}, label: runLabel(opts)}
}

// runLabel names what the run's own review compares.
func runLabel(opts options) string {
	switch ref := opts.ref(); {
	case opts.Staged:
		return "staged"
	case ref != "":
		return "vs " + ref
	}
	return "unstaged"
}

// command is the palette row.
func (l *localDiff) command() splitview.Command {
	l.mu.Lock()
	defer l.mu.Unlock()
	return splitview.Command{Name: "Diff View…", Detail: l.label, Run: l.pick}
}

// pick lists what the review can compare, after asking git for the
// upstream and the default branch.
func (l *localDiff) pick(inner tea.Model) tea.Cmd {
	store := reviewStore(inner)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localDiffTimeout)
		defer cancel()
		items := []splitview.PickerItem{
			{Label: "Uncommitted changes", Detail: "staged and unstaged", Value: localChoice{ref: "HEAD", label: "uncommitted"}},
			{Label: "Unstaged changes", Detail: "not staged yet", Value: localChoice{label: "unstaged"}},
			{Label: "Staged changes", Detail: "what the next commit takes", Value: localChoice{staged: true, label: "staged"}},
		}
		up := l.git.upstream(ctx)
		if up != "" {
			items = append(items, splitview.PickerItem{Label: "Unpushed changes", Detail: "vs " + up + ", uncommitted included",
				Value: localChoice{ref: up, label: "unpushed (vs " + up + ")"}})
		}
		if def := l.git.defaultBranch(ctx); def != "" && def != up { // on the default branch, unpushed says it already
			items = append(items, splitview.PickerItem{Label: "Branch changes", Detail: "vs " + def + ", uncommitted included",
				Value: localChoice{ref: def, label: "vs " + def}})
		}
		items = append(items, splitview.PickerItem{Label: "Against a branch…", Detail: "pick the base", Value: againstBranch{}})
		return splitview.ShowPickerMsg{
			Title: "Diff of " + l.src.projectName(),
			Items: items,
			OnPick: func(it splitview.PickerItem) tea.Cmd {
				if _, branches := it.Value.(againstBranch); branches {
					return l.pickBranch(store)
				}
				c, _ := it.Value.(localChoice)
				return l.show(c, store)
			},
		}
	}
}

// pickBranch lists the branches to compare against, the current one left
// out; a typed ref, such as a tag or a commit, is taken too.
func (l *localDiff) pickBranch(store *annotation.Store) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localDiffTimeout)
		defer cancel()
		refs, err := l.git.refs(ctx)
		if err != nil {
			return splitview.FlashMsg{Text: "Diff: " + err.Error()}
		}
		against := func(ref string) localChoice {
			return localChoice{ref: ref, label: "vs " + ref}
		}
		items := make([]splitview.PickerItem, 0, len(refs))
		for _, r := range refs {
			if r.current {
				continue
			}
			detail := "local"
			if r.remote {
				detail = "remote"
			}
			items = append(items, splitview.PickerItem{Label: r.name, Detail: detail, Value: against(r.name)})
		}
		return splitview.ShowPickerMsg{
			Title: "Compare " + l.src.projectName() + " against",
			Items: items,
			OnPick: func(it splitview.PickerItem) tea.Cmd {
				c, _ := it.Value.(localChoice)
				return l.show(c, store)
			},
			OnText: func(typed string) tea.Cmd {
				return l.show(against(typed), store)
			},
			TextLabel: "Compare against %s",
		}
	}
}

// show rebuilds the review comparing c, with the annotations in store. A
// branch is compared from the commit it shares with HEAD, so the review
// shows what this branch changed, not what the other one gained since.
func (l *localDiff) show(c localChoice, store *annotation.Store) tea.Cmd {
	return tea.Batch(func() tea.Msg { return splitview.FlashMsg{Text: "Diff: " + c.label + "…"} }, func() tea.Msg {
		ref := c.ref
		if ref != "" && ref != "HEAD" {
			ctx, cancel := context.WithTimeout(context.Background(), localDiffTimeout)
			defer cancel()
			base, err := l.git.mergeBase(ctx, ref)
			if err != nil {
				return splitview.FlashMsg{Text: "Diff: " + err.Error()}
			}
			ref = base
		}
		deps := l.deps
		deps.opts.Refs.Base, deps.opts.Refs.Against, deps.opts.Staged = ref, "", c.staged
		model, err := deps.newModel(l.src, store, l.description+"\n\ndiff: "+c.label)
		if err != nil {
			return splitview.FlashMsg{Text: "Diff: " + err.Error()}
		}
		l.mu.Lock()
		l.label = c.label
		l.mu.Unlock()
		return splitview.ReplaceInnerMsg{Model: model}
	})
}

// reviewStore is the annotation store of the review model, a new one for
// any other model.
func reviewStore(m tea.Model) *annotation.Store {
	if r, ok := m.(interface{ Store() *annotation.Store }); ok {
		return r.Store()
	}
	return annotation.NewStore()
}
