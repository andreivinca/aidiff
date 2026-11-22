package main

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

// A single positional argument names either a git ref, as in revdiff, or a
// folder to review, as if the run started there: `aidiff .`, `aidiff
// ~/src/app`. It is a folder when written as a path (folderForm), or when it
// names an existing directory and no revision of the repository the run is
// in; a branch named like a folder stays a ref unless written ./name or
// name/. Two arguments, and a range (a..b), are always refs.

// folderForm reports whether arg is written as a path: ".", "..", starting
// with "/", "./", "../" or "~", or ending with "/". No git ref is spelled so:
// a ref neither starts nor ends with a slash, and holds no "~" but after a
// name ("HEAD~3").
func folderForm(arg string) bool {
	switch {
	case arg == "." || arg == "..":
		return true
	case strings.HasSuffix(arg, "/"):
		return true
	}
	for _, prefix := range []string{"/", "./", "../", "~"} {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}

// withFolderForm takes a single positional argument written as a path for
// the folder to review instead of a ref, before the flags are checked
// against refs. Whether it exists is resolveStartArg's to say.
func (o options) withFolderForm() options {
	if o.Refs.Against == "" && folderForm(o.Refs.Base) {
		o.folder, o.Refs.Base = o.Refs.Base, ""
	}
	return o
}

// validateFolderArg refuses a folder argument with a flag that reviews
// something else: stdin, a compare pair, a T3 thread, or files of their own.
func validateFolderArg(o options) error {
	if o.folder == "" {
		return nil
	}
	for _, c := range []struct {
		set  bool
		flag string
	}{
		{o.Stdin, "--stdin"},
		{o.CompareOld != "" || o.CompareNew != "", "--compare-old/--compare-new"},
		{o.t3Mode(), "--t3"},
		{len(o.Only) > 0, "--only"},
	} {
		if c.set {
			return fmt.Errorf("a folder (%s) cannot be used with %s", o.folder, c.flag)
		}
	}
	return nil
}

// resolveStartArg settles the positional argument against the disk, cwd
// being the working directory: a folder argument must be a directory, and
// becomes its absolute path; a single ref that is no revision of the git
// repository the run is in becomes the folder it names, else it is refused
// with why. Ranges, two refs and the revisions of hg and jj, which git
// cannot check, go through as refs, as before.
func resolveStartArg(o options, cwd string) (options, error) {
	if o.folder != "" {
		dir, err := folderPath(o.folder, cwd)
		if err != nil {
			return options{}, err
		}
		o.folder = dir
		return o, nil
	}
	arg := o.Refs.Base
	if arg == "" || o.Refs.Against != "" || strings.Contains(arg, "..") || strings.HasPrefix(arg, "-") {
		return o, nil
	}
	dir := filepath.Join(cwd, arg)
	vcs, root := diff.DetectVCS(cwd)
	switch vcs {
	case diff.VCSGit:
		switch isRevision(root, arg) {
		case revYes:
			return o, nil
		case revUnknown:
			return o, nil // git could not say: a ref, as before
		}
		if isDir(dir) {
			o.folder, o.Refs.Base = dir, ""
			return o, nil
		}
		return options{}, fmt.Errorf("%q is neither a folder nor a git revision", arg)
	case diff.VCSNone:
		if isDir(dir) {
			o.folder, o.Refs.Base = dir, ""
			return o, nil
		}
		return options{}, fmt.Errorf("%q is not a folder, and a ref needs a repository", arg)
	}
	return o, nil // hg, jj: a revision of theirs; ./name names the folder
}

// folderPath is folder argument arg as an absolute path, ~ expanded, once
// it is known to be a directory.
func folderPath(arg, cwd string) (string, error) {
	dir := arg
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("folder %s: %w", arg, err)
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	dir = filepath.Clean(dir)
	fi, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("folder %s does not exist", arg)
	case err != nil:
		return "", fmt.Errorf("folder %s: %w", arg, err)
	case !fi.IsDir():
		return "", fmt.Errorf("%s is not a folder", arg)
	}
	return dir, nil
}

// isDir reports whether dir is a directory.
func isDir(dir string) bool {
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// revAnswer is what git says of a name: a commit, none, or nothing (git
// could not run).
type revAnswer int

const (
	revNo revAnswer = iota
	revYes
	revUnknown
)

// revisionCheck bounds asking git whether a name is a revision.
const revisionCheck = 5 * time.Second

// isRevision asks git, in repository root, whether arg names a commit
// there. A variable for the tests.
var isRevision = func(root, arg string) revAnswer {
	ctx, cancel := context.WithTimeout(context.Background(), revisionCheck)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", arg+"^{commit}") //nolint:gosec // a name the user typed, checked by git
	cmd.Dir = root
	err := cmd.Run()
	if err == nil {
		return revYes
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok && ctx.Err() == nil {
		return revNo
	}
	return revUnknown
}
