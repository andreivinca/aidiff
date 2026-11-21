package t3client

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/umputun/revdiff/app/diff"
)

// SourceFiles is a review of every file of the folder instead of its
// changes: not a getDiffPreview kind, but read with a FolderReader.
const SourceFiles = "files"

// FolderReader browses a folder on a T3 host the way diff.FolderReader
// browses a local one: it lists the root and the folders opened in the tree,
// one projects.listEntries call each, and shows a file whole, read with
// projects.readFile.
type FolderReader struct {
	conn *Conn
	cwd  string

	mu       sync.Mutex
	expanded map[string]bool // open folders, relative to cwd
}

// NewFolderReader browses cwd on the host c is connected to.
func NewFolderReader(c *Conn, cwd string) *FolderReader {
	return &FolderReader{conn: c, cwd: cwd, expanded: map[string]bool{}}
}

// SetDirectory opens or closes a folder, its subfolders closing with it; the
// next ChangedFiles lists the result.
func (r *FolderReader) SetDirectory(path string, expanded bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if expanded {
		r.expanded[path] = true
		return
	}
	for dir := range r.expanded {
		if dir == path || strings.HasPrefix(dir, path+"/") {
			delete(r.expanded, dir)
		}
	}
}

// projectEntry is one entry of a projects.listEntries result.
type projectEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // file or directory
}

// ChangedFiles lists the root and every open folder: files, and folders as
// IsDir entries (Expanded when open), sorted by path. A folder that can no
// longer be listed is shown closed and forgotten; only the root's failure is
// an error.
func (r *FolderReader) ChangedFiles(_ string, _ bool) ([]diff.FileEntry, error) {
	r.mu.Lock()
	open := make(map[string]bool, len(r.expanded))
	for dir := range r.expanded {
		open[dir] = true
	}
	r.mu.Unlock()

	var out []diff.FileEntry
	var list func(dir string) error
	list = func(dir string) error {
		entries, err := r.list(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Kind != "directory" {
				out = append(out, diff.FileEntry{Path: e.Path})
				continue
			}
			out = append(out, diff.FileEntry{Path: e.Path, IsDir: true, Expanded: open[e.Path]})
			if !open[e.Path] {
				continue
			}
			if err := list(e.Path); err != nil {
				out[len(out)-1].Expanded = false
				r.SetDirectory(e.Path, false)
			}
		}
		return nil
	}
	if err := list(""); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b diff.FileEntry) int {
		return strings.Compare(a.Path, b.Path)
	})
	return out, nil
}

// list is the children of dir ("" for the root).
func (r *FolderReader) list(dir string) ([]projectEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var res struct {
		Entries []projectEntry `json:"entries"`
	}
	if err := r.conn.call(ctx, "projects.listEntries", map[string]any{"cwd": r.cwd, "directoryPath": dir}, &res); err != nil {
		return nil, fmt.Errorf("list %s: %w", cmpOr(dir, "the folder"), err)
	}
	return res.Entries, nil
}

// FileDiff is the file's text as context lines. A file the server will not
// show as text (see unshowable) is one placeholder line saying why; a file
// it cut short ends with a line saying so.
func (r *FolderReader) FileDiff(req diff.FileDiffRequest) ([]diff.DiffLine, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var res struct {
		Contents  string `json:"contents"`
		Truncated bool   `json:"truncated"`
	}
	err := r.conn.call(ctx, "projects.readFile", map[string]any{"cwd": r.cwd, "relativePath": req.Path}, &res)
	if refused, ok := errors.AsType[*rpcError](err); ok && unshowable(refused) {
		detail := cmpOr(refused.Detail, "the T3 server cannot show this file")
		return []diff.DiffLine{{ChangeType: diff.ChangeContext, Content: detail, IsPlaceholder: true}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", req.Path, err)
	}
	reader, err := diff.NewStdinReaderFromReader(req.Path, strings.NewReader(res.Contents))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", req.Path, err)
	}
	lines, err := reader.FileDiff(diff.FileDiffRequest{Path: req.Path})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", req.Path, err)
	}
	if res.Truncated {
		lines = append(lines, diff.DiffLine{ChangeType: diff.ChangeContext, Content: "the T3 server cut the file short here", IsPlaceholder: true})
	}
	return lines, nil
}

// unshowable reports whether a projects.readFile failure is about the file
// itself, which then shows as a placeholder: binary, not a regular file, a
// symlink leading out of the folder, or one that cannot be opened or read.
// Anything else, such as a folder the server no longer knows, is an error:
// a placeholder would pass it off as a property of the file.
func unshowable(e *rpcError) bool {
	if e.Tag != "ProjectReadFileError" {
		return false
	}
	switch e.Failure {
	case "binary_file", "path_not_file", "resolved_path_outside_root":
		return true
	case "operation_failed":
		// realpath-workspace-root is the folder failing, not the file
		switch e.Operation {
		case "realpath-target", "open", "stat", "read":
			return true
		}
	}
	return false
}

// cutQuery shortens a trimmed query to what projects.searchEntries takes:
// whole characters only, and trimmed again, since the server also refuses
// a query that ends in a space.
func cutQuery(query string) string {
	units := 0
	for i, r := range query {
		if units += utf16.RuneLen(r); units > searchQueryMax {
			return strings.TrimSpace(query[:i])
		}
	}
	return query
}

func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// searchEntriesMax is the most matches projects.searchEntries returns.
const searchEntriesMax = 200

// searchQueryMax is the longest query projects.searchEntries takes, in
// UTF-16 code units: the server checks a JavaScript string's length.
const searchQueryMax = 256

// SearchFiles finds the files of the whole folder matching query, opened
// folders or not, best first, with projects.searchEntries: at most limit,
// and never more than the server's 200. An empty query finds none.
func (r *FolderReader) SearchFiles(ctx context.Context, query string, limit int) (diff.FoundPaths, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return diff.FoundPaths{}, nil
	}
	if limit <= 0 || limit > searchEntriesMax {
		limit = searchEntriesMax
	}
	query = cutQuery(query)
	var res struct {
		Entries   []projectEntry `json:"entries"`
		Truncated bool           `json:"truncated"`
	}
	params := map[string]any{"cwd": r.cwd, "query": query, "limit": limit, "kind": "file"}
	if err := r.conn.call(ctx, "projects.searchEntries", params, &res); err != nil {
		return diff.FoundPaths{}, fmt.Errorf("search files: %w", err)
	}
	found := diff.FoundPaths{Truncated: res.Truncated}
	for _, e := range res.Entries {
		if e.Kind != "directory" {
			found.Paths = append(found.Paths, e.Path)
		}
	}
	return found, nil
}
