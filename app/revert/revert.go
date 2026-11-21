// Package revert undoes a review's changes in the working tree of a local git
// repository: one change block, by editing the file, or a whole file, through
// git. It reverts only where the review's new side is the working tree: the
// unstaged changes (against the index) or the working tree against a ref.
package revert

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/umputun/revdiff/app/diff"
)

// gitTimeout bounds one git call.
const gitTimeout = 15 * time.Second

// ErrStale means the file on disk no longer holds the change the review
// shows, so reverting it could undo something else.
var ErrStale = errors.New("the file changed since the review loaded it; reload (R) and try again")

// Git reverts the changes of a review of the repository at Root.
type Git struct {
	Root string // the repository's top folder; review paths are relative to it
	// Ref is what the working tree is compared against: "" for the index,
	// else a commit. A revert against HEAD also unstages, so the change
	// leaves "uncommitted" entirely.
	Ref string
}

// RevertFile puts file back as the review's base has it: restored from the
// base, or, for a file the base lacks, removed. A rename does both.
func (g Git) RevertFile(file diff.FileEntry) error {
	switch file.Status {
	case diff.FileAdded, diff.FileUntracked:
		return g.remove(file.Path)
	case diff.FileRenamed:
		if file.OldPath != "" && file.OldPath != file.Path {
			if err := g.restore(file.OldPath); err != nil {
				return err
			}
			return g.remove(file.Path)
		}
	default:
	}
	return g.restore(file.Path)
}

