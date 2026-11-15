package diff

import (
	"fmt"
	"path/filepath"
	"strings"
)

// WatchDirs returns what a file watcher needs to observe this repository: the
// worktree root and the absolute git directory (which differs from root/.git in
// linked worktrees and submodules).
func (g *Git) WatchDirs() (root, gitDir string, err error) {
	out, err := g.runGit("rev-parse", "--show-toplevel", "--absolute-git-dir")
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return "", "", fmt.Errorf("unexpected git rev-parse output: %q", out)
	}
	return lines[0], lines[1], nil
}

// IgnoredDirs lists the directories under root that git ignores (node_modules,
// build output...), as absolute paths. --directory collapses each ignored tree
// to its top directory, so the list stays short and is cheap to produce.
func (g *Git) IgnoredDirs(root string) ([]string, error) {
	out, err := g.runGit("-C", root, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, err
	}
	var dirs []string
	for entry := range strings.SplitSeq(out, "\x00") {
		if strings.HasSuffix(entry, "/") {
			dirs = append(dirs, filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(entry, "/"))))
		}
	}
	return dirs, nil
}

// IsIgnored reports whether git ignores path. Used for directories created
// after startup, so a fresh node_modules is not watched file by file.
func (g *Git) IsIgnored(path string) bool {
	_, err := g.runGit("check-ignore", "-q", "--", path)
	return err == nil // exit 0: ignored; exit 1: not ignored; other errors: treat as not ignored
}
