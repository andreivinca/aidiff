package t3client

import (
	"context"
	"fmt"
	"strings"

	"github.com/umputun/revdiff/app/diff"
)

// A T3 server builds each file's patch with git's default context, and
// review.getDiffPreview takes no option for more. The whole-files view and
// the context size (C, alt+= / alt+-) need the unchanged lines the patch
// leaves out, so they come from review.getDiffFileContents, which T3's own
// UI uses to expand a diff: the patch is laid over the file's new contents,
// and the result trimmed to the context asked for.

const (
	patchContext        = 3       // the context git, and so the server, puts in a patch
	fullContextSentinel = 1000000 // a context this large, or <= 0, is the whole file
)

// fileContents is review.getDiffFileContents's result.
type fileContents struct {
	OldContents string `json:"oldContents"`
	NewContents string `json:"newContents"`
}

// contentsFetch is one file's new contents, fetched once however many views
// ask at a time: done is closed when lines and err are set.
type contentsFetch struct {
	done  chan struct{}
	lines []string
	err   error
}

// withContext gives a file's patch lines the context req asks for. It
// returns lines unchanged when the patch already has it, when the file is
// not one of the listing's (an older server's one patch), or when its
// contents cannot be fetched or no longer match the patch: the patch is
// still right, only with less around each change.
func (r *Renderer) withContext(req diff.FileDiffRequest, lines []diff.DiffLine) []diff.DiffLine {
	if req.ContextLines == patchContext || !expandable(lines) {
		return lines
	}
	newLines, err := r.newContentsOf(req.Path)
	if err != nil {
		return lines
	}
	full, ok := expandFull(lines, newLines)
	if !ok {
		return lines
	}
	if req.ContextLines <= 0 || req.ContextLines >= fullContextSentinel {
		return full
	}
	return trimContext(full, req.ContextLines)
}

// expandable reports whether lines are a text diff that can be expanded:
// not empty, a binary or placeholder row.
func expandable(lines []diff.DiffLine) bool {
	if len(lines) == 0 {
		return false
	}
	for _, l := range lines {
		if l.IsBinary || l.IsPlaceholder || l.IsSubmodule {
			return false
		}
	}
	return true
}

// newContentsOf is path's new contents as lines, fetched the first time.
func (r *Renderer) newContentsOf(path string) ([]string, error) {
	r.mu.Lock()
	stat, listed := r.stats[path]
	if !listed || r.contents == nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("%s is not listed", path)
	}
	if cf, ok := r.contents[path]; ok {
		r.mu.Unlock()
		<-cf.done
		return cf.lines, cf.err
	}
	cf := &contentsFetch{done: make(chan struct{})}
	r.contents[path] = cf
	contents := r.contents
	r.mu.Unlock()

	cf.lines, cf.err = r.fetchContents(stat)
	if cf.err != nil {
		r.mu.Lock()
		if contents[path] == cf {
			delete(contents, path) // the next view asks again
		}
		r.mu.Unlock()
	}
	close(cf.done)
	return cf.lines, cf.err
}

// fetchContents asks the server for one file's contents.
func (r *Renderer) fetchContents(stat diffFileStat) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	changeType := stat.ChangeType
	if changeType == "" {
		changeType = "change"
	}
	oldPath := stat.Path
	if stat.PreviousPath != nil && *stat.PreviousPath != "" {
		oldPath = *stat.PreviousPath
	}
	var baseRef any // null: the server's default
	if r.baseRef != "" {
		baseRef = r.baseRef
	}
	payload := map[string]any{
		"cwd": r.cwd, "sourceKind": r.source, "changeType": changeType,
		"baseRef": baseRef, "headRef": nil, "oldPath": oldPath, "newPath": stat.Path,
	}
	var res fileContents
	if err := r.conn.call(ctx, "review.getDiffFileContents", payload, &res); err != nil {
		return nil, fmt.Errorf("T3 contents of %s: %w", stat.Path, err)
	}
	return splitLines(res.NewContents), nil
}

// splitLines splits a file's text into its lines, without the final
// newline's empty one.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// expandFull lays a patch's lines over the file's new lines: every new line
// the patch leaves out becomes a context line, so the result is the whole
// file with its changes. It reports false when the contents do not match
// the patch, such as a file changed since the patch was made.
func expandFull(lines []diff.DiffLine, newLines []string) ([]diff.DiffLine, bool) {
	out := make([]diff.DiffLine, 0, len(newLines)+len(lines))
	oldPos, newPos := 1, 1 // the next unchanged line's numbers
	fill := func(untilNew int) bool {
		for ; newPos < untilNew; newPos, oldPos = newPos+1, oldPos+1 {
			if newPos > len(newLines) {
				return false
			}
			out = append(out, diff.DiffLine{OldNum: oldPos, NewNum: newPos, Content: newLines[newPos-1], ChangeType: diff.ChangeContext})
		}
		return true
	}
	for _, l := range lines {
		switch l.ChangeType {
		case diff.ChangeDivider:
			continue
		case diff.ChangeRemove:
			if !fill(newPos + l.OldNum - oldPos) {
				return nil, false
			}
			oldPos = l.OldNum + 1
		default: // context or add
			if !fill(l.NewNum) || l.NewNum > len(newLines) || newLines[l.NewNum-1] != l.Content {
				return nil, false
			}
			newPos = l.NewNum + 1
			if l.ChangeType == diff.ChangeContext {
				oldPos = l.OldNum + 1
			}
		}
		out = append(out, l)
	}
	if !fill(len(newLines) + 1) {
		return nil, false
	}
	return out, true
}

// trimContext keeps n unchanged lines around each change of a whole file's
// lines, and replaces every other run of unchanged lines by a divider
// counting them, as git's diff would with -U<n>.
func trimContext(full []diff.DiffLine, n int) []diff.DiffLine {
	keep := make([]bool, len(full))
	for i, l := range full {
		if l.ChangeType == diff.ChangeContext {
			continue
		}
		for j := max(i-n, 0); j <= min(i+n, len(full)-1); j++ {
			keep[j] = true
		}
	}
	out := make([]diff.DiffLine, 0, len(full))
	skipped := 0
	flush := func() {
		switch {
		case skipped == 1:
			out = append(out, diff.DiffLine{ChangeType: diff.ChangeDivider, Content: "⋯ 1 line ⋯"})
		case skipped > 1:
			out = append(out, diff.DiffLine{ChangeType: diff.ChangeDivider, Content: fmt.Sprintf("⋯ %d lines ⋯", skipped)})
		}
		skipped = 0
	}
	for i, l := range full {
		if !keep[i] {
			skipped++
			continue
		}
		flush()
		out = append(out, l)
	}
	flush()
	return out
}