// RevertChange undoes the change block lines[start:end+1] (added and removed
// lines only) of file, whose whole diff is lines: the block's added lines
// are taken out of the file on disk and its removed lines put back, and,
// against HEAD, out of the index too. A block that covers a deleted file
// restores the file through git. A new file left empty is removed.
func (g Git) RevertChange(file diff.FileEntry, lines []diff.DiffLine, start, end int) error {
	if start < 0 || end >= len(lines) || start > end {
		return errors.New("no change under the cursor")
	}
	block := lines[start : end+1]
	for _, dl := range block {
		if dl.ChangeType != diff.ChangeAdd && dl.ChangeType != diff.ChangeRemove {
			return errors.New("no change under the cursor")
		}
		if dl.IsBinary || dl.IsPlaceholder || dl.IsSubmodule {
			return errors.New("this change cannot be reverted line by line; revert the whole file")
		}
	}
	if file.Status == diff.FileDeleted {
		return g.RevertFile(file)
	}
	path, err := g.abs(file.Path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path) //nolint:gosec // a file of the reviewed repository
	if err != nil {
		return fmt.Errorf("read %s: %w", file.Path, err)
	}
	content := splitLines(string(data))
	// the block's own added lines are checked by apply; a removal-only block
	// has none, so the lines around it are what proves where it goes back
	if !content.holdsNewSide(lines, start, end) {
		return ErrStale
	}
	e := newEdit(block, newPosition(lines, start))
	final := keepFinalEOL
	if e.from+len(e.added) == len(content.lines) && len(e.removed) > 0 {
		final = g.baseFinalEOL(file) // the restored lines end the file
	}
	edited, err := content.apply(e, final)
	if err != nil {
		return err
	}
	if len(edited.lines) == 0 && (file.Status == diff.FileAdded || file.Status == diff.FileUntracked) {
		return g.remove(file.Path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", file.Path, err)
	}
	undo := func() {}
	if g.Ref == "HEAD" && file.Status != diff.FileUntracked {
		lead, trail := contextAround(lines, start, end)
		if undo, err = g.unstage(file.Path, content, edited, e, lead, trail); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, []byte(edited.String()), info.Mode().Perm()); err != nil { //nolint:gosec // the file's own mode
		undo()
		return fmt.Errorf("write %s: %w", file.Path, err)
	}
	return nil
}

// newPosition is the 1-based line of the new file where the block starting
// at lines[start] sits: its first added line's, else the line its removed
// lines go back before, counted from the old file's line numbers and the
// changes above it.
func newPosition(lines []diff.DiffLine, start int) int {
	for _, dl := range lines[start:] {
		if dl.ChangeType == diff.ChangeAdd {
			return dl.NewNum
		}
		if dl.ChangeType != diff.ChangeRemove {
			break
		}
	}
	shift := 0
	for _, dl := range lines[:start] {
		switch dl.ChangeType {
		case diff.ChangeAdd:
			shift++
		case diff.ChangeRemove:
			shift--
		default:
		}
	}
	return lines[start].OldNum + shift
}

// contextChecked is how many of the review's new-side lines on each side of
// a block must still be where the review saw them for the block to revert.
const contextChecked = 3

// holdsNewSide reports whether the file still has, at their line numbers,
// the review's new-side lines (context and added) nearest the block
// lines[start:end+1], up to contextChecked on each side: lines inserted or
// removed above the block since the review loaded move them.
func (f fileLines) holdsNewSide(lines []diff.DiffLine, start, end int) bool {
	onNewSide := func(dl diff.DiffLine) bool {
		return (dl.ChangeType == diff.ChangeContext || dl.ChangeType == diff.ChangeAdd) && dl.NewNum > 0 &&
			!dl.IsBinary && !dl.IsPlaceholder && !dl.IsSubmodule
	}
	matches := func(dl diff.DiffLine) bool {
		i := dl.NewNum - 1
		return i < len(f.lines) && sameLine(f.lines[i], dl.Content)
	}
	found := false
	for _, dir := range []int{-1, 1} {
		i, checked := start-1, 0
		if dir > 0 {
			i = end + 1
		}
		for ; i >= 0 && i < len(lines) && checked < contextChecked; i += dir {
			if !onNewSide(lines[i]) {
				continue // a removed line or a divider is not in the file
			}
			if !matches(lines[i]) {
				return false
			}
			checked++
			found = true
		}
	}
	for _, dl := range lines[start : end+1] {
		found = found || dl.ChangeType == diff.ChangeAdd
	}
	// a diff with no new-side line at all is of a file emptied: one that
	// is not empty now changed
	return found || len(f.lines) == 0
}

// contextAround counts the context lines right above and below the block
// lines[start:end+1], up to contextChecked each: the unchanged lines next to
// it, which every version of the file shares.
func contextAround(lines []diff.DiffLine, start, end int) (lead, trail int) {
	for i := start - 1; i >= 0 && lead < contextChecked && lines[i].ChangeType == diff.ChangeContext; i-- {
		lead++
	}
	for i := end + 1; i < len(lines) && trail < contextChecked && lines[i].ChangeType == diff.ChangeContext; i++ {
		trail++
	}
	return lead, trail
}

// sameLine reports whether a line of the file is the diff's line: the diff
// parser drops a carriage return the file may still end the line with.
func sameLine(fileLine, diffLine string) bool {
	return strings.TrimSuffix(fileLine, "\r") == strings.TrimSuffix(diffLine, "\r")
}

// fileLines is a text file as lines, each with its own ending, so editing
// some lines leaves the others byte for byte.
type fileLines struct {
	lines []string // without their endings
	eols  []string // each line's ending: "\n", "\r\n", or "" for a last line without one
}

// splitLines reads text as lines.
func splitLines(text string) fileLines {
	var f fileLines
	for text != "" {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			f.lines, f.eols = append(f.lines, text), append(f.eols, "")
			break
		}
		line, eol := text[:i], "\n"
		if strings.HasSuffix(line, "\r") {
			line, eol = line[:len(line)-1], "\r\n"
		}
		f.lines, f.eols = append(f.lines, line), append(f.eols, eol)
		text = text[i+1:]
	}
	return f
}

// String is the file's text.
func (f fileLines) String() string {
	var b strings.Builder
	for i, l := range f.lines {
		b.WriteString(l)
		b.WriteString(f.eols[i])
	}
	return b.String()
}

// edit is what reverting a block does to the file: its added lines, from
// the 0-based line from on, give way to its removed lines.
type edit struct {
	from           int
	added, removed []string
}

// newEdit is the edit reverting block, whose added lines sit at the 1-based
// line at.
func newEdit(block []diff.DiffLine, at int) edit {
	e := edit{from: at - 1}
	for _, dl := range block {
		text := strings.TrimSuffix(dl.Content, "\r")
		if dl.ChangeType == diff.ChangeAdd {
			e.added = append(e.added, text)
		} else {
			e.removed = append(e.removed, text)
		}
	}
	return e
}

// finalEOL says whether a file whose last lines a revert restores ends with
// a line ending.
type finalEOL int

const (
	keepFinalEOL finalEOL = iota // as the file on disk does: the base is not known
	withFinalEOL                 // the base ends with a line ending
	noFinalEOL                   // the base's last line has none
)

