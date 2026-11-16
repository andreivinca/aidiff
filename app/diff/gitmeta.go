package diff

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxGitFileSize bounds how much of a .git file is read: a real one holds a
// single "gitdir: <path>" line.
const maxGitFileSize = 4096

// isGitMetadata reports whether dir/.git is usable repository metadata rather
// than an empty, stale or placeholder entry: a directory holding HEAD, or a
// "gitdir: <path>" file (linked worktrees, submodules) whose target directory
// exists. It reads the filesystem only, so detection spawns no git process,
// and an invalid entry lets DetectVCS keep walking up to an enclosing repository.
func isGitMetadata(dir string) bool {
	meta := filepath.Join(dir, ".git")
	info, err := os.Stat(meta)
	if err != nil {
		return false
	}
	if info.IsDir() {
		head, headErr := os.Stat(filepath.Join(meta, "HEAD"))
		return headErr == nil && head.Mode().IsRegular()
	}
	target := gitFileTarget(meta)
	if target == "" {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	targetInfo, err := os.Stat(target)
	return err == nil && targetInfo.IsDir()
}

// gitFileTarget returns the path a .git file points to, or "" when the file
// cannot be read or does not start with a "gitdir:" line.
func gitFileTarget(path string) string {
	f, err := os.Open(path) //nolint:gosec // path is the .git entry of a directory being probed
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(io.LimitReader(f, maxGitFileSize))
	if !scanner.Scan() {
		return ""
	}
	target, ok := strings.CutPrefix(scanner.Text(), "gitdir:")
	if !ok {
		return ""
	}
	return strings.TrimSpace(target)
}
