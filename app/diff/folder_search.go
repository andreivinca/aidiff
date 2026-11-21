package diff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// folderIndexTTL is how long a folder's file list serves searches
	// before it is listed again: long enough for one search being typed.
	folderIndexTTL = 10 * time.Second
	// folderIndexMax bounds the files a walk of a folder outside git lists.
	folderIndexMax = 200_000
)

// errIndexFull stops a walk that reached folderIndexMax.
var errIndexFull = errors.New("folder index full")

// folderIndex is the file list searches of a folder share for a while.
type folderIndex struct {
	mu      sync.Mutex
	files   []string
	loaded  bool // files was listed at at; an empty folder lists nil
	partial bool // the walk stopped at folderIndexMax
	at      time.Time
}

// SearchFiles finds the files of the whole folder matching query, opened
// folders or not, at most limit of them, best first (RankPaths). In a git
// work tree they are the tracked and untracked files git does not ignore;
// elsewhere every file, up to folderIndexMax. An empty query finds none.
func (r *FolderReader) SearchFiles(ctx context.Context, query string, limit int) (FoundPaths, error) {
	if strings.TrimSpace(query) == "" {
		return FoundPaths{}, nil
	}
	files, partial, err := r.indexedFiles(ctx)
	if err != nil {
		return FoundPaths{}, err
	}
	found := RankPaths(query, files, limit)
	found.Truncated = found.Truncated || partial
	return found, nil
}

// indexedFiles is the folder's file list, listed again once it is older
// than folderIndexTTL.
func (r *FolderReader) indexedFiles(ctx context.Context) ([]string, bool, error) {
	idx := &r.index
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.loaded && time.Since(idx.at) < folderIndexTTL {
		return idx.files, idx.partial, nil
	}
	files, partial, err := r.listGitFiles(ctx)
	if err != nil {
		files, partial, err = r.walkFiles(ctx)
	}
	if err != nil {
		return nil, false, err
	}
	kept := files[:0]
	for _, f := range files {
		if r.shows(f, false) {
			kept = append(kept, f)
		}
	}
	idx.files, idx.loaded, idx.partial, idx.at = kept, true, partial, time.Now()
	return kept, partial, nil
}

// listGitFiles lists the files git knows of under the root, tracked or
// untracked, ignored ones left out; an error outside a git work tree. A
// file with a merge conflict is listed once per stage, so names are
// deduplicated here: ls-files --deduplicate needs git 2.31, and an older
// git failing on it would fall back to walkFiles, which ignores .gitignore.
func (r *FolderReader) listGitFiles(ctx context.Context) ([]string, bool, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = r.root
	out, err := cmd.Output()
	if err != nil {
		return nil, false, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	seen := make(map[string]bool)
	for name := range bytes.SplitSeq(out, []byte{0}) {
		if len(name) > 0 && !seen[string(name)] {
			seen[string(name)] = true
			files = append(files, string(name))
		}
	}
	return files, false, nil
}

// walkFiles lists every file under the root, VCS metadata left out, up to
// folderIndexMax; partial reports that the walk stopped there.
func (r *FolderReader) walkFiles(ctx context.Context) (files []string, partial bool, err error) {
	err = filepath.WalkDir(r.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == r.root {
				return err
			}
			return nil // an unreadable folder is skipped
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() {
			if p != r.root && isVCSMetadata(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(r.root, p)
		if relErr != nil {
			return nil //nolint:nilerr // a path outside the root is not listed
		}
		files = append(files, filepath.ToSlash(rel))
		if len(files) >= folderIndexMax {
			return errIndexFull
		}
		return nil
	})
	if errors.Is(err, errIndexFull) {
		return files, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("list folder %s: %w", r.root, err)
	}
	return files, false, nil
}