// apply makes the edit, refusing it with ErrStale when the added lines are
// not where it expects them. Every line keeps its own ending; a restored
// line takes its neighbours' (above, else below), and the file's last line
// ends as final says.
func (f fileLines) apply(e edit, final finalEOL) (fileLines, error) {
	if e.from < 0 || e.from+len(e.added) > len(f.lines) {
		return fileLines{}, ErrStale
	}
	for i, want := range e.added {
		if !sameLine(f.lines[e.from+i], want) {
			return fileLines{}, ErrStale
		}
	}
	eol := f.neighbourEOL(e)
	rest := e.from + len(e.added)
	out := fileLines{
		lines: make([]string, 0, len(f.lines)-len(e.added)+len(e.removed)),
		eols:  make([]string, 0, len(f.lines)-len(e.added)+len(e.removed)),
	}
	out.lines = append(append(append(out.lines, f.lines[:e.from]...), e.removed...), f.lines[rest:]...)
	out.eols = append(out.eols, f.eols[:e.from]...)
	for range e.removed {
		out.eols = append(out.eols, eol)
	}
	out.eols = append(out.eols, f.eols[rest:]...)
	if len(out.lines) == 0 {
		return out, nil
	}
	for i := range len(out.eols) - 1 {
		if out.eols[i] == "" { // a last line without an ending that lines now follow
			out.eols[i] = eol
		}
	}
	last := len(out.eols) - 1
	switch {
	case rest < len(f.lines) || len(e.removed) == 0: // the last line is one the edit left as it was
	case final == noFinalEOL:
		out.eols[last] = ""
	case final == withFinalEOL:
		out.eols[last] = eol
	case len(f.lines) > 0 && f.eols[len(f.lines)-1] == "":
		out.eols[last] = ""
	}
	return out, nil
}

// neighbourEOL is the line ending a restored line takes: the line above's,
// else the one below's, else any line's, else "\n".
func (f fileLines) neighbourEOL(e edit) string {
	near := []int{e.from - 1, e.from + len(e.added)}
	for i := range f.lines {
		near = append(near, i)
	}
	for _, i := range near {
		if i >= 0 && i < len(f.eols) && f.eols[i] != "" {
			return f.eols[i]
		}
	}
	return "\n"
}

// baseFinalEOL reads whether the review's base version of file ends with a
// line ending: the diff's lines do not say. keepFinalEOL when it cannot be
// read.
func (g Git) baseFinalEOL(file diff.FileEntry) finalEOL {
	path := file.Path
	if file.OldPath != "" {
		path = file.OldPath
	}
	spec := ":0:" + path // the index; ":0:" so a path like "1:x" is not read as a merge stage
	if g.Ref != "" {
		spec = g.Ref + ":" + path
	}
	out, err := g.run("", "cat-file", "blob", spec)
	switch {
	case err != nil || len(out) == 0:
		return keepFinalEOL
	case out[len(out)-1] == '\n':
		return withFinalEOL
	default:
		return noFinalEOL
	}
}

// unstage makes the edit in the index too, for a revert against HEAD: the
// staged copy of the block's region, before, becomes after. Nothing is
// staged when the index does not hold the block (it was never staged); an
// index holding another version of it fails, leaving the index as it was.
// undo puts the index back.
func (g Git) unstage(path string, before, after fileLines, e edit, lead, trail int) (undo func(), err error) {
	variants := []bool{false}
	if slices.Contains(before.eols, "\r\n") || slices.Contains(after.eols, "\r\n") {
		// with core.autocrlf or an eol attribute the index holds the lines
		// with \n where the file has \r\n
		variants = append(variants, true)
	}
	type attempt struct {
		patch string
		args  []string
	}
	attempts := make([]attempt, 0, len(variants))
	for _, lf := range variants {
		patch, zero := indexPatch(path, before, after, e, lead, trail, lf)
		args := []string{"apply", "--cached", "--whitespace=nowarn"}
		if zero {
			args = append(args, "--unidiff-zero")
		}
		attempts = append(attempts, attempt{patch: patch, args: args})
	}
	for _, a := range attempts {
		if _, err := g.run(a.patch, append(a.args, "--", "-")...); err == nil {
			return func() { _, _ = g.run(a.patch, append(a.args, "-R", "--", "-")...) }, nil
		}
	}
	for _, a := range attempts {
		if _, err := g.run(a.patch, append(a.args, "--check", "-R", "--", "-")...); err == nil {
			return func() {}, nil // the index already has the block reverted: it was not staged
		}
	}
	return nil, fmt.Errorf("the staged copy of this change in %s differs from the file, so it cannot be unstaged; "+
		"nothing was reverted (unstage it with git restore --staged, or revert the whole file with X)", path)
}

