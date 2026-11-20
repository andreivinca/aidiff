package t3palette

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

func diffRefs() []t3client.Ref {
	return []t3client.Ref{
		{Name: "feature", UpstreamRef: "refs/remotes/origin/feature", Current: true},
		{Name: "main", IsDefault: true},
		{Name: "origin/main", IsRemote: true},
	}
}

func labels(items []splitview.PickerItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}

// shownReview is the review a ReplaceInnerMsg among msgs puts on screen.
func shownReview(t *testing.T, msgs []tea.Msg) *Review {
	t.Helper()
	fm, ok := find[splitview.SwitchWorkspaceMsg](t, msgs).Model.(fakeModel)
	require.True(t, ok)
	return fm.review
}

// commandDetail is the detail of the palette row named name.
func commandDetail(cmds []splitview.Command, name string) string {
	for _, c := range cmds {
		if c.Name == name {
			return c.Detail
		}
	}
	return "(no " + name + ")"
}

func TestPalette_DiffPicksWhatToCompare(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	a.server.refs = diffRefs()
	h := newHarness(t, Config{NoWatch: true, NoChat: true, DiffLabel: "uncommitted"}, threadReview(t, a, loginID))
	h.connect(t, "ssh://a", a)
	shown := annotatedModel()

	assert.Equal(t, "uncommitted", commandDetail(h.p.Commands(), "T3: Diff View…"), "the run's default until something else is chosen")

	picker := find[splitview.ShowPickerMsg](t, run(h.p.diffCmd(shown)))
	assert.Equal(t, []string{"Uncommitted changes", "Unpushed changes", "Branch changes", "Against a branch…", "File tree"}, labels(picker.Items))
	assert.Equal(t, "vs origin/feature, uncommitted included", picker.Items[1].Detail, "unpushed compares with the branch's upstream")

	r := shownReview(t, run(picker.OnPick(picker.Items[1])))
	assert.Equal(t, t3client.DiffSource{Kind: t3client.SourceBranchRange, BaseRef: "origin/feature"}, r.Source)
	assert.Equal(t, loginID, r.Target.Thread.ID, "the same thread")
	replace := find[splitview.SwitchWorkspaceMsg](t, run(picker.OnPick(picker.Items[1])))
	assert.Same(t, shown.store, replace.Model.(fakeModel).store, "the annotations come along")
	h.swap(replace, shown)
	assert.Contains(t, commandDetail(h.p.Commands(), "T3: Diff View…"), "unpushed")

	branches := find[splitview.ShowPickerMsg](t, run(picker.OnPick(picker.Items[3])))
	assert.Equal(t, []string{"main", "origin/main"}, labels(branches.Items), "every branch but the current one")
	assert.Equal(t, []string{"default branch", "remote"}, []string{branches.Items[0].Detail, branches.Items[1].Detail})
	r = shownReview(t, run(branches.OnPick(branches.Items[1])))
	assert.Equal(t, t3client.DiffSource{Kind: t3client.SourceBranchRange, BaseRef: "origin/main"}, r.Source)
	r = shownReview(t, run(branches.OnText("v0.0.10")))
	assert.Equal(t, "v0.0.10", r.Source.BaseRef, "a typed ref, such as a tag, is compared against too")
	assert.Equal(t, "vs v0.0.10", r.SourceLabel)

	r = shownReview(t, run(picker.OnPick(picker.Items[0])))
	assert.Equal(t, t3client.DiffSource{Kind: t3client.SourceWorkingTree}, r.Source)
}

func TestPalette_DiffChoiceStaysWithItsFolder(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	login := threadReview(t, a, loginID)
	run(h.p.showDiff(login, diffChoice{source: t3client.DiffSource{Kind: t3client.SourceBranchRange, BaseRef: "main"}, label: "vs main"},
		annotatedModel().store))
	assert.Equal(t, "main", h.p.newReview(a, login.Target).Source.BaseRef, "the same folder compares the same way")
	other := login.Target
	other.Cwd = "/srv/elsewhere"
	assert.Equal(t, t3client.DiffSource{}, h.p.newReview(a, other).Source, "another folder compares what the run says")
}

func TestPalette_DiffWithoutBranches(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	a.server.refsErr = errors.New("not a repository")
	h := newHarness(t, Config{NoWatch: true, NoChat: true}, threadReview(t, a, loginID))
	assert.Contains(t, flashes(run(h.p.diffCmd(annotatedModel()))), "T3: list branches: not a repository")

	a.server.refsErr = t3client.ErrNotRepository
	assert.Equal(t, []string{"T3: Sabra2 / Fix login is not a git repository: there is no diff to choose"},
		flashes(run(h.p.diffCmd(annotatedModel()))[1:]), "a folder that is no repository offers nothing to compare")

	h = newHarness(t, Config{NoWatch: true, NoChat: true}, nil)
	assert.Equal(t, []string{"T3: open a thread first"}, flashes(run(h.p.diffCmd(nil))))
}

// fakeCompacter is a review that can show the changed blocks only.
type fakeCompacter struct {
	tea.Model
	compact, applicable bool
}

func (f fakeCompacter) Compact() bool           { return f.compact }
func (f fakeCompacter) CompactApplicable() bool { return f.applicable }
func (f fakeCompacter) CompactContext() int     { return 7 }

func TestKeepViewRecordsWholeFiles(t *testing.T) {
	p := &Palette{}
	p.keepView(nil)
	p.keepView(fakeCompacter{applicable: false})
	assert.Equal(t, View{}, p.view, "a model that cannot show blocks leaves it")

	p.keepView(fakeCompacter{compact: true, applicable: true})
	assert.Equal(t, View{Context: 7}, p.view, "the blocks on screen are kept for the next review")

	p.keepView(fakeCompacter{applicable: true})
	assert.Equal(t, View{WholeFiles: true, Context: 7}, p.view)
	assert.Equal(t, View{WholeFiles: true, Context: 7}, p.newReview(nil, t3client.Target{}).View, "a new review shows its files the same way")
}

func TestPalette_ShowViewSwitchesToFilesAndBack(t *testing.T) {
	a := newFakeSession("ssh://a", testShell())
	a.server.refs = diffRefs()
	h := newHarness(t, Config{NoWatch: true, NoChat: true, DiffLabel: "uncommitted"}, threadReview(t, a, loginID))
	h.connect(t, "ssh://a", a)
	shown := annotatedModel()

	picker := find[splitview.ShowPickerMsg](t, run(h.p.diffCmd(shown)))
	h.swap(find[splitview.SwitchWorkspaceMsg](t, run(picker.OnPick(picker.Items[1]))), shown) // unpushed

	files := shownReview(t, run(h.p.ShowView(shown, true)))
	assert.Equal(t, t3client.SourceFiles, files.Source.Kind, "every file of the folder")
	assert.Equal(t, loginID, files.Target.Thread.ID)
	h.swap(find[splitview.SwitchWorkspaceMsg](t, run(h.p.ShowView(shown, true))), shown)
	assert.Contains(t, commandDetail(h.p.Commands(), "T3: Diff View…"), "file tree")

	back := shownReview(t, run(h.p.ShowView(shown, false)))
	assert.Equal(t, t3client.DiffSource{Kind: t3client.SourceBranchRange, BaseRef: "origin/feature"}, back.Source,
		"the changes come back compared as chosen last")

	picker = find[splitview.ShowPickerMsg](t, run(h.p.diffCmd(shown)))
	tree := shownReview(t, run(picker.OnPick(picker.Items[len(picker.Items)-1])))
	assert.Equal(t, t3client.SourceFiles, tree.Source.Kind, "the picker's File tree row does the same")
}
