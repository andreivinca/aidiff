package main

import (
	"cmp"
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

	mu      sync.Mutex
	label   string             // what the review on screen compares
	compact bool               // changed blocks only, kept when Diff View… rebuilds the review
	context int                // lines around each change, kept the same way (alt+= / alt+-)
	last    *localChoice       // the comparison shown last, nil for the run's own
	shown   *localChoice       // what the review on screen compares, its ref resolved; nil for the run's own
	project *diff.FolderReader // every file of the repository, for the palette's search (palette_files.go)
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

// fileTree is the Diff View… row that browses every file of the repository.
type fileTree struct{}

// compacter is a review that can show the changed blocks only.
type compacter interface {
	Compact() bool
	CompactApplicable() bool
	CompactContext() int
}

// newLocalDiff is the Diff View… of the run's review, nil unless that is a
// review of a git repository's changes: not stdin, a compare pair, a plain
// folder, every file, or a T3 thread.
func newLocalDiff(deps reviewDeps, src sourceSetup, description string) *localDiff {
	opts := deps.opts
	if src.folder != nil || !hasLocalDiff(opts, src) {
		return nil
	}
	return &localDiff{deps: deps, src: src, description: description, git: localGit{dir: src.workDir}, label: runLabel(opts), compact: opts.Compact,
		context: opts.CompactContext}
}

// hasLocalDiff reports whether a review of src run with opts belongs to a
// Diff View…: a git repository's, not stdin, a compare pair, every file
// (--all-files) or a T3 thread. The toolbar's View switch asks it too
// (viewSwitch.forSource), so it offers no switch that has nothing to run.
func hasLocalDiff(opts options, src sourceSetup) bool {
	return src.vcsType == diff.VCSGit && src.t3 == nil && !opts.Stdin && !opts.AllFiles && opts.CompareOld == ""
}

// compared is opts comparing what the review on screen compares, for filing
// its annotations: Diff View… may have changed it since the run began. A nil
// l leaves opts as they are.
func (l *localDiff) compared(opts options) options {
	if l == nil {
		return opts
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.shown != nil {
		opts.Refs.Base, opts.Refs.Against, opts.Staged = l.shown.ref, "", l.shown.staged
	}
	return opts
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
	view := l.viewRows(inner)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localDiffTimeout)
		defer cancel()
		return splitview.ShowPickerMsg{
			Title:  "Diff of " + l.src.projectName(),
			Items:  append(l.compareItems(ctx), view...),
			OnPick: l.onPick(store),
		}
	}
}

// compareItems are what the review can compare: the uncommitted, unstaged
// and staged changes, the unpushed ones and the branch's when git knows the
// upstream and the default branch, and a branch to pick.
func (l *localDiff) compareItems(ctx context.Context) []splitview.PickerItem {
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
	return append(items, splitview.PickerItem{Label: "Against a branch…", Detail: "pick the base", Value: againstBranch{}})
}

// onPick does what a Diff View… row says, with the annotations in store.
func (l *localDiff) onPick(store *annotation.Store) func(it splitview.PickerItem) tea.Cmd {
	return func(it splitview.PickerItem) tea.Cmd {
		switch it.Value.(type) {
		case againstBranch:
			return l.pickBranch(store)
		case fileTree:
			return l.showTree(store)
		}
		c, _ := it.Value.(localChoice)
		return l.show(c, store)
	}
}

// viewRows are the Diff View… rows after the comparisons: the file tree.
// Whole files or the changed blocks are the context dropdown's, on the
// toolbar; how inner shows its files is kept for the reviews Diff View…
// builds, since C, alt+= / alt+- and that dropdown may have changed it.
func (l *localDiff) viewRows(inner tea.Model) []splitview.PickerItem {
	l.keepCompact(inner)
	return []splitview.PickerItem{{Label: "File tree", Detail: "every file, each folder listed when opened", Value: fileTree{}}}
}

// keepCompact records how inner shows its files, whole or the changed blocks
// only, for the reviews Diff View… builds next.
func (l *localDiff) keepCompact(inner tea.Model) {
	if c, ok := inner.(compacter); ok && c.CompactApplicable() {
		l.mu.Lock()
		l.compact = c.Compact()
		l.context = c.CompactContext()
		l.mu.Unlock()
	}
}

// showTree rebuilds the review as a browser of every file in the
// repository, with the annotations in store. Like a plain folder, it lists
// a folder only once it is opened, so it starts at once in a large tree.
func (l *localDiff) showTree(store *annotation.Store) tea.Cmd {
	return tea.Batch(func() tea.Msg { return splitview.FlashMsg{Text: "Diff: file tree…"} }, func() tea.Msg {
		root := cmp.Or(l.src.gitRoot, l.src.workDir)
		folder := diff.NewFolderReader(root, l.deps.opts.Include, l.deps.opts.Exclude)
		src := sourceSetup{vcsSetup: vcsSetup{renderer: folder, vcsType: l.src.vcsType, gitRoot: l.src.gitRoot, workDir: root}, folder: folder}
		deps := l.deps
		deps.opts.Refs.Base, deps.opts.Refs.Against, deps.opts.Staged = "", "", false
		model, err := deps.newModel(src, store, l.description+"\n\nview: file tree")
		if err != nil {
			return splitview.FlashMsg{Text: "Diff: " + err.Error()}
		}
		l.mu.Lock()
		l.label = "file tree"
		l.shown = &localChoice{label: l.label} // every file as it is now: the working tree
		l.mu.Unlock()
		return splitview.ReplaceInnerMsg{Model: model}
	})
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
		l.mu.Lock()
		deps.opts.Compact = l.compact
		if l.context > 0 {
			deps.opts.CompactContext = l.context
		}
		l.mu.Unlock()
		model, err := deps.newModel(l.src, store, l.description+"\n\ndiff: "+c.label)
		if err != nil {
			return splitview.FlashMsg{Text: "Diff: " + err.Error()}
		}
		l.mu.Lock()
		l.label = c.label
		l.last = &c
		l.shown = &localChoice{ref: ref, staged: c.staged, label: c.label}
		l.mu.Unlock()
		return splitview.ReplaceInnerMsg{Model: model}
	})
}

// showView is the toolbar's switch: every file of the repository, or its
// changes again, compared as they were last.
func (l *localDiff) showView(inner tea.Model, files bool) tea.Cmd {
	store := reviewStore(inner)
	l.keepCompact(inner) // C, alt+= / alt+- or the context dropdown may have changed it
	if files {
		return l.showTree(store)
	}
	l.mu.Lock()
	last := l.last
	l.mu.Unlock()
	if last != nil {
		return l.show(*last, store)
	}
	return tea.Batch(func() tea.Msg { return splitview.FlashMsg{Text: "Diff: " + runLabel(l.deps.opts) + "…"} }, func() tea.Msg {
		deps := l.deps
		l.mu.Lock()
		deps.opts.Compact = l.compact
		if l.context > 0 {
			deps.opts.CompactContext = l.context
		}
		l.mu.Unlock()
		model, err := deps.newModel(l.src, store, l.description)
		if err != nil {
			return splitview.FlashMsg{Text: "Diff: " + err.Error()}
		}
		l.mu.Lock()
		l.label = runLabel(l.deps.opts)
		l.shown = nil
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