// indexPatch is the patch turning the edit's region of before into after,
// with up to lead and trail context lines around it, every line written
// with its own ending (\n for all of them when lf). zero reports a patch
// with no context below it although lines follow, which git apply takes to
// end the file unless told --unidiff-zero.
func indexPatch(path string, before, after fileLines, e edit, lead, trail int, lf bool) (patch string, zero bool) {
	lo := e.from - min(lead, e.from)
	hiB := min(len(before.lines), e.from+len(e.added)+trail)
	hiA := min(len(after.lines), e.from+len(e.removed)+(hiB-e.from-len(e.added)))
	type line struct{ text, eol string }
	region := func(f fileLines, hi int) []line {
		out := make([]line, 0, hi-lo)
		for i := lo; i < hi; i++ {
			eol := f.eols[i]
			if lf && eol == "\r\n" {
				eol = "\n"
			}
			out = append(out, line{f.lines[i], eol})
		}
		return out
	}
	old, cur := region(before, hiB), region(after, hiA)
	prefix := 0
	for prefix < len(old) && prefix < len(cur) && old[prefix] == cur[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(cur)-prefix && old[len(old)-1-suffix] == cur[len(cur)-1-suffix] {
		suffix++
	}
	start := func(n int) int { // an empty side names the line before the hunk
		if n == 0 {
			return lo
		}
		return lo + 1
	}
	var b strings.Builder
	a, bn := quotePatchPath("a/"+path), quotePatchPath("b/"+path)
	fmt.Fprintf(&b, "diff --git %s %s\n--- %s\n+++ %s\n", a, bn, a, bn)
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", start(len(old)), len(old), start(len(cur)), len(cur))
	write := func(mark byte, ls []line) {
		for _, l := range ls {
			b.WriteByte(mark)
			b.WriteString(l.text)
			if l.eol == "" {
				b.WriteString("\n\\ No newline at end of file\n")
				continue
			}
			b.WriteString(l.eol)
		}
	}
	write(' ', old[:prefix])
	write('-', old[prefix:len(old)-suffix])
	write('+', cur[prefix:len(cur)-suffix])
	write(' ', old[len(old)-suffix:])
	return b.String(), suffix == 0 && hiB < len(before.lines)
}

// quotePatchPath writes a patch's file name as git does: in C-style quotes
// when it holds a control character, a quote or a backslash.
func quotePatchPath(name string) string {
	if !strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '"' || r == '\\' }) {
		return name
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := range len(name) {
		switch c := name[i]; c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03o`, c)
				continue
			}
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// restore puts path's base version in the working tree, and, against HEAD,
// in the index too.
func (g Git) restore(path string) error {
	args := []string{"restore"}
	switch g.Ref {
	case "":
	case "HEAD":
		args = append(args, "--source=HEAD", "--staged", "--worktree")
	default:
		args = append(args, "--source="+g.Ref, "--worktree")
	}
	return g.git(append(args, "--", path)...)
}

// remove deletes path, a file the base lacks, from the working tree, and,
// against HEAD, from the index too.
func (g Git) remove(path string) error {
	if g.Ref == "HEAD" {
		if err := g.git("rm", "--cached", "--quiet", "--ignore-unmatch", "--", path); err != nil {
			return err
		}
	}
	abs, err := g.abs(path)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// abs is path inside the repository, refusing one that leaves it.
func (g Git) abs(path string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the repository", path)
	}
	return filepath.Join(g.Root, clean), nil
}

// git runs git in the repository, paths taken literally.
func (g Git) git(args ...string) error {
	_, err := g.run("", args...)
	return err
}

// run runs git in the repository with stdin as its input, paths taken
// literally, and returns what it printed.
func (g Git) run(stdin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed git subcommands
	cmd.Dir = g.Root
	cmd.Env = append(os.Environ(), "GIT_LITERAL_PATHSPECS=1")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.Bytes(), nil
}
