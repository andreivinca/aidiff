// Package revert undoes a review's changes in the working tree of a local git
// repository: one change block, by editing the file, or a whole file, through
// git. It reverts only where the review's new side is the working tree: the
// unstaged changes (against the index) or the working tree against a ref.
package revert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// are taken out of the file on disk and its removed lines put back. A block
// that covers a deleted file restores the file through git. A new file left
// empty is removed.
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
	at := newPosition(lines, start)
	edited, err := content.revert(block, at)
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
	if err := os.WriteFile(path, []byte(edited.String()), info.Mode().Perm()); err != nil { //nolint:gosec // the file's own mode
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

// fileLines is a text file as lines without their endings.
type fileLines struct {
	lines    []string
	crlf     bool // lines end in \r\n
	trailEOL bool // the last line ends with a line ending
}

// splitLines reads text as lines.
func splitLines(text string) fileLines {
	f := fileLines{crlf: strings.Contains(text, "\r\n"), trailEOL: strings.HasSuffix(text, "\n")}
	if text == "" {
		return fileLines{trailEOL: true}
	}
	body := strings.TrimSuffix(text, "\n")
	for l := range strings.SplitSeq(body, "\n") {
		f.lines = append(f.lines, strings.TrimSuffix(l, "\r"))
	}
	return f
}

// String is the file's text.
func (f fileLines) String() string {
	if len(f.lines) == 0 {
		return ""
	}
	eol := "\n"
	if f.crlf {
		eol = "\r\n"
	}
	out := strings.Join(f.lines, eol)
	if f.trailEOL {
		out += eol
	}
	return out
}

// revert replaces the block's added lines, which must be at line at, with
// its removed lines.
func (f fileLines) revert(block []diff.DiffLine, at int) (fileLines, error) {
	var added, removed []string
	for _, dl := range block {
		text := strings.TrimSuffix(dl.Content, "\r")
		if dl.ChangeType == diff.ChangeAdd {
			added = append(added, text)
		} else {
			removed = append(removed, text)
		}
	}
	from := at - 1
	if from < 0 || from+len(added) > len(f.lines) {
		return fileLines{}, ErrStale
	}
	for i, want := range added {
		if f.lines[from+i] != want {
			return fileLines{}, ErrStale
		}
	}
	out := make([]string, 0, len(f.lines)-len(added)+len(removed))
	out = append(out, f.lines[:from]...)
	out = append(out, removed...)
	out = append(out, f.lines[from+len(added):]...)
	return fileLines{lines: out, crlf: f.crlf, trailEOL: f.trailEOL || len(f.lines) == 0}, nil
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
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed git subcommands
	cmd.Dir = g.Root
	cmd.Env = append(os.Environ(), "GIT_LITERAL_PATHSPECS=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", args[0], msg)
	}
	return nil
}
