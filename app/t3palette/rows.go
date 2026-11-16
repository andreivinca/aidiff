package t3palette

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/umputun/revdiff/app/splitview"
	"github.com/umputun/revdiff/app/t3client"
)

// projectThreads are the threads of p, newest first, or every thread when p
// is nil.
func projectThreads(sh t3client.Shell, p *t3client.Project) []t3client.Thread {
	all := t3client.SortedThreads(sh)
	if p == nil {
		return all
	}
	var out []t3client.Thread
	for _, th := range all {
		if th.ProjectID == p.ID {
			out = append(out, th)
		}
	}
	return out
}

// threadItems renders threads as picker rows; the one with id current is
// marked.
func threadItems(sh t3client.Shell, threads []t3client.Thread, current string) []splitview.PickerItem {
	items := make([]splitview.PickerItem, 0, len(threads))
	for _, th := range threads {
		label := threadTitle(th)
		if th.ID == current {
			label = "▸ " + label
		}
		items = append(items, splitview.PickerItem{Label: label, Detail: threadDetail(sh, th), Value: th})
	}
	return items
}

// projectItems renders the projects still in use as picker rows, by title.
func projectItems(sh t3client.Shell) []splitview.PickerItem {
	var items []splitview.PickerItem
	for _, p := range sh.Projects {
		if p.Archived() {
			continue
		}
		items = append(items, splitview.PickerItem{Label: p.Title, Detail: p.WorkspaceRoot, Value: p})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
	})
	return items
}

// threadTitle names a thread for the palette.
func threadTitle(th t3client.Thread) string {
	if title := strings.TrimSpace(th.Title); title != "" {
		return title
	}
	return trunc(th.ID, 9)
}

// threadDetail is the thread's project, state and branch.
func threadDetail(sh t3client.Shell, th t3client.Thread) string {
	var parts []string
	if p, ok := sh.Project(th.ProjectID); ok {
		parts = append(parts, p.Title)
	}
	parts = append(parts, threadState(th))
	if th.Branch != nil && *th.Branch != "" {
		parts = append(parts, *th.Branch)
	}
	return strings.Join(parts, " · ")
}

// threadState is "running" while the thread's agent works, else "idle".
func threadState(th t3client.Thread) string {
	if th.Running() {
		return "running"
	}
	return "idle"
}

// WriteThreads prints sh's threads to w, most recently updated first, one
// per line: id, state, project, branch and title.
func WriteThreads(w io.Writer, sh t3client.Shell) error {
	threads := t3client.SortedThreads(sh)
	if len(threads) == 0 {
		if _, err := fmt.Fprintln(w, "no threads"); err != nil {
			return fmt.Errorf("write threads: %w", err)
		}
		return nil
	}
	for _, th := range threads {
		project := th.ProjectID
		if p, ok := sh.Project(th.ProjectID); ok {
			project = p.Title
		}
		branch := "-"
		if th.Branch != nil {
			branch = *th.Branch
		}
		if _, err := fmt.Fprintf(w, "%s  %-7s  %-20s  %-24s  %s\n", th.ID, threadState(th), trunc(project, 20), trunc(branch, 24), th.Title); err != nil {
			return fmt.Errorf("write threads: %w", err)
		}
	}
	return nil
}

// plural is the suffix of a count of n things.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// trunc shortens s to n runes, the last of them an ellipsis.
func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
